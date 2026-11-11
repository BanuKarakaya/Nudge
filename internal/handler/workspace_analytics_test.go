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
	for _, path := range []string{"/dashboard/operations", "/dashboard/usage"} {
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
	for _, path := range []string{"/dashboard/usage?scope=workspace", "/dashboard/operations?scope=workspace"} {
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
