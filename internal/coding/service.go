package coding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type Service struct {
	store    Store
	provider Provider
}

// GitHub is authoritative for repository permissions. Slack identity is resolved
// from persisted installation/user records instead of operator-maintained lists.
func New(store Store, provider Provider) (*Service, error) {
	if store == nil || provider == nil {
		return nil, ErrArguments
	}
	return &Service{store: store, provider: provider}, nil
}

// AuthorizeIdentity also works before GitHub is connected, allowing a private
// connection invitation only for the authenticated Slack workspace member.
func (s *Service) AuthorizeIdentity(ctx context.Context, i Identity) error {
	if i.UserID <= 0 || strings.TrimSpace(i.Team) == "" {
		return ErrDenied
	}
	team, err := s.store.SlackTeam(ctx, i.UserID)
	if err != nil {
		return ErrUnavailable
	}
	if team != i.Team {
		return ErrDenied
	}
	return nil
}

// Connected is a local onboarding check; it never exposes or refreshes tokens.
func (s *Service) Connected(ctx context.Context, userID int64) (bool, error) {
	err := s.store.WithConnection(ctx, userID, func(*Connection) error { return nil })
	if errors.Is(err, ErrConnect) {
		return false, nil
	}
	return err == nil, err
}

func stateHash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func (s *Service) BeginConnect(ctx context.Context, userID int64) (string, string, error) {
	if userID <= 0 {
		return "", "", ErrArguments
	}
	team, err := s.store.SlackTeam(ctx, userID)
	if err != nil || team == "" {
		return "", "", ErrDenied
	}
	state, err := RandomID()
	if err != nil {
		return "", "", err
	}
	if err := s.store.PutOAuthState(ctx, stateHash(state), OAuthState{UserID: userID, Phase: "install", Expires: time.Now().Add(10 * time.Minute)}); err != nil {
		return "", "", ErrUnavailable
	}
	return state, s.provider.InstallationURL(state), nil
}

// ContinueConnect starts PKCE authorization only after the repository selection
// redirect. The installation ID remains untrusted until checked with GitHub.
func (s *Service) ContinueConnect(ctx context.Context, installState string, installationID int64) (string, string, error) {
	if !taskID.MatchString(installState) || installationID < 0 {
		return "", "", ErrArguments
	}
	saved, err := s.store.ConsumeOAuthState(ctx, stateHash(installState))
	if err != nil || saved.Phase != "install" || !time.Now().Before(saved.Expires) {
		return "", "", ErrConnect
	}
	state, err := RandomID()
	if err != nil {
		return "", "", err
	}
	// 64 characters meets PKCE's 43-character minimum.
	a, err := RandomID()
	if err != nil {
		return "", "", err
	}
	b, err := RandomID()
	if err != nil {
		return "", "", err
	}
	verifier := a + b
	if err := s.store.PutOAuthState(ctx, stateHash(state), OAuthState{UserID: saved.UserID, Verifier: verifier, Expires: time.Now().Add(10 * time.Minute), Phase: "oauth", InstallationID: installationID}); err != nil {
		return "", "", ErrUnavailable
	}
	return state, s.provider.AuthorizeURL(state, verifier), nil
}

func (s *Service) CompleteConnect(ctx context.Context, state, code string) error {
	_, err := s.CompleteConnectAccount(ctx, state, code)
	return err
}

func (s *Service) CompleteConnectAccount(ctx context.Context, state, code string) (Account, error) {
	if !taskID.MatchString(state) || code == "" || len(code) > 1024 {
		return Account{}, ErrArguments
	}
	saved, err := s.store.ConsumeOAuthState(ctx, stateHash(state))
	if err != nil || saved.Phase != "oauth" || saved.InstallationID < 0 || !time.Now().Before(saved.Expires) {
		return Account{}, ErrConnect
	}
	credentials, err := s.provider.Exchange(ctx, code, saved.Verifier)
	if err != nil {
		return Account{}, ErrConnect
	}
	id, login, err := s.provider.User(ctx, credentials.Access)
	if err != nil || id <= 0 || login == "" {
		return Account{}, ErrConnect
	}
	if err := s.provider.VerifyInstallation(ctx, credentials.Access, saved.InstallationID, Account{ID: id, Login: login}); err != nil {
		return Account{}, err
	}
	team, err := s.store.SlackTeam(ctx, saved.UserID)
	if err != nil || team == "" {
		return Account{}, ErrConnect
	}
	if err = s.store.SaveConnection(ctx, Connection{UserID: saved.UserID, Team: team, GitHubUserID: id, Login: login, Credentials: credentials}); err != nil {
		return Account{}, ErrUnavailable
	}
	log.Printf("coding_event=github_connected user_id=%d github_user_id=%d", saved.UserID, id)
	return Account{ID: id, Login: login}, nil
}

func (s *Service) Disconnect(ctx context.Context, userID int64) error {
	return s.store.Disconnect(ctx, userID)
}

// ConnectionBindings is only exposed through the authenticated admin router.
func (s *Service) ConnectionBindings(ctx context.Context) ([]ConnectionBinding, error) {
	return s.store.ConnectionBindings(ctx)
}

// verifiedConnection checks both the Slack binding and the live token subject.
// A corrupted/swapped token must fail before any repository data is requested.
func (s *Service) verifiedConnection(ctx context.Context, identity Identity) (Account, string, error) {
	if err := s.AuthorizeIdentity(ctx, identity); err != nil {
		return Account{}, "", err
	}
	var token string
	var account Account
	err := s.store.WithConnection(ctx, identity.UserID, func(c *Connection) error {
		if c.UserID != identity.UserID || c.Team != identity.Team {
			return ErrDenied
		}
		if c.GitHubUserID <= 0 || c.Login == "" || !strings.HasPrefix(c.Credentials.Access, "ghu_") {
			return ErrConnect
		}
		if !c.Credentials.Expires.IsZero() && time.Now().Add(time.Minute).After(c.Credentials.Expires) {
			if c.Credentials.Refresh == "" || !time.Now().Before(c.Credentials.RefreshExpires) {
				return ErrConnect
			}
			next, err := s.provider.Refresh(ctx, c.Credentials.Refresh)
			if err != nil {
				return ErrConnect
			}
			c.Credentials = next
		}
		if !strings.HasPrefix(c.Credentials.Access, "ghu_") {
			return ErrConnect
		}
		id, login, err := s.provider.User(ctx, c.Credentials.Access)
		if err != nil {
			return err
		}
		if id != c.GitHubUserID || login == "" {
			log.Printf("coding_event=github_identity_mismatch user_id=%d expected_github_user_id=%d actual_github_user_id=%d", identity.UserID, c.GitHubUserID, id)
			return ErrConnect
		}
		account = Account{ID: id, Login: login}
		token = c.Credentials.Access
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrConnect) || errors.Is(err, ErrDenied) {
			return Account{}, "", err
		}
		return Account{}, "", ErrUnavailable
	}
	return account, token, nil
}

func (s *Service) Account(ctx context.Context, identity Identity) (Account, error) {
	account, _, err := s.verifiedConnection(ctx, identity)
	return account, err
}

func (s *Service) Repositories(ctx context.Context, identity Identity) ([]Repository, error) {
	access, err := s.RepositoryAccess(ctx, identity)
	return access.Repositories, err
}

func (s *Service) RepositoryAccess(ctx context.Context, identity Identity) (RepositoryAccess, error) {
	account, token, err := s.verifiedConnection(ctx, identity)
	if err != nil {
		return RepositoryAccess{}, err
	}
	repos, err := s.provider.Repositories(ctx, token)
	if err != nil {
		return RepositoryAccess{}, err
	}
	result := make([]Repository, 0)
	for _, repo := range repos {
		if repo.ID > 0 && repo.InstallationID > 0 && ValidRepository(repo.FullName) && repo.DefaultBranch != "" {
			result = append(result, repo)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FullName < result[j].FullName })
	log.Printf("coding_event=repositories_listed user_id=%d github_user_id=%d count=%d", identity.UserID, account.ID, len(result))
	return RepositoryAccess{Account: account, Repositories: result}, nil
}

// Prepare stores intent only. This phase has no executor, push, or PR API.
// A future runner must require explicit approval and recheck current permissions.
func (s *Service) Prepare(ctx context.Context, identity Identity, name string) (Task, error) {
	if !ValidRepository(name) || identity.Event == "" || identity.Channel == "" || identity.Thread == "" || strings.TrimSpace(identity.Request) == "" || utf8.RuneCountInString(identity.Request) > 12000 {
		return Task{}, ErrArguments
	}
	repos, err := s.Repositories(ctx, identity)
	if err != nil {
		return Task{}, err
	}
	for _, repo := range repos {
		if !strings.EqualFold(repo.FullName, name) {
			continue
		}
		id, err := RandomID()
		if err != nil {
			return Task{}, ErrUnavailable
		}
		task, err := s.store.CreateDraft(ctx, Task{ID: id, Identity: identity, Provider: "github", Repository: repo, Instruction: identity.Request, Status: "draft", CreatedAt: time.Now().UTC()})
		if err != nil {
			return Task{}, ErrUnavailable
		}
		if task.Repository.ID != repo.ID {
			return Task{}, errors.New("bu Slack mesajı için zaten başka bir repo görev taslağı var")
		}
		return task, nil
	}
	return Task{}, ErrDenied
}

func (s *Service) Task(ctx context.Context, identity Identity, id string, cancel bool) (Task, error) {
	if s.AuthorizeIdentity(ctx, identity) != nil || !taskID.MatchString(id) {
		return Task{}, ErrNotFound
	}
	var task Task
	var err error
	if cancel {
		task, err = s.store.CancelDraft(ctx, identity, id)
	} else {
		task, err = s.store.Task(ctx, identity, id)
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Task{}, ErrUnavailable
	}
	return task, err
}
