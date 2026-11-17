package handler

import (
	"context"
	"nudge/internal/analytics"
	"nudge/internal/repository"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type inspectorFixture struct{}

func (inspectorFixture) Monitor(_ context.Context, _ int64, _ bool, f repository.MonitorFilters) (repository.MonitorSummary, error) {
	now := time.Date(2026, 11, 16, 20, 10, 0, 0, time.UTC)
	cost := 0.00145
	usage := &analytics.AIUsage{PromptTokens: 7953, CompletionTokens: 452, TotalTokens: 8405, CachedTokens: 4608, ReasoningTokens: 128, CostUSD: &cost}
	result := repository.MonitorSummary{Requests: 1248, Completed: 1196, Running: 3, Queued: 2, Failed: 47, IssueRequests: 52, ToolIssueRequests: 12, ActiveUsers: 18, Tokens: 3284100, PromptTokens: 2840020, CompletionTokens: 444080, CachedTokens: 1650000, ReasoningTokens: 65000, ModelCallCount: 1824, CostCalls: 1824, CostUSD: 4.1821, AverageMS: 3250, P95MS: 8200, UpdatedAt: now, FilteredRequests: 4}
	for i, v := range []int64{104, 167, 130, 260, 182, 243, 162} {
		result.Daily = append(result.Daily, repository.DailyUsage{Date: now.AddDate(0, 0, i-6).Format("2006-01-02"), Messages: v, Tokens: v*2500 + int64(i)*13000})
	}
	result.Models = []repository.MonitorModel{{Model: "deepseek/deepseek-v4.1-flash-1017", Provider: "openrouter.ai", Tokens: 2458100, PromptTokens: 2180000, CompletionTokens: 278100, Requests: 1042, Calls: 1432}, {Model: "workspace/advanced", Provider: "openrouter.ai", Tokens: 826000, PromptTokens: 660020, CompletionTokens: 165980, Requests: 206, Calls: 392}}
	result.Users = []repository.MonitorUser{{Name: "Banu Karakaya", User: "U01", Requests: 428, Tokens: 1240350, Errors: 12, LastSeen: now}, {Name: "Latif Atçı", User: "U02", Requests: 316, Tokens: 840200, Errors: 8, LastSeen: now}}
	for i := 0; i < 4; i++ {
		j := repository.MonitorRequest{ID: int64(42 - i), User: "U01", Name: "Banu Karakaya", Channel: "C08TEAM", Thread: "1794857748.289379", Status: "completed", Stage: "delivery_completed", Attempts: 1, CreatedAt: now.Add(-10 * time.Second), UpdatedAt: now, DeliveredAt: &now, LatencyMS: 10000, Model: "deepseek/deepseek-v4.1-flash-1017", PromptTokens: 7953, CompletionTokens: 452, TotalTokens: 8405, CachedTokens: 4608, CostCalls: 2, ModelCallCount: 2, CostUSD: .00145, TraceCount: 12, ContentVisible: true}
		for _, stage := range []string{"worker_started", "identity_lookup", "installation_lookup", "history_loading", "model_starting", "model_running", "tool_started", "tool_completed", "usage", "response_ready", "delivery_started", "delivery_completed"} {
			j.Events = append(j.Events, repository.MonitorEvent{LiveEvent: analytics.LiveEvent{Stage: stage, Model: j.Model}, At: now, Attempt: 1})
		}
		if i == 1 {
			j.Status = "running"
			j.Stage = "model_running"
			j.DeliveredAt = nil
		}
		if i == 2 {
			j.Status = "failed"
			j.Stage = "tool_failed"
			j.ErrorCode = "slack_missing_scope"
			j.ToolFailures = 1
		}
		if f.JobID > 0 {
			result.FilteredRequests = 1
			j.ModelCallCount = 1
			j.CostCalls = 1
			if j.ID != f.JobID {
				continue
			}
			j.RequestText = "GitHub hesabımı bağlamak istiyorum. Takımın repolarına nasıl erişebilirim?"
			j.ResponseText = "GitHub hesabını bağlaman için dashboard bağlantısını özel mesajına gönderdim. Hesaplarım → GitHub üzerinden kendi hesabını doğrulayabilirsin."
			j.ModelCalls = []analytics.ModelCall{{Model: j.Model, Provider: "openrouter.ai", Status: 200, StartedAt: now, DurationMS: 1842, Usage: usage, ID: "gen-synthetic-preview", Request: `{"model":"deepseek/deepseek-v4.1-flash-1017","instructions":"You are Nudgy, a helpful assistant inside Slack.","input":[{"role":"user","content":"GitHub hesabımı bağlamak istiyorum."},{"type":"function_call","name":"workspace_connect","arguments":"{}"},{"type":"function_call_output","output":"{\"connection_link_sent_privately\":true}"}]}`, Response: `{"id":"gen-synthetic-preview","status":"completed","usage":{"input_tokens":7953,"output_tokens":452,"input_tokens_details":{"cached_tokens":4608},"cost":0.00145},"output":[{"role":"assistant","content":[{"type":"output_text","text":"Dashboard bağlantısını özel mesajına gönderdim."}]}]}`}}
		}
		result.Recent = append(result.Recent, j)
	}
	return result, nil
}
func TestInspectorRenderingAndOptionalPreview(t *testing.T) {
	h, members, mux := workspaceFixture(t)
	h.WithMonitor(inspectorFixture{})
	for _, page := range []struct{ path, name string }{{"/dashboard", "overview.html"}, {"/dashboard/operations?job=42", "inspector.html"}, {"/dashboard/accounts", "accounts.html"}, {"/dashboard/setup", "setup.html"}} {
		response := workspaceRequest(members, mux, 1, "GET", page.path, nil)
		if response.Code != 200 || !strings.Contains(response.Body.String(), "</html>") {
			t.Fatalf("incomplete render %s %d %s", page.path, response.Code, response.Body.String())
		}
		if page.name == "inspector.html" && (!strings.Contains(response.Body.String(), "Modelin gördüğü iletiler") || !strings.Contains(response.Body.String(), "$0.001450")) {
			t.Fatal("inspector metrics missing")
		}
		if dir := os.Getenv("NUDGY_PREVIEW_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, page.name), response.Body.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
