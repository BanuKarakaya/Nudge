package tracker

import (
	"context"
	"errors"
	"strings"
)

var ErrUserScope = errors.New("Jira user lookup authorization required")

type Member struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username,omitempty"`
}
type Label struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}
type LookupInput struct{ Kind, Item, Query, Cursor string }
type ChangeInput struct {
	Operation string         `json:"operation"`
	Item      string         `json:"item_id"`
	Target    string         `json:"target_id,omitempty"`
	Text      string         `json:"text,omitempty"`
	Mode      string         `json:"mode,omitempty"`
	IDs       []string       `json:"ids,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
}
type ManagementProvider interface {
	Lookup(context.Context, string, Resource, LookupInput) (any, error)
	Change(context.Context, string, Resource, ChangeInput) (MutationResult, error)
}

func (s *Service) Lookup(ctx context.Context, id Identity, resource string, input LookupInput) (any, error) {
	c, r, err := s.resource(ctx, id, resource)
	if err != nil {
		return nil, err
	}
	if s.Kind() == "jira" && input.Kind == "members" && !strings.Contains(" "+c.Credentials.Scopes+" ", " read:jira-user ") {
		return nil, ErrUserScope
	}
	p, ok := s.provider.(ManagementProvider)
	if !ok {
		return nil, ErrUnavailable
	}
	return p.Lookup(ctx, c.Credentials.Access, r, input)
}
func (s *Service) Change(ctx context.Context, id Identity, request, resource string, input ChangeInput) (MutationResult, error) {
	if s.Kind() == "jira" && input.Operation == "assign" && len(input.IDs) > 0 {
		c, err := s.connection(ctx, id)
		if err != nil {
			return MutationResult{}, err
		}
		if !strings.Contains(" "+c.Credentials.Scopes+" ", " read:jira-user ") {
			return MutationResult{}, ErrUserScope
		}
	}
	return s.mutate(ctx, id, request, resource, "manage", input, func(p WriteProvider, token string, r Resource) (MutationResult, error) {
		manager, ok := p.(ManagementProvider)
		if !ok {
			return MutationResult{}, ErrUnavailable
		}
		return manager.Change(ctx, token, r, input)
	})
}
