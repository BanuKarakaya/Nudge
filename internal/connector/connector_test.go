package connector

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nudge/internal/coding"
	"nudge/internal/service"
	"nudge/internal/workspace"
)

type memoryMembers struct {
	workspace.Store
	members map[int64]workspace.Member
}

func (s *memoryMembers) Member(_ context.Context, user int64) (workspace.Member, error) {
	m, ok := s.members[user]
	if !ok {
		return m, workspace.ErrDenied
	}
	return m, nil
}

type memoryConnections struct {
	c     Connection
	token string
}

func (s *memoryConnections) List(_ context.Context, user int64) ([]Connection, error) {
	if user == 3 {
		return nil, nil
	}
	if s.c.ID == "" {
		return nil, nil
	}
	raw, _ := json.Marshal(s.c)
	var c Connection
	_ = json.Unmarshal(raw, &c)
	return []Connection{c}, nil
}
func (s *memoryConnections) Secret(_ context.Context, user int64, id, rev string) (string, error) {
	if user == 3 || s.c.ID != id || s.c.Revision != rev {
		return "", workspace.ErrDenied
	}
	return s.token, nil
}
func (s *memoryConnections) Save(_ context.Context, user int64, c Connection, token, expected string) error {
	if user != 1 {
		return workspace.ErrDenied
	}
	if expected != s.c.Revision {
		return ErrConflict
	}
	s.c = c
	s.token = token
	return nil
}
func (s *memoryConnections) Delete(_ context.Context, user int64, id, rev string) error {
	if user != 1 || s.c.ID != id || s.c.Revision != rev {
		return workspace.ErrDenied
	}
	s.c = Connection{}
	s.token = ""
	return nil
}

type memoryRemote struct {
	calls, discoveries int
	token              string
}

func (r *memoryRemote) Discover(_ context.Context, _, token string) ([]Tool, error) {
	r.discoveries++
	r.token = token
	return []Tool{{Name: "read", Schema: map[string]any{"type": "object"}, Fingerprint: "read-v1"}, {Name: "delete", Schema: map[string]any{"type": "object"}, Fingerprint: "delete-v1"}}, nil
}
func (r *memoryRemote) Call(_ context.Context, _, token string, _ Tool, _ json.RawMessage) (string, error) {
	r.calls++
	r.token = token
	return "done", nil
}

type pendingActions struct {
	proposals int
	mode      string
}

func (p *pendingActions) ActionMode(context.Context, int64, string) (string, error) {
	if p.mode != "" {
		return p.mode, nil
	}
	return "approval", nil
}
func (p *pendingActions) ProposeAction(_ context.Context, a service.ActionProposal) (service.ActionProposal, error) {
	p.proposals++
	return a, nil
}

func TestConnectorLifecyclePermissionsApprovalsAndRevocation(t *testing.T) {
	ctx := context.Background()
	members := &memoryMembers{members: map[int64]workspace.Member{1: {UserID: 1, Team: "A", Manager: true}, 2: {UserID: 2, Team: "A"}, 3: {UserID: 3, Team: "B", Manager: true}}}
	store := &memoryConnections{}
	remote := &memoryRemote{}
	s := &Service{Store: store, Members: members, Remote: remote}
	if err := s.Discover(ctx, 2, "", "", "Internal", "https://internal.test/mcp", "test-secret"); err == nil || remote.discoveries != 0 {
		t.Fatal("member discovered")
	}
	if err := s.Discover(ctx, 1, "", "", "Internal", "https://internal.test/mcp", "test-secret"); err != nil {
		t.Fatal(err)
	}
	identity := coding.Identity{UserID: 2, Team: "A"}
	if tools, err := s.Tools(ctx, identity); err != nil || len(tools) != 0 {
		t.Fatal("discovery enabled tools")
	}
	id, rev := store.c.ID, store.c.Revision
	if err := s.Select(ctx, 1, id, rev, true, []string{"forged"}); err == nil {
		t.Fatal("unknown tool enabled")
	}
	if err := s.Select(ctx, 1, id, rev, true, []string{"read"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Select(ctx, 1, id, rev, true, []string{"delete"}); err == nil {
		t.Fatal("stale form accepted")
	}
	tools, err := s.Tools(ctx, identity)
	if err != nil || len(tools) != 1 || tools[0].ReadOnly {
		t.Fatal("tool policy", tools, err)
	}
	if other, err := s.Tools(ctx, coding.Identity{UserID: 3, Team: "B"}); err != nil || len(other) != 0 {
		t.Fatal("cross-team tools")
	}
	if _, err := s.Tools(ctx, coding.Identity{UserID: 2, Team: "B"}); err == nil {
		t.Fatal("forged identity")
	}
	registry := service.NewToolRegistry(service.NewAgentTools(nil, nil, nil, 2, time.Now(), time.UTC))
	if err := registry.Register(tools[0]); err != nil {
		t.Fatal(err)
	}
	actions := &pendingActions{}
	notified := false
	wrapped := service.WithActionPolicy(registry, actions, identity, func(context.Context, service.ActionProposal) error { notified = true; return nil })
	result, err := wrapped.Call(ctx, tools[0].Definition.Name, json.RawMessage(`{}`))
	if err != nil || !strings.Contains(result, "awaiting_user_approval") || !notified || remote.calls != 0 {
		t.Fatal("approval bypass", result, err)
	}
	if _, err = registry.Call(ctx, tools[0].Definition.Name, json.RawMessage(`{}`)); err != nil || remote.calls != 1 {
		t.Fatal("approved tool call", err)
	}
	oldName := tools[0].Definition.Name
	if err = s.Select(ctx, 1, id, store.c.Revision, false, []string{"read"}); err != nil {
		t.Fatal(err)
	}
	if _, err = registry.Call(ctx, oldName, json.RawMessage(`{}`)); err == nil || remote.calls != 1 {
		t.Fatal("revoked tool executed from old catalog")
	}
	if err = s.Select(ctx, 1, id, store.c.Revision, true, []string{"read"}); err != nil {
		t.Fatal(err)
	}
	newTools, _ := s.Tools(ctx, identity)
	if newTools[0].Definition.Name == oldName {
		t.Fatal("old approval reusable")
	}
	if err = s.Discover(ctx, 1, id, store.c.Revision, "Internal", "https://different.test/mcp", ""); err == nil || remote.discoveries != 1 {
		t.Fatal("old token sent to new host")
	}
	if err = s.Discover(ctx, 1, id, store.c.Revision, "Internal", store.c.Endpoint, ""); err != nil || remote.token != "test-secret" || store.c.Enabled || store.c.Tools[0].Selected {
		t.Fatal("refresh did not reset selection", err)
	}
	if err = s.Delete(ctx, 2, id, store.c.Revision); err == nil {
		t.Fatal("member deleted")
	}
	if err = s.Delete(ctx, 1, id, store.c.Revision); err != nil || store.token != "" {
		t.Fatal("delete retained credential", err)
	}
}
