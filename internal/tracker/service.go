// Package tracker connects user-scoped Jira Cloud and Trello accounts.
package tracker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

var (
	ErrConnect     = errors.New("tracker connection required")
	ErrDenied      = errors.New("tracker access denied")
	ErrArguments   = errors.New("invalid tracker arguments")
	ErrUnavailable = errors.New("tracker temporarily unavailable")
)

type Identity struct {
	UserID int64
	Team   string
}
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Credentials struct {
	Access, Refresh string
	Expires         time.Time
	Scopes          string
}
type Resource struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	SiteID    string `json:"site_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
}
type Item struct {
	ID          string `json:"id"`
	Key         string `json:"key,omitempty"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Status      string `json:"status,omitempty"`
	StatusID    string `json:"status_id,omitempty"`
	Description any    `json:"description,omitempty"`
	Assignee    string `json:"assignee,omitempty"`
	Due         string `json:"due,omitempty"`
}
type Page struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type Access struct {
	Account      Account    `json:"account"`
	Resources    []Resource `json:"resources"`
	WriteEnabled bool       `json:"write_enabled"`
}
type Connection struct {
	Identity
	Provider    string
	Account     Account
	Credentials Credentials
	Selected    []string
	Mutations   map[string]MutationReceipt
}
type State struct {
	UserID          int64
	Phase, Verifier string
	Pending         *Connection
	Expires         time.Time
}

type Store interface {
	SlackTeam(context.Context, int64) (string, error)
	PutState(context.Context, string, string, State) error
	ConsumeState(context.Context, string, string) (State, error)
	Save(context.Context, Connection) error
	WithConnection(context.Context, string, int64, func(*Connection) error) error
	Disconnect(context.Context, string, int64) error
}
type Provider interface {
	Kind() string
	AuthorizeURL(string, string) string
	Exchange(context.Context, string, string) (Credentials, error)
	Refresh(context.Context, string) (Credentials, error)
	Account(context.Context, string) (Account, error)
	Resources(context.Context, string) ([]Resource, error)
	Items(context.Context, string, Resource, string) (Page, error)
	Item(context.Context, string, Resource, string) (Item, error)
}
type Service struct {
	store    Store
	provider Provider
}

func New(store Store, provider Provider) *Service { return &Service{store, provider} }
func (s *Service) Kind() string                   { return s.provider.Kind() }
func (s *Service) Name() string {
	if s.Kind() == "jira" {
		return "Jira"
	}
	return "Trello"
}
func (s *Service) Authorize(ctx context.Context, id Identity) error {
	team, err := s.store.SlackTeam(ctx, id.UserID)
	if err != nil || id.UserID <= 0 || id.Team == "" || team != id.Team {
		return ErrDenied
	}
	return nil
}
func randomState() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func stateHash(state string) string {
	h := sha256.Sum256([]byte(state))
	return hex.EncodeToString(h[:])
}
func (s *Service) Begin(ctx context.Context, user int64) (string, string, error) {
	team, err := s.store.SlackTeam(ctx, user)
	if err != nil || team == "" || user <= 0 {
		return "", "", ErrDenied
	}
	state, verifier := randomState(), randomState()
	err = s.store.PutState(ctx, s.Kind(), stateHash(state), State{UserID: user, Phase: "oauth", Verifier: verifier, Expires: time.Now().Add(10 * time.Minute)})
	if err != nil {
		return "", "", err
	}
	return state, s.provider.AuthorizeURL(state, verifier), nil
}
func (s *Service) consume(ctx context.Context, state, phase string) (State, error) {
	if len(state) != 64 {
		return State{}, ErrConnect
	}
	saved, err := s.store.ConsumeState(ctx, s.Kind(), stateHash(state))
	if err != nil || saved.Phase != phase || !time.Now().Before(saved.Expires) {
		return State{}, ErrConnect
	}
	return saved, nil
}
func (s *Service) Selection(ctx context.Context, state, code string) (string, Access, error) {
	if code == "" || len(code) > 4096 {
		return "", Access{}, ErrArguments
	}
	saved, err := s.consume(ctx, state, "oauth")
	if err != nil {
		return "", Access{}, err
	}
	creds, err := s.provider.Exchange(ctx, code, saved.Verifier)
	if err != nil {
		return "", Access{}, err
	}
	account, err := s.provider.Account(ctx, creds.Access)
	if err != nil {
		return "", Access{}, err
	}
	resources, err := s.provider.Resources(ctx, creds.Access)
	if err != nil {
		return "", Access{}, err
	}
	team, err := s.store.SlackTeam(ctx, saved.UserID)
	if err != nil || team == "" {
		return "", Access{}, ErrDenied
	}
	next := randomState()
	c := Connection{Identity: Identity{saved.UserID, team}, Provider: s.Kind(), Account: account, Credentials: creds}
	err = s.store.PutState(ctx, s.Kind(), stateHash(next), State{UserID: saved.UserID, Phase: "select", Pending: &c, Expires: time.Now().Add(10 * time.Minute)})
	return next, Access{account, resources, canWrite(s.Kind(), creds)}, err
}
func (s *Service) Complete(ctx context.Context, state string, ids []string) (Account, error) {
	if len(ids) == 0 || len(ids) > 2000 {
		return Account{}, ErrArguments
	}
	saved, err := s.consume(ctx, state, "select")
	if err != nil || saved.Pending == nil {
		return Account{}, ErrConnect
	}
	c := *saved.Pending
	if c.UserID != saved.UserID || c.Provider != s.Kind() || s.Authorize(ctx, c.Identity) != nil {
		return Account{}, ErrDenied
	}
	account, err := s.provider.Account(ctx, c.Credentials.Access)
	if err != nil || account.ID != c.Account.ID {
		return Account{}, ErrConnect
	}
	resources, err := s.provider.Resources(ctx, c.Credentials.Access)
	if err != nil {
		return Account{}, err
	}
	allowed := map[string]bool{}
	for _, r := range resources {
		allowed[r.ID] = true
	}
	for _, id := range ids {
		if !allowed[id] {
			return Account{}, ErrDenied
		}
		delete(allowed, id)
	}
	c.Account = account
	c.Selected = append([]string(nil), ids...)
	return account, s.store.Save(ctx, c)
}
func (s *Service) connection(ctx context.Context, id Identity) (Connection, error) {
	if err := s.Authorize(ctx, id); err != nil {
		return Connection{}, err
	}
	var result Connection
	err := s.store.WithConnection(ctx, s.Kind(), id.UserID, func(c *Connection) error {
		if c.Identity != id || c.Provider != s.Kind() {
			return ErrDenied
		}
		if !time.Now().Add(time.Minute).Before(c.Credentials.Expires) {
			creds, err := s.provider.Refresh(ctx, c.Credentials.Refresh)
			if err != nil {
				return err
			}
			// OAuth may omit scope when refreshing an unchanged grant.
			if creds.Scopes == "" {
				creds.Scopes = c.Credentials.Scopes
			}
			c.Credentials = creds
		}
		result = *c
		return nil
	})
	if err != nil {
		return Connection{}, err
	}
	// Persist rotating refresh tokens before later API calls can fail.
	account, err := s.provider.Account(ctx, result.Credentials.Access)
	if err != nil {
		return Connection{}, err
	}
	if account.ID != result.Account.ID {
		return Connection{}, ErrConnect
	}
	result.Account = account
	return result, nil
}
func (s *Service) access(ctx context.Context, id Identity) (Connection, []Resource, error) {
	c, err := s.connection(ctx, id)
	if err != nil {
		return c, nil, err
	}
	resources, err := s.provider.Resources(ctx, c.Credentials.Access)
	if err != nil {
		return c, nil, err
	}
	selected := map[string]bool{}
	for _, id := range c.Selected {
		selected[id] = true
	}
	allowed := []Resource{}
	for _, r := range resources {
		if selected[r.ID] {
			allowed = append(allowed, r)
		}
	}
	return c, allowed, nil
}
func (s *Service) Access(ctx context.Context, id Identity) (Access, error) {
	c, resources, err := s.access(ctx, id)
	return Access{c.Account, resources, canWrite(s.Kind(), c.Credentials)}, err
}
func (s *Service) resource(ctx context.Context, id Identity, resource string) (Connection, Resource, error) {
	c, resources, err := s.access(ctx, id)
	if err != nil {
		return c, Resource{}, err
	}
	for _, r := range resources {
		if r.ID == resource {
			return c, r, nil
		}
	}
	return c, Resource{}, ErrDenied
}
func (s *Service) Items(ctx context.Context, id Identity, resource, cursor string) (Page, error) {
	if len(cursor) > 4096 {
		return Page{}, ErrArguments
	}
	c, r, err := s.resource(ctx, id, resource)
	if err != nil {
		return Page{}, err
	}
	return s.provider.Items(ctx, c.Credentials.Access, r, cursor)
}
func (s *Service) Item(ctx context.Context, id Identity, resource, item string) (Item, error) {
	c, r, err := s.resource(ctx, id, resource)
	if err != nil {
		return Item{}, err
	}
	return s.provider.Item(ctx, c.Credentials.Access, r, item)
}
func (s *Service) Connected(ctx context.Context, user int64) (bool, error) {
	err := s.store.WithConnection(ctx, s.Kind(), user, func(*Connection) error { return nil })
	if errors.Is(err, ErrConnect) {
		return false, nil
	}
	return err == nil, err
}
func (s *Service) Disconnect(ctx context.Context, user int64) error {
	return s.store.Disconnect(ctx, s.Kind(), user)
}
