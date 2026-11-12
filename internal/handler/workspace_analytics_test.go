package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nudge/internal/analytics"
	"nudge/internal/repository"
	"nudge/internal/workspace"
)

type customerAnalytics struct {
	user    int64
	team    bool
	calls   int
	filters repository.DashboardFilters
	err     error
}

func (s *customerAnalytics) WorkspaceDashboard(_ context.Context, user int64, team bool, f repository.DashboardFilters) (repository.DashboardSummary, error) {
	s.user = user
	s.team = team
	s.filters = f
	s.calls++
	return repository.DashboardSummary{TotalMessages: 26, SuccessfulMessages: 25, ErrorCount: 1, TotalTokens: 12345, Daily: []repository.DailyUsage{{Date: "2026-11-11", Tokens: 12345, Messages: 26}}, Models: []repository.ModelUsage{{Model: "model-a", Tokens: 12345, Messages: 26}}, Recent: []repository.Interaction{{Action: "AGENT", Request: "SECRET_MESSAGE", Response: "SECRET_REPLY", Model: "model-a", Status: "success", CreatedAt: time.Now(), Operations: []analytics.Operation{{Tool: "web_search", Status: "success", Detail: "SECRET_TOOL_RESULT", Diff: "SECRET_DIFF"}}}}}, s.err
}
func TestCustomerAnalyticsAuthenticationAndScope(t *testing.T) {
	h, s, mux := workspaceFixture(t)
	repo := &customerAnalytics{}
	h.WithAnalytics(repo)
	for _, path := range []string{"/dashboard", "/dashboard/operations", "/dashboard/usage"} {
		before := repo.calls
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path+"?user_id=1&team_id=OTHER", nil)
		r.Header.Set("X-Dashboard-Token", "operator")
		mux.ServeHTTP(w, r)
		if repo.calls != before || !strings.Contains(w.Body.String(), "Hoş geldin.") {
			t.Fatal("unauthenticated data read")
		}
		w = workspaceRequest(s, mux, 2, "GET", path+"?user_id=1&team_id=OTHER", nil)
		if w.Code != 200 || repo.user != 2 || repo.team || !strings.Contains(w.Body.String(), "12.345") || strings.Contains(w.Body.String(), "SECRET_") {
			t.Fatal("identity/content isolation", w.Code)
		}
	}
	calls := repo.calls
	for _, path := range []string{"/dashboard?scope=workspace", "/dashboard/usage?scope=workspace", "/dashboard/operations?scope=workspace"} {
		w := workspaceRequest(s, mux, 2, "GET", path, nil)
		if w.Code != 403 || repo.calls != calls {
			t.Fatal("member accessed totals")
		}
	}
	w := workspaceRequest(s, mux, 1, "GET", "/dashboard/usage?scope=workspace", nil)
	if w.Code != 200 || !repo.team || repo.user != 1 || strings.Contains(w.Body.String(), "İşlem geçmişi") || strings.Contains(w.Body.String(), "SECRET_") {
		t.Fatal("manager totals exposed history")
	}
	w = workspaceRequest(s, mux, 1, "GET", "/dashboard/operations?scope=workspace", nil)
	if w.Code != 403 {
		t.Fatal("manager accessed team history")
	}
}
func TestCustomerAnalyticsFiltersAndFailure(t *testing.T) {
	h, s, mux := workspaceFixture(t)
	repo := &customerAnalytics{}
	h.WithAnalytics(repo)
	for _, query := range []string{"range=bad", "scope=other", "offset=-1", "offset=3", "offset=1000025", "status=bad", "model=" + strings.Repeat("a", 201)} {
		w := workspaceRequest(s, mux, 2, "GET", "/dashboard/operations?"+query, nil)
		if w.Code != 400 || repo.calls != 0 {
			t.Fatal("invalid filters accepted", query, w.Code)
		}
	}
	w := workspaceRequest(s, mux, 2, "GET", "/dashboard/operations?range=30d&offset=25&status=issues&model=model-a", nil)
	if w.Code != 200 || repo.filters.Offset != 25 || repo.filters.Limit != 25 || repo.filters.Status != "issues" || repo.filters.Model != "model-a" || repo.filters.To.Sub(repo.filters.From) != 30*24*time.Hour || !strings.Contains(w.Body.String(), "Önceki") {
		t.Fatal("filter/pagination failed", w.Code)
	}
	w = workspaceRequest(s, mux, 2, "GET", "/dashboard/operations", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Sonraki") {
		t.Fatal("next page missing")
	}
	repo.err = errors.New("SECRET_DB_ERROR")
	w = workspaceRequest(s, mux, 2, "GET", "/dashboard/usage", nil)
	if w.Code != 503 || strings.Contains(w.Body.String(), "SECRET_DB_ERROR") || strings.Contains(w.Body.String(), "12.345") {
		t.Fatal("error exposed data or partial success")
	}
}

func TestOverviewAndSetupSeparation(t *testing.T) {
	h, members, mux, ai, _ := aiFixture(t)
	repo := &customerAnalytics{}
	h.WithAnalytics(repo)
	ai.byTeam["T"] = workspace.AICredentials{AISettings: workspace.AISettings{HasKey: true, Model: "vendor/daily"}, APIKey: "SECRET_API_KEY"}
	// No shared integrations are enabled: optional connections must not cause a reminder.
	for _, user := range []int64{1, 2} {
		w := workspaceRequest(members, mux, user, "GET", "/dashboard?range=30d&offset=25&status=issues", nil)
		body := w.Body.String()
		if w.Code != 200 || repo.filters.Limit != 5 || repo.filters.Offset != 0 || repo.filters.Status != "" || repo.filters.To.Sub(repo.filters.From) != 30*24*time.Hour {
			t.Fatal("overview did not query the first five personal records", w.Code, repo.filters)
		}
		for _, want := range []string{"Toplam token", "Son işlemlerim", "Günlük token kullanımı", `action="/dashboard"`, `href="/dashboard/setup"`} {
			if !strings.Contains(body, want) {
				t.Errorf("overview missing %q", want)
			}
		}
		for _, unwanted := range []string{"setup-reminder", "Kurulum adımları", "Senin modellerin. Ekibinin hesapları.", "SECRET_", "Sayfa 1"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("overview contains %q", unwanted)
			}
		}
		calls := repo.calls
		w = workspaceRequest(members, mux, user, "GET", "/dashboard/setup", nil)
		body = w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "Kurulum adımları") || !strings.Contains(body, "AI ayarların hazır.") || strings.Contains(body, "Toplam token") || strings.Contains(body, "0 / 4") || repo.calls != calls {
			t.Fatal("setup was not separated from usage", w.Code)
		}
		if user == 2 && (strings.Contains(body, `href="/dashboard/ai"`) || strings.Contains(body, `href="/dashboard/integrations"`)) {
			t.Fatal("setup exposes management controls to member")
		}
	}
	w := workspaceRequest(members, mux, 1, "GET", "/dashboard?scope=workspace", nil)
	if w.Code != 200 || !repo.team || strings.Contains(w.Body.String(), "Son işlemlerim") || strings.Contains(w.Body.String(), "activity-record") || strings.Contains(w.Body.String(), "SECRET_") {
		t.Fatal("aggregate overview exposed personal history", w.Code)
	}
	delete(ai.byTeam, "T")
	for _, user := range []int64{1, 2} {
		w = workspaceRequest(members, mux, user, "GET", "/dashboard", nil)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "setup-reminder") || !strings.Contains(body, "Toplam token") {
			t.Fatal("incomplete setup hid usage or omitted reminder")
		}
		if user == 1 && !strings.Contains(body, "Kurulumu tamamla →") {
			t.Fatal("manager lacks setup link")
		}
		if user == 2 && (!strings.Contains(body, "Yöneticin API anahtarını") || strings.Contains(body, "Kurulumu tamamla →")) {
			t.Fatal("member told to configure workspace AI")
		}
	}
}
