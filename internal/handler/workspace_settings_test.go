package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"nudge/internal/coding"
	"nudge/internal/service"
)

type settingsPageStore struct {
	*workspaceMemory
	zone                         string
	enabled                      bool
	deleted, exported            int64
	auditReads, preferenceWrites int
}

func (s *settingsPageStore) Preferences(context.Context, int64) (string, bool, error) {
	return s.zone, s.enabled, nil
}
func (s *settingsPageStore) SavePreferences(_ context.Context, _ int64, zone string, enabled bool) error {
	if zone != "Europe/Istanbul" {
		return errors.New("invalid timezone")
	}
	s.zone, s.enabled = zone, enabled
	s.preferenceWrites++
	return nil
}
func (s *settingsPageStore) ExportPersonalData(_ context.Context, user int64) (json.RawMessage, error) {
	s.exported = user
	return json.RawMessage(`{"profile":"synthetic"}`), nil
}
func (s *settingsPageStore) DeletePersonalData(_ context.Context, user int64) error {
	s.deleted = user
	return nil
}
func (s *settingsPageStore) AuditLog(context.Context, int64) (json.RawMessage, error) {
	s.auditReads++
	return json.RawMessage(`[{"id":1,"actor_id":1,"action":"policy_changed","target":"<script>alert(1)</script>","created_at":"2026-11-16T12:00:00Z"}]`), nil
}

type settingsActions struct {
	actionStore
	mode string
}

func (s *settingsActions) ActionMode(context.Context, int64, string) (string, error) {
	return "approval", nil
}
func (s *settingsActions) SetActionMode(_ context.Context, _ int64, _ string, mode string) error {
	s.mode = mode
	return nil
}

func TestWorkspaceSettingsUseSharedShellAndRetainAccessGuards(t *testing.T) {
	h, memory, mux := workspaceFixture(t)
	store := &settingsPageStore{workspaceMemory: memory, zone: "Europe/Istanbul", enabled: true}
	h.store = store
	actions := &settingsActions{}
	events := &SlackEventHandler{}
	events.WithToolModule(func(coding.Identity) []service.RegisteredTool {
		return []service.RegisteredTool{{Definition: service.ToolDefinition{Name: "ext_test_write", Description: "Synthetic action", InputSchema: map[string]any{"type": "object"}}, Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }}}
	})
	h.WithActions(actions, events)
	for _, section := range []string{"data", "preferences", "actions", "audit"} {
		w := workspaceRequest(memory, mux, 1, "GET", "/dashboard/"+section, nil)
		if w.Code != 200 {
			t.Fatalf("%s status=%d body=%s", section, w.Code, w.Body.String())
		}
		for _, part := range []string{`class="workspace-page"`, `class="sidebar"`, `href="/dashboard/styles.css"`, `href="/dashboard/workspace.css"`, `class="nav-item active" href="/dashboard/` + section + `"`, `form-action 'self'`} {
			if !strings.Contains(w.Body.String()+w.Header().Get("Content-Security-Policy"), part) {
				t.Errorf("%s missing shared UI/security component %s", section, part)
			}
		}
		if strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
			t.Fatal("audit target rendered as executable HTML")
		}
	}
	if store.deleted != 0 || store.preferenceWrites != 0 || actions.mode != "" {
		t.Fatal("viewing settings changed data")
	}
	for _, path := range []string{"/dashboard/preferences", "/dashboard/actions", "/dashboard/audit", "/dashboard/audit/export"} {
		before := store.auditReads
		if w := workspaceRequest(memory, mux, 2, "GET", path, nil); w.Code != 403 {
			t.Fatalf("member accessed %s", path)
		}
		if before != store.auditReads {
			t.Fatal("member read audit data before authorization")
		}
	}
	for _, path := range []string{"/dashboard/data/export", "/dashboard/audit/export"} {
		w := workspaceRequest(memory, mux, 1, "GET", path, nil)
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment;") || !json.Valid(w.Body.Bytes()) {
			t.Fatalf("download broken: %s", path)
		}
	}
	if w := workspaceRequest(memory, mux, 2, "GET", "/dashboard/data", nil); w.Code != 200 {
		t.Fatal("member cannot open personal data")
	}
	w := workspaceRequest(memory, mux, 2, "POST", "/dashboard/data/delete", url.Values{"confirm": {"wrong"}})
	if w.Code != 400 || store.deleted != 0 || !strings.Contains(w.Body.String(), `role="alert"`) {
		t.Fatal("missing confirmation did not remain a safe dashboard error")
	}
	w = workspaceRequest(memory, mux, 2, "POST", "/dashboard/data/delete", url.Values{"confirm": {"SİL"}, "csrf": {"wrong"}})
	if w.Code != 403 || store.deleted != 0 {
		t.Fatal("delete bypassed CSRF")
	}
	w = workspaceRequest(memory, mux, 1, "POST", "/dashboard/preferences", url.Values{"timezone": {"<invalid>"}})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "&lt;invalid&gt;") || store.preferenceWrites != 0 {
		t.Fatal("invalid timezone lost form or escaped value")
	}
	w = workspaceRequest(memory, mux, 1, "POST", "/dashboard/preferences", url.Values{"timezone": {"Europe/Istanbul"}, "enabled": {"true"}})
	if w.Code != 303 || w.Header().Get("Location") != "/dashboard/preferences?saved=1" || store.preferenceWrites != 1 {
		t.Fatal("save/confirmation redirect broken")
	}
	w = workspaceRequest(memory, mux, 1, "POST", "/dashboard/actions", url.Values{"tool": {"ext_test_write"}, "mode": {"disabled"}})
	if w.Code != 303 || actions.mode != "disabled" {
		t.Fatal("tool policy form broken")
	}
	// Deletion is exercised only against this in-memory synthetic fixture.
	w = workspaceRequest(memory, mux, 2, "POST", "/dashboard/data/delete", url.Values{"confirm": {"SİL"}, "user_id": {"1"}})
	if w.Code != 303 || store.deleted != 2 {
		t.Fatal("personal deletion changed owner binding")
	}
}
