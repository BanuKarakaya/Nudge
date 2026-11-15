package handler

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"nudge/internal/coding"
	"nudge/internal/connector"
	"nudge/internal/service"
	"nudge/internal/workspace"
)

type connectorMemory struct {
	c      connector.Connection
	secret string
	calls  int
}

func (s *connectorMemory) List(_ context.Context, user int64) ([]connector.Connection, error) {
	if user == 3 || s.c.ID == "" {
		return nil, nil
	}
	return []connector.Connection{s.c}, nil
}
func (s *connectorMemory) Secret(_ context.Context, user int64, id, rev string) (string, error) {
	if user == 3 || id != s.c.ID || rev != s.c.Revision {
		return "", workspace.ErrDenied
	}
	return s.secret, nil
}
func (s *connectorMemory) Save(_ context.Context, user int64, c connector.Connection, token, expected string) error {
	if user != 1 || expected != s.c.Revision {
		return workspace.ErrDenied
	}
	s.c = c
	s.secret = token
	return nil
}
func (s *connectorMemory) Delete(_ context.Context, user int64, id, rev string) error {
	if user != 1 || id != s.c.ID || rev != s.c.Revision {
		return workspace.ErrDenied
	}
	s.c = connector.Connection{}
	s.secret = ""
	return nil
}
func (s *connectorMemory) Discover(context.Context, string, string) ([]connector.Tool, error) {
	s.calls++
	return []connector.Tool{{Name: "ticket_read", Description: "<script>alert(1)</script>", Schema: map[string]any{"type": "object"}, Fingerprint: "v1"}}, nil
}
func (s *connectorMemory) Call(context.Context, string, string, connector.Tool, json.RawMessage) (string, error) {
	s.calls++
	return "ok", nil
}

func TestConnectorDashboardAuthorizationSelectionAndRuntime(t *testing.T) {
	h, members, mux := workspaceFixture(t)
	store := &connectorMemory{}
	events := &SlackEventHandler{}
	h.WithConnectors(&connector.Service{Store: store, Members: members, Remote: store}, events)
	path := "/dashboard/connectors"
	for _, method := range []string{"GET", "POST"} {
		if w := workspaceRequest(members, mux, 2, method, path, nil); w.Code != 403 {
			t.Fatal("member managed connectors", w.Code)
		}
	}
	form := url.Values{"action": {"discover"}, "name": {"Internal tickets"}, "endpoint": {"https://company.test/mcp"}, "token": {"unit-private-credential"}, "csrf": {"wrong"}}
	if w := workspaceRequest(members, mux, 1, "POST", path, form); w.Code != 403 || store.calls != 0 {
		t.Fatal("CSRF reached remote")
	}
	form.Del("csrf")
	if w := workspaceRequest(members, mux, 1, "POST", path, form); w.Code != 303 {
		t.Fatal("discovery failed", w.Code, w.Body)
	}
	w := workspaceRequest(members, mux, 1, "GET", path, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), store.secret) || strings.Contains(w.Body.String(), "<script>alert") || !strings.Contains(w.Body.String(), "ticket_read") || w.Header().Get("Cache-Control") == "" {
		t.Fatal("unsafe connection page", w.Code, w.Body)
	}
	if w = workspaceRequest(members, mux, 3, "GET", path, nil); strings.Contains(w.Body.String(), "Internal tickets") {
		t.Fatal("cross-team page leak")
	}
	selection := url.Values{"action": {"select"}, "id": {store.c.ID}, "revision": {store.c.Revision}, "tool": {"ticket_read"}, "enabled": {"true"}}
	if w = workspaceRequest(members, mux, 1, "POST", path, selection); w.Code != 400 || store.c.Enabled {
		t.Fatal("shared-account consent missing")
	}
	selection.Set("shared_account", "true")
	if w = workspaceRequest(members, mux, 1, "POST", path, selection); w.Code != 303 || !store.c.Enabled {
		t.Fatal("selection failed", w.Code, w.Body)
	}
	base := service.NewAgentTools(nil, nil, nil, 2, time.Now(), time.UTC)
	tools, err := events.moduleTools(context.Background(), base, coding.Identity{UserID: 2, Team: "T"})
	if err != nil {
		t.Fatal(err)
	}
	name := connector.ToolName(store.c, store.c.Tools[0])
	proposal, err := events.prepareAction(context.Background(), service.ActionProposal{Identity: coding.Identity{UserID: 2, Team: "T"}, Tool: name})
	if err != nil || !strings.Contains(proposal.Display, "https://company.test/mcp") || !strings.Contains(proposal.Display, "ticket_read") {
		t.Fatal("approval lacks real destination", proposal, err)
	}
	found := false
	for _, d := range tools.Definitions() {
		if d.Name == name {
			found = true
		}
	}
	if !found || !service.ToolMutates(tools, name) {
		t.Fatal("MCP omitted from runtime or bypasses approval")
	}
	// Runtime reconstruction after configuration change must omit old names.
	selection.Set("revision", store.c.Revision)
	selection.Set("enabled", "false")
	if w = workspaceRequest(members, mux, 1, "POST", path, selection); w.Code != 303 {
		t.Fatal(w.Code)
	}
	updated, err := events.moduleTools(context.Background(), base, coding.Identity{UserID: 2, Team: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = updated.Call(context.Background(), name, json.RawMessage(`{}`)); err == nil || store.calls != 1 {
		t.Fatal("old approval executed")
	}
	if w = workspaceRequest(members, mux, 1, "POST", path, url.Values{"action": {"delete"}, "id": {store.c.ID}, "revision": {store.c.Revision}}); w.Code != 303 || store.secret != "" {
		t.Fatal("delete failed")
	}
}
