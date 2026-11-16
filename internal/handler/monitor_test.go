package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"nudge/internal/analytics"
	"nudge/internal/repository"
	"strings"
	"testing"
	"time"
)

type monitorFake struct {
	user    int64
	team    bool
	calls   int
	filters repository.MonitorFilters
	err     error
}

func (s *monitorFake) Monitor(_ context.Context, user int64, team bool, f repository.MonitorFilters) (repository.MonitorSummary, error) {
	s.user = user
	s.team = team
	s.filters = f
	s.calls++
	now := time.Now()
	return repository.MonitorSummary{IssueRequests: 1, ToolIssueRequests: 1, Requests: 26, Running: 1, Tokens: 300, FilteredRequests: 26, UpdatedAt: now, Models: []repository.MonitorModel{{Model: "daily", Tokens: 300}}, Users: []repository.MonitorUser{{User: "U1", Name: "<script>alert(1)</script>", Requests: 26, Tokens: 300}}, Recent: []repository.MonitorRequest{{ToolFailures: 1, ID: 42, User: "U1", Name: "Banu", Status: "running", Stage: "tool_started", Tool: "web_search", CreatedAt: now, UpdatedAt: now, AvailableAt: &now, Events: []repository.MonitorEvent{{LiveEvent: analytics.LiveEvent{Stage: "tool_started", Model: "daily", Tool: "web_search"}, At: now, Attempt: 1}}}}}, s.err
}
func TestMonitorPagesAndPollingUseSessionRolesAndSafeHTML(t *testing.T) {
	h, members, mux := workspaceFixture(t)
	repo := &monitorFake{}
	h.WithMonitor(repo)
	for _, path := range []string{"/dashboard", "/dashboard/operations", "/dashboard/usage"} {
		w := workspaceRequest(members, mux, 1, "GET", path, nil)
		if w.Code != 200 || !repo.team || repo.user != 1 || !strings.Contains(w.Body.String(), "monitor.js") || !strings.Contains(w.Body.String(), "monitor-view") || !strings.Contains(w.Body.String(), "%3.8") || !strings.Contains(w.Body.String(), "1 istekte araç hatası") || strings.Contains(w.Body.String(), "<script>alert") {
			t.Fatal("manager monitor missing or unsafe", w.Code, w.Body)
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
			t.Fatal("live asset blocked")
		}
	}
	w := workspaceRequest(members, mux, 2, "GET", "/dashboard/operations?user_id=1&team_id=OTHER", nil)
	if w.Code != 200 || repo.user != 2 || repo.team || strings.Contains(w.Body.String(), "En çok kim kullanıyor?") {
		t.Fatal("member scope", w.Code)
	}
	before := repo.calls
	if w = workspaceRequest(members, mux, 2, "GET", "/dashboard/live/data?section=overview&scope=workspace", nil); w.Code != 403 || repo.calls != before {
		t.Fatal("member polled workspace")
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/dashboard/live/data?section=overview", nil))
	if w.Code != 401 || repo.calls != before {
		t.Fatal("unauthenticated poll")
	}
	w = workspaceRequest(members, mux, 1, "GET", "/dashboard/live/data?section=operations&range=24h&status=active&model=daily&user=U1&job=42&offset=25", nil)
	var response map[string]string
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response["updated_at"] == "" || !strings.Contains(response["html"], "web_search") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || repo.filters.Offset != 25 || repo.filters.JobID != 42 || repo.filters.To.Sub(repo.filters.From) != 24*time.Hour {
		t.Fatal("poll/filter mismatch", w.Code, w.Body, repo.filters)
	}
	for _, q := range []string{"section=bad", "section=overview&range=bad", "section=overview&offset=no&job=42", "section=overview&offset=1", "section=overview&status=bad", "section=overview&job=abc", "section=overview&scope=other"} {
		before = repo.calls
		w = workspaceRequest(members, mux, 1, "GET", "/dashboard/live/data?"+q, nil)
		if w.Code != 400 || repo.calls != before {
			t.Fatal("invalid filter accepted", q, w.Code)
		}
	}
	repo.err = errors.New("PRIVATE_DATABASE_ERROR")
	w = workspaceRequest(members, mux, 1, "GET", "/dashboard/live/data?section=overview", nil)
	if w.Code != 503 || strings.Contains(w.Body.String(), "PRIVATE_") {
		t.Fatal("raw error leak", w.Code, w.Body)
	}
}

func TestLiveErrorCodesDoNotExposeRawErrors(t *testing.T) {
	if liveError(errors.New("PRIVATE_SECRET")) != "processing_failed" {
		t.Fatal("unknown error leaked")
	}
	if liveError(context.DeadlineExceeded) != "timeout" {
		t.Fatal("timeout not classified")
	}
	if liveError(analytics.Failure("model_failed", errors.New("PRIVATE_PROVIDER_BODY"))) != "model_failed" {
		t.Fatal("typed model failure lost")
	}
}
