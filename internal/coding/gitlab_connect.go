package coding

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

func NewGitLab(store Store, provider GitLab) (*Service, error) {
	s, err := New(store, provider)
	if err == nil {
		s.kind = "gitlab"
	}
	return s, err
}
func (s *Service) validToken(token string) bool {
	if s.Kind() == "gitlab" {
		return validGitLabToken(token)
	}
	return strings.HasPrefix(token, "ghu_")
}
func (s *Service) filterSelected(ctx context.Context, user int64, repos []Repository) ([]Repository, error) {
	if s.workspace != nil {
		scope, err := s.workspace.Scope(ctx, user, s.Kind())
		if err != nil {
			return nil, ErrUnavailable
		}
		if !scope.Enabled {
			return nil, ErrDenied
		}
		result := []Repository{}
		for _, r := range repos {
			if scope.Allows(strconv.FormatInt(r.ID, 10)) {
				result = append(result, r)
			}
		}
		return result, nil
	}
	if s.Kind() != "gitlab" {
		return repos, nil
	}
	var ids []int64
	err := s.store.WithConnection(ctx, user, func(c *Connection) error { return json.Unmarshal([]byte(c.Credentials.SelectedProjects), &ids) })
	if err != nil {
		return nil, ErrConnect
	}
	allowed := map[int64]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	result := []Repository{}
	for _, r := range repos {
		if r.Kind() == "gitlab" && allowed[r.ID] {
			result = append(result, r)
		}
	}
	return result, nil
}
func (s *Service) BeginGitLab(ctx context.Context, user int64) (string, string, error) {
	if err := s.CanConnect(ctx, user); err != nil {
		return "", "", err
	}
	if s.Kind() != "gitlab" {
		return "", "", ErrArguments
	}
	team, err := s.store.SlackTeam(ctx, user)
	if err != nil || team == "" {
		return "", "", ErrDenied
	}
	state, err := RandomID()
	if err != nil {
		return "", "", err
	}
	a, err := RandomID()
	if err != nil {
		return "", "", err
	}
	b, err := RandomID()
	if err != nil {
		return "", "", err
	}
	saved := OAuthState{UserID: user, Verifier: a + b, Phase: "gitlab-oauth", Expires: time.Now().Add(10 * time.Minute)}
	if err := s.store.PutOAuthState(ctx, stateHash(state), saved); err != nil {
		return "", "", err
	}
	return state, s.provider.AuthorizeURL(state, saved.Verifier), nil
}

// OAuth alone never activates repo access. The user must submit a cookie-bound
// selection form; pending credentials are encrypted in the single-use state.
func (s *Service) GitLabSelection(ctx context.Context, state, code string) (string, Account, []Repository, error) {
	if s.Kind() != "gitlab" || !taskID.MatchString(state) || code == "" || len(code) > 1024 {
		return "", Account{}, nil, ErrArguments
	}
	saved, err := s.store.ConsumeOAuthState(ctx, stateHash(state))
	if err != nil || saved.Phase != "gitlab-oauth" || !time.Now().Before(saved.Expires) {
		return "", Account{}, nil, ErrConnect
	}
	creds, err := s.provider.Exchange(ctx, code, saved.Verifier)
	if err != nil {
		return "", Account{}, nil, err
	}
	id, login, err := s.provider.User(ctx, creds.Access)
	if err != nil || id <= 0 || login == "" {
		return "", Account{}, nil, ErrConnect
	}
	repos, err := s.provider.Repositories(ctx, creds.Access)
	if err != nil {
		return "", Account{}, nil, err
	}
	team, err := s.store.SlackTeam(ctx, saved.UserID)
	if err != nil || team == "" {
		return "", Account{}, nil, ErrDenied
	}
	pending := Connection{UserID: saved.UserID, Team: team, GitHubUserID: id, Login: login, Credentials: creds}
	if s.workspace != nil {
		if err := s.CanConnect(ctx, saved.UserID); err != nil {
			return "", Account{}, nil, err
		}
		err = s.store.SaveConnection(ctx, pending)
		return "", Account{ID: id, Login: login}, nil, err
	}
	next, err := RandomID()
	if err != nil {
		return "", Account{}, nil, err
	}
	err = s.store.PutOAuthState(ctx, stateHash(next), OAuthState{UserID: saved.UserID, Phase: "gitlab-select", Expires: time.Now().Add(10 * time.Minute), Pending: &pending})
	return next, Account{ID: id, Login: login}, repos, err
}
func (s *Service) CompleteGitLabSelection(ctx context.Context, state string, ids []int64) (Account, error) {
	if s.Kind() != "gitlab" || !taskID.MatchString(state) || len(ids) == 0 || len(ids) > 2000 {
		return Account{}, ErrArguments
	}
	saved, err := s.store.ConsumeOAuthState(ctx, stateHash(state))
	if err != nil || saved.Phase != "gitlab-select" || saved.Pending == nil || !time.Now().Before(saved.Expires) {
		return Account{}, ErrConnect
	}
	if err := s.CanConnect(ctx, saved.UserID); err != nil {
		return Account{}, err
	}
	c := *saved.Pending
	if c.UserID != saved.UserID || s.AuthorizeIdentity(ctx, Identity{UserID: c.UserID, Team: c.Team}) != nil {
		return Account{}, ErrDenied
	}
	id, login, err := s.provider.User(ctx, c.Credentials.Access)
	if err != nil || id != c.GitHubUserID {
		return Account{}, ErrConnect
	}
	repos, err := s.provider.Repositories(ctx, c.Credentials.Access)
	if err != nil {
		return Account{}, err
	}
	allowed := map[int64]bool{}
	for _, r := range repos {
		if r.Valid() && r.Kind() == "gitlab" {
			allowed[r.ID] = true
		}
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if !allowed[id] || seen[id] {
			return Account{}, ErrDenied
		}
		seen[id] = true
	}
	raw, _ := json.Marshal(ids)
	c.Credentials.SelectedProjects = string(raw)
	c.Login = login
	if err := s.store.SaveConnection(ctx, c); err != nil {
		return Account{}, err
	}
	return Account{ID: id, Login: login}, nil
}
