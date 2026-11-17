package postgres

import (
	"context"
	"encoding/json"
	"nudge/internal/analytics"
	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/migrations"
	"strings"
	"testing"
	"time"
)

func TestModelInspectorUpgradePreservesExistingRequests(t *testing.T) {
	db := reviewDB(t, 2)
	ctx := context.Background()
	// Recreate the deployed 013 schema inside this disposable test schema.
	_, err := db.Exec(ctx, `DROP TABLE nudge_model_calls,nudge_http_errors;
 ALTER TABLE nudge_job_model_usage DROP COLUMN cached_tokens,DROP COLUMN reasoning_tokens,DROP COLUMN cost_usd,DROP COLUMN cost_calls;
 ALTER TABLE nudge_slack_jobs DROP COLUMN had_error,DROP COLUMN model_call_count;
 DELETE FROM nudge_schema_migrations WHERE version='014_model_inspector.sql';
 INSERT INTO nudge_slack_jobs(team_id,channel_id,thread_ts,slack_user_id,event_key,payload,result,status,trace)
 VALUES ('T','C','1','U','recovered','{"event":{"text":"retained question"}}','{"Message":"retained answer"}','completed','[{"error_code":"provider_http_error"},{"error_code":"none"}]'),
 ('T','C','2','U','failed','{}',NULL,'failed','[]'),
 ('T','C','3','U','clean','{}',NULL,'completed','[]');
 INSERT INTO nudge_job_model_usage(job_id,provider,model,calls,prompt_tokens,completion_tokens,total_tokens)
 SELECT id,'synthetic','test/model',1,100,20,120 FROM nudge_slack_jobs WHERE event_key='recovered'`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrations.Apply(ctx, db); err != nil {
			t.Fatal("upgrade or repeated startup failed", err)
		}
	}
	var requests, errors, captures int
	if err := db.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE had_error),sum(model_call_count) FROM nudge_slack_jobs`).Scan(&requests, &errors, &captures); err != nil || requests != 3 || errors != 2 || captures != 0 {
		t.Fatal("upgrade lost requests, errors or invented old captures", requests, errors, captures, err)
	}
	var question, answer string
	var tokens, cached, costCalls int
	if err := db.QueryRow(ctx, `SELECT payload#>>'{event,text}',result->>'Message',total_tokens,cached_tokens,cost_calls FROM nudge_slack_jobs j JOIN nudge_job_model_usage u ON u.job_id=j.id WHERE event_key='recovered'`).Scan(&question, &answer, &tokens, &cached, &costCalls); err != nil || question != "retained question" || answer != "retained answer" || tokens != 120 || cached != 0 || costCalls != 0 {
		t.Fatal("historical content/usage changed or unknown cost became reported", err)
	}
}

func TestModelInspectorEncryptionUsageAndOwnerIsolation(t *testing.T) {
	db := reviewDB(t, 4)
	ctx := context.Background()
	cipher := reviewCipher(t)
	installs := NewInstallationRepository(db)
	owner, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "A", SlackUserID: "OWNER", BotToken: "test"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := installs.UpsertUser(ctx, "A", "MEMBER")
	if err != nil {
		t.Fatal(err)
	}
	other, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "B", SlackUserID: "MEMBER", BotToken: "test"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := NewSlackJobRepository(ctx, db, cipher)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewAnalyticsRepository(ctx, db, cipher)
	if err != nil {
		t.Fatal(err)
	}
	err = jobs.Enqueue(ctx, repository.SlackJob{Key: repository.SlackJobKey{Team: "A", User: "MEMBER", Channel: "D1", Thread: "1"}, EventKey: "one", Payload: json.RawMessage(`{"event":{"text":"private question"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	j, err := jobs.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cost := 0.002
	usage := analytics.AIUsage{Provider: "openrouter.ai", Model: "test/model", PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120, CachedTokens: 70, ReasoningTokens: 5, CostUSD: &cost}
	if err = jobs.ReportJob(ctx, j, analytics.LiveEvent{Stage: "usage", Usage: &usage}); err != nil {
		t.Fatal(err)
	}
	if err = jobs.ReportModelCall(ctx, j, analytics.ModelCall{ID: "gen-1", Model: "test/model", Provider: "openrouter.ai", Status: 200, Usage: &usage, Request: `{"input":"private model input"}`, Response: `{"output":"private model output"}`, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err = jobs.SaveResult(ctx, j, json.RawMessage(`{"Message":"private answer"}`)); err != nil {
		t.Fatal(err)
	}
	var contents string
	if err = db.QueryRow(ctx, `SELECT contents FROM nudge_model_calls WHERE job_id=$1`, j.ID).Scan(&contents); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(contents, "private") || !strings.HasPrefix(contents, "v2:") {
		t.Fatal("plaintext trace")
	}
	// Failed attempts have no usage but must count; a subsequent success must
	// not erase the earlier error from the dashboard's issue totals.
	if err = jobs.ReportModelCall(ctx, j, analytics.ModelCall{Status: 503, ErrorCode: "provider_http_error", Model: "test/model"}); err != nil {
		t.Fatal(err)
	}
	if err = jobs.ReportJob(ctx, j, analytics.LiveEvent{Stage: "provider_failed", ErrorCode: "provider_http_error"}); err != nil {
		t.Fatal(err)
	}
	if err = jobs.ReportJob(ctx, j, analytics.LiveEvent{Stage: "usage", ErrorCode: "none"}); err != nil {
		t.Fatal(err)
	}
	// No message data in list polling, even for the owner.
	list, err := repo.Monitor(ctx, member, false, repository.MonitorFilters{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "private") {
		t.Fatal("content loaded without selection")
	}
	personal, err := repo.Monitor(ctx, member, false, repository.MonitorFilters{JobID: j.ID})
	if err != nil {
		t.Fatal(err)
	}
	if personal.CachedTokens != 70 || personal.CostUSD != cost || personal.CostCalls != 1 || personal.ModelCallCount != 2 || personal.IssueRequests != 1 || personal.Recent[0].RequestText != "private question" || personal.Recent[0].ResponseText != "private answer" || len(personal.Recent[0].ModelCalls) != 2 || personal.Recent[0].ModelCalls[0].Request == "" {
		t.Fatalf("details missing: %+v", personal)
	}
	team, err := repo.Monitor(ctx, owner, true, repository.MonitorFilters{JobID: j.ID})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(team)
	if strings.Contains(string(raw), "private") || team.Recent[0].ContentVisible {
		t.Fatal("manager read member content")
	}
	outsider, err := repo.Monitor(ctx, other, true, repository.MonitorFilters{JobID: j.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(outsider.Recent) != 0 {
		t.Fatal("cross workspace leak")
	}
	if err = repo.RecordHTTPError(ctx, member, repository.HTTPError{RequestID: "request-test", Method: "POST", Route: "POST /dashboard/ai", Status: 503}); err != nil {
		t.Fatal(err)
	}
	summary, err := repo.Monitor(ctx, member, false, repository.MonitorFilters{})
	if err != nil || len(summary.HTTPErrors) != 1 {
		t.Fatal("HTTP error not visible", err)
	}
	// Removing retained jobs also removes encrypted content by FK cascade.
	if _, err = db.Exec(ctx, `DELETE FROM nudge_slack_jobs WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow(ctx, `SELECT COUNT(*) FROM nudge_model_calls`).Scan(&n); err != nil || n != 0 {
		t.Fatal("trace retained without job")
	}
}
