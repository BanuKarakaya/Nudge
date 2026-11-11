package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"nudge/internal/service"
	"strings"
	"testing"
	"time"

	"nudge/internal/workspace"
)

type memoryAI struct {
	members      *workspaceMemory
	byTeam       map[string]workspace.AICredentials
	reads, saves int
}

func (s *memoryAI) AISettings(ctx context.Context, id int64) (workspace.AISettings, error) {
	m, e := s.members.Member(ctx, id)
	return s.byTeam[m.Team].AISettings, e
}
func (s *memoryAI) AICredentials(ctx context.Context, id int64) (workspace.AICredentials, error) {
	s.reads++
	m, e := s.members.Member(ctx, id)
	if e != nil {
		return workspace.AICredentials{}, e
	}
	c, ok := s.byTeam[m.Team]
	if !ok {
		return c, workspace.ErrAINotConfigured
	}
	return c, nil
}
func (s *memoryAI) SaveAI(ctx context.Context, id int64, c workspace.AICredentials, revision string) error {
	m, e := s.members.Member(ctx, id)
	if e != nil || !m.Manager {
		return workspace.ErrDenied
	}
	if s.byTeam[m.Team].Revision != revision {
		return workspace.ErrAIConflict
	}
	s.saves++
	c.Revision = fmt.Sprint(s.saves)
	c.HasKey = c.APIKey != ""
	c.HasSearchKey = c.SearchAPIKey != ""
	s.byTeam[m.Team] = c
	return nil
}

type fakeCatalog struct {
	err   error
	calls int
}

func (c *fakeCatalog) Models(context.Context, string, string) ([]workspace.Model, error) {
	c.calls++
	return []workspace.Model{{ID: "vendor/daily", Name: "Daily"}, {ID: "vendor/advanced", Name: "Advanced"}}, c.err
}
func (c *fakeCatalog) VerifySearch(context.Context, string, string) error { c.calls++; return c.err }
func aiFixture(t *testing.T) (*WorkspaceHandler, *workspaceMemory, *http.ServeMux, *memoryAI, *fakeCatalog) {
	h, m, mux := workspaceFixture(t)
	s := &memoryAI{members: m, byTeam: map[string]workspace.AICredentials{}}
	c := &fakeCatalog{}
	h.WithAI(s, c)
	return h, m, mux, s, c
}

func TestWorkspaceAIManagerOnlyAndNoSecretRendering(t *testing.T) {
	_, m, mux, s, c := aiFixture(t)
	for _, method := range []string{"GET", "POST"} {
		w := workspaceRequest(m, mux, 2, method, "/dashboard/ai", url.Values{"action": {"credentials"}, "api_key": {"forged"}, "user_id": {"1"}})
		if w.Code != 403 {
			t.Fatalf("member AI access: %d", w.Code)
		}
	}
	if s.reads != 0 || s.saves != 0 || c.calls != 0 {
		t.Fatal("unauthorized access reached keys or provider")
	}
	w := workspaceRequest(m, mux, 1, "POST", "/dashboard/ai", url.Values{"action": {"credentials"}, "provider": {"openrouter"}, "api_key": {"secret-workspace-key"}})
	if w.Code != 303 || s.saves != 1 {
		t.Fatalf("save credentials %d %s", w.Code, w.Body)
	}
	if s.byTeam["T"].Ready() {
		t.Fatal("credentials alone enabled inference")
	}
	w = workspaceRequest(m, mux, 1, "GET", "/dashboard/ai", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret-workspace-key") || !strings.Contains(w.Body.String(), "vendor/daily") {
		t.Fatal("key rendered or catalog missing", w.Code)
	}
	w = workspaceRequest(m, mux, 3, "GET", "/dashboard/ai", nil)
	if strings.Contains(w.Body.String(), "Anahtar kayıtlı") || strings.Contains(w.Body.String(), "vendor/daily") {
		t.Fatal("cross-team settings leak")
	}
	w = workspaceRequest(m, mux, 2, "GET", "/dashboard", nil)
	if strings.Contains(w.Body.String(), `href="/dashboard/ai"`) || strings.Contains(w.Body.String(), `href="/dashboard/integrations"`) {
		t.Fatal("member sees manager controls")
	}
}
func TestWorkspaceAIValidationRotationAndRemoval(t *testing.T) {
	_, m, mux, s, c := aiFixture(t)
	s.byTeam["T"] = workspace.AICredentials{AISettings: workspace.AISettings{Provider: "openrouter", Model: "vendor/daily", HasKey: true, Revision: "original"}, APIKey: "old-key"}
	form := url.Values{"action": {"models"}, "model": {"forged/model"}, "revision": {"original"}}
	if w := workspaceRequest(m, mux, 1, "POST", "/dashboard/ai", form); w.Code != 400 || s.saves != 0 {
		t.Fatal("forged model saved")
	}
	form.Set("model", "vendor/daily")
	form.Set("complex_model", "vendor/advanced")
	form.Set("revision", "stale")
	if w := workspaceRequest(m, mux, 1, "POST", "/dashboard/ai", form); w.Code != 409 || s.saves != 0 {
		t.Fatal("stale form saved")
	}
	form.Set("revision", "original")
	form.Set("csrf", "wrong")
	if w := workspaceRequest(m, mux, 1, "POST", "/dashboard/ai", form); w.Code != 403 || s.saves != 0 {
		t.Fatal("CSRF accepted")
	}
	form.Del("csrf")
	if w := workspaceRequest(m, mux, 1, "POST", "/dashboard/ai", form); w.Code != 303 || !s.byTeam["T"].Ready() {
		t.Fatal("models not saved")
	}
	c.err = errors.New("unavailable")
	form = url.Values{"action": {"credentials"}, "provider": {"openrouter"}, "api_key": {"replacement-secret"}, "revision": {s.byTeam["T"].Revision}}
	if w := workspaceRequest(m, mux, 1, "POST", "/dashboard/ai", form); w.Code != 400 || strings.Contains(w.Body.String(), "replacement-secret") {
		t.Fatal("failed validation leaked key")
	}
	if s.byTeam["T"].APIKey != "old-key" {
		t.Fatal("failed validation overwrote credentials")
	}
	c.err = nil
	if w := workspaceRequest(m, mux, 1, "POST", "/dashboard/ai", form); w.Code != 303 || s.byTeam["T"].Ready() {
		t.Fatal("new credentials retained old models")
	}
	form = url.Values{"action": {"remove"}, "revision": {s.byTeam["T"].Revision}}
	if w := workspaceRequest(m, mux, 1, "POST", "/dashboard/ai", form); w.Code != 303 || s.byTeam["T"].APIKey != "" || s.byTeam["T"].Model != "" {
		t.Fatal("key removal failed")
	}
}
func TestCustomerDashboardReplacesGlobalAnalytics(t *testing.T) {
	_, _, mux, _, _ := aiFixture(t)
	for _, path := range []string{"/api/v1/bookmarks", "/api/v1/raindrop/sync", "/api/dashboard", "/api/dashboard?team_id=OTHER", "/api/dashboard/branch-rules", "/api/dashboard/branch-rules/preview"} {
		for _, method := range []string{"GET", "PUT", "POST"} {
			r := httptest.NewRequest(method, path, nil)
			r.Header.Set("X-Dashboard-Token", "old-operator-token")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != 410 {
				t.Fatalf("legacy API %s %s returned %d", method, path, w.Code)
			}
		}
	}
	for _, path := range []string{"/dashboard", "/dashboard/ai", "/dashboard/accounts", "/dashboard/team"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Hoş geldin.") || strings.Contains(w.Body.String(), "Tüm çalışma alanları") {
			t.Fatal("unsigned customer dashboard", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/workspace/install?user_id=2&token=old-link", nil))
	if w.Code != 307 || w.Header().Get("Location") != "/dashboard/install?user_id=2&token=old-link" {
		t.Fatal("legacy login link lost")
	}
	for _, path := range []string{"/dashboard/styles.css", "/dashboard/workspace.css", "/dashboard/nudgy-mascot.webp"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatal("missing shared asset", path, w.Code)
		}
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/dashboard/app.js", nil))
	if w.Code != 404 {
		t.Fatal("old analytics app is still served")
	}
}

func (s *workspaceMemory) Members(ctx context.Context, id int64) ([]workspace.Member, error) {
	m, e := s.Member(ctx, id)
	if e != nil || !m.Manager {
		return nil, workspace.ErrDenied
	}
	var out []workspace.Member
	for _, v := range s.members {
		if v.Team == m.Team {
			out = append(out, v)
		}
	}
	return out, nil
}
func TestWorkspaceTeamRosterAndManagerRoutes(t *testing.T) {
	_, m, mux, _, _ := aiFixture(t)
	for _, path := range []string{"/dashboard/team", "/dashboard/integrations"} {
		if w := workspaceRequest(m, mux, 2, "GET", path, nil); w.Code != 403 {
			t.Fatal("member sees management", path, w.Code)
		}
	}
	w := workspaceRequest(m, mux, 1, "GET", "/dashboard/team", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Personal") {
		t.Fatal("team page leaked another tenant", w.Code)
	}
}

func TestSlackMissingWorkspaceAIExplainsSetupWithoutFallback(t *testing.T) {
	sender := &conversationSender{}
	store := make(conversationTestStore)
	calls := 0
	runtime := agentFunc(func(_ context.Context, r service.AgentRequest) (service.AgentResult, error) {
		calls++
		if r.UserID != 1 || r.Key.Team != "T" || r.Key.User != "U" {
			t.Fatal("Slack identity was not bound")
		}
		return service.AgentResult{}, workspace.ErrAINotConfigured
	})
	h := NewSlackEventHandler("", conversationInstallations{}, nil, nil, runtime, nil, store, sender, time.UTC)
	h.replyWithSummary(conversationEvent("Benim için bu görevi açıkla"))
	if calls != 1 || len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "kendi API anahtarını") {
		t.Fatal("missing-key setup message absent", sender.messages)
	}
}
