package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"nudge/internal/analytics"
	"nudge/internal/domain"
	"nudge/internal/repository"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLiveMonitorIncludesUnansweredJobsUsageAndIsolation(t *testing.T) {
	db := reviewDB(t, 4)
	ctx := context.Background()
	installs := NewInstallationRepository(db)
	owner, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "A", SlackUserID: "OWNER", BotToken: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := installs.UpsertUser(ctx, "A", "MEMBER")
	if err != nil {
		t.Fatal(err)
	}
	other, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "B", SlackUserID: "OWNER", BotToken: "synthetic-other"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := NewSlackJobRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewAnalyticsRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func(team, user, key string) {
		t.Helper()
		err := jobs.Enqueue(ctx, repository.SlackJob{Key: repository.SlackJobKey{Team: team, User: user, Channel: "C", Thread: "123"}, EventKey: key, Payload: json.RawMessage(`{"text":"PRIVATE_PROMPT_AND_TOKEN"}`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	enqueue("A", "OWNER", "one")
	enqueue("A", "MEMBER", "two")
	enqueue("B", "OWNER", "other")
	j, err := jobs.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []analytics.LiveEvent{{Stage: "model_running", Model: "daily"}, {Stage: "tool_started", Tool: "web_search", Model: "daily"}, {Stage: "tool_completed", Tool: "web_search", Model: "daily"}, {Stage: "usage", Model: "daily", Usage: &analytics.AIUsage{Provider: "Codex", Model: "daily", TotalTokens: 10, PromptTokens: 7, CompletionTokens: 3}}, {Stage: "usage", Model: "advanced", Usage: &analytics.AIUsage{Provider: "Codex", Model: "advanced", TotalTokens: 20, PromptTokens: 15, CompletionTokens: 5}}} {
		if err = jobs.ReportJob(ctx, j, e); err != nil {
			t.Fatal(err)
		}
	}
	stale := j
	stale.ClaimToken = "stale"
	if err = jobs.ReportJob(ctx, stale, analytics.LiveEvent{Stage: "model_running", Model: "wrong"}); !errors.Is(err, repository.ErrSlackJobLeaseLost) {
		t.Fatal("stale worker changed telemetry", err)
	}
	j2, err := jobs.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = jobs.ReportJob(ctx, j2, analytics.LiveEvent{Stage: "delivery_failed", ErrorCode: "slack_delivery_failed"}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE nudge_slack_jobs SET attempts=8 WHERE id=$1`, j2.ID); err != nil {
		t.Fatal(err)
	}
	if err = jobs.Retry(ctx, j2, 0); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordInteraction(ctx, repository.InteractionRecord{RequestID: "legacy", TeamID: "A", SlackUserID: "OWNER", UserID: owner, Status: "success", StartedAt: time.Now(), Usage: []analytics.AIUsage{{Model: "old", TotalTokens: 5}}, RequestText: "PRIVATE_TEXT"}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE nudge_ai_interactions SET request_text='PRIVATE_TEXT',response_text='PRIVATE_RESPONSE'`); err != nil {
		t.Fatal(err)
	}
	if err = installs.SaveProfileName(ctx, owner, "Banu"); err != nil {
		t.Fatal(err)
	}
	team, err := repo.Monitor(ctx, owner, true, repository.MonitorFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if team.Requests != 3 || team.Running != 1 || team.Failed != 1 || team.Undelivered != 1 || team.Tokens != 35 || team.ActiveUsers != 2 || len(team.Models) != 3 || len(team.Users) != 2 || len(team.Recent) != 3 {
		t.Fatalf("incomplete live summary: %+v", team)
	}
	if team.Recent[0].ID != j.ID || team.Recent[0].Name != "Banu" || len(team.Recent[0].Events) != 5 || team.Recent[0].Tool != "" || team.Users[0].User != "OWNER" {
		t.Fatal("active request/logs/leaderboard mismatch", team)
	}
	raw, _ := json.Marshal(team)
	if strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("private payload leaked")
	}
	if _, err = repo.Monitor(ctx, member, true, repository.MonitorFilters{}); err == nil {
		t.Fatal("member read workspace")
	}
	own, err := repo.Monitor(ctx, member, false, repository.MonitorFilters{User: "OWNER"})
	if err != nil || own.Requests != 1 || own.Failed != 1 || len(own.Users) != 0 {
		t.Fatal("forged personal scope", own, err)
	}
	foreign, err := repo.Monitor(ctx, other, true, repository.MonitorFilters{})
	if err != nil || foreign.Requests != 1 || foreign.Tokens != 0 || foreign.Recent[0].Model != "" {
		t.Fatal("cross tenant leak", foreign, err)
	}
	filtered, err := repo.Monitor(ctx, owner, true, repository.MonitorFilters{Status: "unanswered"})
	if err != nil || filtered.Requests != 3 || filtered.FilteredRequests != 1 || filtered.Recent[0].ID != j2.ID || filtered.Recent[0].ErrorCode != "slack_delivery_failed" {
		t.Fatal("unanswered request missing", filtered, err)
	}
	filtered, err = repo.Monitor(ctx, owner, true, repository.MonitorFilters{Model: "advanced"})
	if err != nil || filtered.Requests != 1 || filtered.Tokens != 30 {
		t.Fatal("model filter lost multi-model request", filtered, err)
	}
	filtered, err = repo.Monitor(ctx, member, false, repository.MonitorFilters{JobID: j.ID})
	if err != nil || filtered.FilteredRequests != 0 {
		t.Fatal("guessed job ID leaked", filtered, err)
	}
	// A completed interaction must not double count the queue request's usage.
	if err = jobs.SaveResult(ctx, j, json.RawMessage(`{"secret":"PRIVATE_RESPONSE"}`)); err != nil {
		t.Fatal(err)
	}
	if err = jobs.MarkDelivered(ctx, j); err != nil {
		t.Fatal(err)
	}
	if err = jobs.Complete(ctx, j, nil, repository.InteractionRecord{RequestID: "slack-job-" + strconv.FormatInt(j.ID, 10), TeamID: "A", SlackUserID: "OWNER", UserID: owner, Status: "success", StartedAt: time.Now(), Usage: []analytics.AIUsage{{Model: "advanced", TotalTokens: 20}}}, true); err != nil {
		t.Fatal(err)
	}
	team, err = repo.Monitor(ctx, owner, true, repository.MonitorFilters{})
	if err != nil || team.Requests != 3 || team.Tokens != 35 || team.Completed != 2 {
		t.Fatal("completion counted twice", team, err)
	}
}

func TestLiveTraceBoundedAndDeletedWithPersonalData(t *testing.T) {
	db := reviewDB(t, 3)
	ctx := context.Background()
	installs := NewInstallationRepository(db)
	user, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "T", SlackUserID: "U", BotToken: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, _ := NewSlackJobRepository(ctx, db)
	repo, _ := NewAnalyticsRepository(ctx, db)
	if err = jobs.Enqueue(ctx, repository.SlackJob{Key: repository.SlackJobKey{Team: "T", User: "U", Channel: "C", Thread: "1"}, EventKey: "one", Payload: json.RawMessage(`{"text":"PRIVATE_QUEUED_MESSAGE"}`)}); err != nil {
		t.Fatal(err)
	}
	j, err := jobs.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 135; n++ {
		if err = jobs.ReportJob(ctx, j, analytics.LiveEvent{Stage: "usage", Usage: &analytics.AIUsage{Model: "model", Provider: "provider", TotalTokens: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = jobs.ReportJob(ctx, j, analytics.LiveEvent{Stage: "dump_prompt", Model: "PRIVATE_SECRET"}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Monitor(ctx, user, false, repository.MonitorFilters{})
	if err != nil || got.Tokens != 135 || got.Recent[0].TraceCount != 135 || len(got.Recent[0].Events) != 128 {
		t.Fatal("bounded trace lost counters", got, err)
	}
	if _, err = db.Exec(ctx, `UPDATE nudge_slack_jobs SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Monitor(ctx, user, false, repository.MonitorFilters{})
	if err != nil || !got.Recent[0].Stalled {
		t.Fatal("expired worker not visible", err)
	}
	workspaceStore, _ := NewWorkspaceStore(ctx, db)
	exported, exportErr := workspaceStore.ExportPersonalData(ctx, user)
	if exportErr != nil || !strings.Contains(string(exported), "live_operations") || !strings.Contains(string(exported), "model_usage") || strings.Contains(string(exported), "PRIVATE_QUEUED_MESSAGE") {
		t.Fatal("live export missing or unsafe", exportErr)
	}
	if err = workspaceStore.DeletePersonalData(ctx, user); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM nudge_job_model_usage`).Scan(&count); err != nil || count != 0 {
		t.Fatal("orphaned private usage", count, err)
	}
}
