package tracker

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
func (s *Service) SetupAccess(ctx context.Context, id Identity) (Access, error) {
	if s.workspace == nil {
		return Access{}, ErrDenied
	}
	scope, err := s.workspace.Scope(ctx, id.UserID, s.Kind())
	if err != nil || !scope.Manager {
		return Access{}, ErrDenied
	}
	c, err := s.connection(ctx, id)
	if err != nil {
		return Access{}, err
	}
	resources, err := s.provider.Resources(ctx, c.Credentials.Access)
	return Access{c.Account, resources, canWrite(s.Kind(), c.Credentials)}, err
}

// StoredAccountName never refreshes or exposes credentials to the panel.
func (s *Service) StoredAccountName(ctx context.Context, user int64) (string, error) {
	var name string
	err := s.store.WithConnection(ctx, s.Kind(), user, func(c *Connection) error {
		if c.UserID != user || c.Provider != s.Kind() {
			return ErrDenied
		}
		name = c.Account.Name
		return nil
	})
	return name, err
}
