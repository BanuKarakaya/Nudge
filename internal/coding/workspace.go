package coding

import (
	"context"
	"nudge/internal/workspace"
)

func (s *Service) WithWorkspacePolicy(p workspace.Policy) { s.workspace = p }
func (s *Service) CanConnect(ctx context.Context, user int64) error {
	if s.workspace == nil {
		return nil
	}
	scope, err := s.workspace.Scope(ctx, user, s.Kind())
	if err != nil {
		return ErrUnavailable
	}
	if !scope.Manager && !scope.Enabled {
		return ErrDenied
	}
	return nil
}

// BeginVerification skips installation; only the manager installs the GitHub App.
func (s *Service) BeginVerification(ctx context.Context, user int64) (string, string, error) {
	state, _, err := s.BeginConnect(ctx, user)
	if err != nil {
		return "", "", err
	}
	return s.ContinueConnect(ctx, state, 0)
}

// SetupAccess is exclusively for the authenticated manager's scope editor.
// It enumerates only the manager's own live provider permissions.
func (s *Service) SetupAccess(ctx context.Context, i Identity) (RepositoryAccess, error) {
	if s.workspace == nil {
		return RepositoryAccess{}, ErrDenied
	}
	scope, err := s.workspace.Scope(ctx, i.UserID, s.Kind())
	if err != nil || !scope.Manager {
		return RepositoryAccess{}, ErrDenied
	}
	a, token, err := s.verifiedConnection(ctx, i)
	if err != nil {
		return RepositoryAccess{}, err
	}
	repos, err := s.provider.Repositories(ctx, token)
	if err != nil {
		return RepositoryAccess{}, err
	}
	result := []Repository{}
	for _, r := range repos {
		if r.Valid() && r.Kind() == s.Kind() && r.DefaultBranch != "" {
			result = append(result, r)
		}
	}
	return RepositoryAccess{Account: a, Repositories: result}, nil
}

// StoredAccountName is display-only; operations still verify the live subject.
func (s *Service) StoredAccountName(ctx context.Context, user int64) (string, error) {
	var name string
	err := s.store.WithConnection(ctx, user, func(c *Connection) error {
		if c.UserID != user {
			return ErrDenied
		}
		name = c.Login
		return nil
	})
	return name, err
}
