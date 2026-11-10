package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrWriteScope     = errors.New("tracker write authorization required")
	ErrRejected       = errors.New("tracker rejected change; check fields and workflow")
	ErrChanged        = errors.New("tracker item status changed")
	ErrOutcomeUnknown = errors.New("tracker change outcome unknown; do not repeat")
)

func canWrite(kind string, creds Credentials) bool {
	scope := "write:jira-work"
	if kind == "trello" {
		scope = "write:board:trello"
	}
	return strings.Contains(" "+creds.Scopes+" ", " "+scope+" ")
}

type Field struct {
	ID         string         `json:"fieldId"`
	Name       string         `json:"name"`
	Required   bool           `json:"required"`
	HasDefault bool           `json:"hasDefaultValue"`
	Schema     map[string]any `json:"schema,omitempty"`
	Allowed    []any          `json:"allowedValues,omitempty"`
	Operations []string       `json:"operations,omitempty"`
}
type Option struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	StatusID string           `json:"status_id,omitempty"`
	Status   string           `json:"status,omitempty"`
	Fields   map[string]Field `json:"fields,omitempty"`
}
type CreateOptions struct {
	Options []Option `json:"options"`
	Fields  []Field  `json:"fields,omitempty"`
}
type CreateInput struct {
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Destination string         `json:"destination_id"` // Trello list or Jira issue type.
	Fields      map[string]any `json:"fields,omitempty"`
}
type MoveInput struct {
	Item           string         `json:"item_id"`
	Destination    string         `json:"destination_id"`
	ExpectedStatus string         `json:"expected_status_id"`
	Fields         map[string]any `json:"fields,omitempty"`
}
type MutationResult struct {
	Status string `json:"status"`
	Item   Item   `json:"item"`
}

// Optional interface keeps read-only providers usable. Implementations validate
// the resource, destination and current item before making any write request.
type WriteProvider interface {
	CreateOptions(context.Context, string, Resource, string) (CreateOptions, error)
	MoveOptions(context.Context, string, Resource, string) ([]Option, error)
	Create(context.Context, string, Resource, CreateInput) (MutationResult, error)
	Move(context.Context, string, Resource, MoveInput) (MutationResult, error)
}
type MutationReceipt struct {
	Started time.Time
	Result  *MutationResult
}

func (s *Service) CreateOptions(ctx context.Context, id Identity, resource, issueType string) (CreateOptions, error) {
	c, r, err := s.resource(ctx, id, resource)
	if err != nil {
		return CreateOptions{}, err
	}
	p, ok := s.provider.(WriteProvider)
	if !ok {
		return CreateOptions{}, ErrUnavailable
	}
	return p.CreateOptions(ctx, c.Credentials.Access, r, issueType)
}
func (s *Service) MoveOptions(ctx context.Context, id Identity, resource, item string) ([]Option, error) {
	c, r, err := s.resource(ctx, id, resource)
	if err != nil {
		return nil, err
	}
	p, ok := s.provider.(WriteProvider)
	if !ok {
		return nil, ErrUnavailable
	}
	return p.MoveOptions(ctx, c.Credentials.Access, r, item)
}
func (s *Service) Create(ctx context.Context, id Identity, request, resource string, input CreateInput) (MutationResult, error) {
	input.Title = strings.TrimSpace(input.Title)
	return s.mutate(ctx, id, request, resource, "create", input, func(p WriteProvider, token string, r Resource) (MutationResult, error) {
		return p.Create(ctx, token, r, input)
	})
}
func (s *Service) Move(ctx context.Context, id Identity, request, resource string, input MoveInput) (MutationResult, error) {
	return s.mutate(ctx, id, request, resource, "move", input, func(p WriteProvider, token string, r Resource) (MutationResult, error) {
		return p.Move(ctx, token, r, input)
	})
}

// A persisted reservation precedes the provider request. A timeout, crash or
// lost response leaves it pending, so a replay cannot silently duplicate a card.
// Receipts live with the encrypted connection and expire after seven days.
func (s *Service) mutate(ctx context.Context, id Identity, request, resource, operation string, input any, run func(WriteProvider, string, Resource) (MutationResult, error)) (MutationResult, error) {
	if request == "" || len(request) > 512 {
		return MutationResult{}, ErrArguments
	}
	c, r, err := s.resource(ctx, id, resource)
	if err != nil {
		return MutationResult{}, err
	}
	if !canWrite(s.Kind(), c.Credentials) {
		return MutationResult{}, ErrWriteScope
	}
	p, ok := s.provider.(WriteProvider)
	if !ok {
		return MutationResult{}, ErrUnavailable
	}
	raw, err := json.Marshal([]any{request, resource, operation, input})
	if err != nil || len(raw) > 65536 {
		return MutationResult{}, ErrArguments
	}
	key := stateHash(string(raw))
	var cached *MutationResult
	err = s.store.WithConnection(ctx, s.Kind(), id.UserID, func(current *Connection) error {
		if current.Identity != id || current.Account.ID != c.Account.ID || current.Provider != s.Kind() || !canWrite(s.Kind(), current.Credentials) {
			return ErrDenied
		}
		selected := false
		for _, v := range current.Selected {
			if v == resource {
				selected = true
			}
		}
		if !selected {
			return ErrDenied
		}
		receipts := make(map[string]MutationReceipt)
		for k, v := range current.Mutations {
			if time.Since(v.Started) < 7*24*time.Hour {
				receipts[k] = v
			}
		}
		if receipt, exists := receipts[key]; exists {
			if receipt.Result == nil {
				return ErrOutcomeUnknown
			}
			cached = receipt.Result
			return nil
		}
		if len(receipts) >= 1000 {
			return ErrUnavailable
		}
		receipts[key] = MutationReceipt{Started: time.Now()}
		current.Mutations = receipts
		return nil
	})
	if err != nil {
		return MutationResult{}, err
	}
	if cached != nil {
		return *cached, nil
	}
	result, runErr := run(p, c.Credentials.Access, r)
	// Preserve the reservation if delivery is uncertain. Preflight and definitive
	// rejection errors are safe to release; no retry is performed here.
	if errors.Is(runErr, ErrOutcomeUnknown) {
		return MutationResult{}, runErr
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = s.store.WithConnection(saveCtx, s.Kind(), id.UserID, func(current *Connection) error {
		if current.Identity != id || current.Account.ID != c.Account.ID {
			return ErrDenied
		}
		receipt, exists := current.Mutations[key]
		if !exists {
			return ErrOutcomeUnknown
		}
		if runErr != nil {
			delete(current.Mutations, key)
		} else {
			receipt.Result = &result
			current.Mutations[key] = receipt
		}
		return nil
	})
	if runErr != nil {
		return MutationResult{}, runErr
	}
	// A confirmed provider success stays a success even if recording it fails;
	// the persisted pending reservation still blocks unsafe replay.
	return result, nil
}
