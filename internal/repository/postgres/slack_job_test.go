package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/repository"
)

func TestSlackJobsPostgres(t *testing.T) {
	dsn := os.Getenv("NUDGE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set NUDGE_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("nudgy_jobs_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	history, err := NewConversationRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	_ = history
	if _, err := NewAnalyticsRepository(ctx, db); err != nil {
		t.Fatal(err)
	}
	q, err := NewSlackJobRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSlackJobRepository(ctx, db); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	enqueue := func(team, event, thread string) {
		t.Helper()
		err := q.Enqueue(ctx, repository.SlackJob{Key: repository.SlackJobKey{Team: team, Channel: "C", Thread: thread, User: "U"}, EventKey: event, Payload: json.RawMessage(`{"text":"original"}`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	assertEmpty := func() {
		t.Helper()
		if _, err := q.ClaimNext(ctx); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("unexpected claim: %v", err)
		}
	}
	expire := func(j repository.SlackJob) {
		t.Helper()
		if _, err := db.Exec(ctx, `UPDATE nudge_slack_jobs SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, j.ID); err != nil {
			t.Fatal(err)
		}
	}
	due := func(j repository.SlackJob) {
		t.Helper()
		if _, err := db.Exec(ctx, `UPDATE nudge_slack_jobs SET available_at=NOW()-INTERVAL '1 second' WHERE id=$1`, j.ID); err != nil {
			t.Fatal(err)
		}
	}
	record := func(j repository.SlackJob) repository.InteractionRecord {
		return repository.InteractionRecord{RequestID: fmt.Sprintf("slack-job-%d", j.ID), TeamID: j.Key.Team, ChannelID: j.Key.Channel, ThreadTS: j.Key.Thread, SlackUserID: j.Key.User, RequestText: "request", ResponseText: "reply", Status: "success"}
	}
	finish := func(j repository.SlackJob, success bool) {
		t.Helper()
		if len(j.Result) == 0 {
			if err := q.SaveResult(ctx, j, json.RawMessage(`{"answer":"saved"}`)); err != nil {
				t.Fatal(err)
			}
		}
		if err := q.MarkDelivered(ctx, j); err != nil {
			t.Fatal(err)
		}
		if err := q.Complete(ctx, j, json.RawMessage(`{"turns":[{"request":"request","reply":"reply"}]}`), record(j), success); err != nil {
			t.Fatal(err)
		}
	}
	enqueue("T", "E1", "root")
	enqueue("T", "E1", "root")
	enqueue("T", "E2", "root")
	enqueue("T", "E3", "other")
	enqueue("T2", "E1", "root")
	// Duplicate payloads never overwrite the signed request accepted first.
	if err := q.Enqueue(ctx, repository.SlackJob{Key: repository.SlackJobKey{Team: "T", Channel: "C", Thread: "root", User: "U"}, EventKey: "E1", Payload: json.RawMessage(`{"text":"changed"}`)}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM nudge_slack_jobs`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("dedup count=%d err=%v", count, err)
	}
	// Concurrent workers get only distinct, unrelated conversations.
	var wg sync.WaitGroup
	results := make(chan repository.SlackJob, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, err := q.ClaimNext(ctx)
			if err == nil {
				results <- j
			} else if !errors.Is(err, pgx.ErrNoRows) {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	claimed := map[int64]bool{}
	var first repository.SlackJob
	for j := range results {
		if claimed[j.ID] || (j.Key.Team == "T" && j.EventKey == "E2") {
			t.Fatal("duplicate or concurrent same-conversation claim")
		}
		claimed[j.ID] = true
		if j.Key.Team == "T" && j.EventKey == "E1" {
			first = j
		} else {
			finish(j, true)
		}
	}
	if len(claimed) != 3 || first.ID == 0 || string(first.Payload) != `{"text": "original"}` {
		t.Fatalf("claims=%d payload=%s", len(claimed), first.Payload)
	}
	assertEmpty()
	if err := q.Retry(ctx, first, time.Hour); err != nil {
		t.Fatal(err)
	}
	assertEmpty() // E2 cannot overtake E1 while E1 backs off.
	due(first)
	first, err = q.ClaimNext(ctx)
	if err != nil || first.Attempts != 2 {
		t.Fatalf("retry: %+v %v", first, err)
	}
	saved := json.RawMessage(`{"answer":"do not regenerate"}`)
	if err := q.SaveResult(ctx, first, saved); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveResult(ctx, first, json.RawMessage(`{}`)); err == nil {
		t.Fatal("immutable response overwritten")
	}
	expire(first)
	// Expiry alone fences the old worker, even before somebody reclaims the job.
	if err := q.CheckLease(ctx, first); !errors.Is(err, repository.ErrSlackJobLeaseLost) {
		t.Fatalf("expired ownership=%v", err)
	}
	if err := q.MarkDelivered(ctx, first); !errors.Is(err, repository.ErrSlackJobLeaseLost) {
		t.Fatal("expired worker marked delivery")
	}
	restarted, err := NewSlackJobRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.ClaimNext(ctx)
	if err != nil || recovered.ID != first.ID || recovered.ClaimToken == first.ClaimToken || recovered.Attempts != 3 || len(recovered.Result) == 0 {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
	if err := q.Retry(ctx, first, 0); !errors.Is(err, repository.ErrSlackJobLeaseLost) {
		t.Fatal("stale worker changed new claim")
	}
	if err := q.Complete(ctx, first, nil, record(first), true); !errors.Is(err, repository.ErrSlackJobLeaseLost) {
		t.Fatal("stale completion accepted")
	}
	if err := q.SaveResult(ctx, first, json.RawMessage(`{}`)); !errors.Is(err, repository.ErrSlackJobLeaseLost) {
		t.Fatal("stale result accepted")
	}
	if err := q.Complete(ctx, recovered, nil, record(recovered), true); err == nil {
		t.Fatal("completed before delivery")
	}
	if err := q.MarkDelivered(ctx, recovered); err != nil {
		t.Fatal(err)
	}
	// Crash after recording delivery: only finalization is resumed.
	expire(recovered)
	delivered, err := restarted.ClaimNext(ctx)
	if err != nil || !delivered.Delivered || len(delivered.Result) == 0 {
		t.Fatalf("lost delivery checkpoint: %+v %v", delivered, err)
	}
	// Failure in analytics must roll back history and terminal status together.
	if _, err := db.Exec(ctx, `ALTER TABLE nudge_ai_interactions ADD CONSTRAINT test_reject_job CHECK(request_id <> 'slack-job-1')`); err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(ctx, delivered, json.RawMessage(`{"turns":[]}`), record(delivered), true); err == nil {
		t.Fatal("expected transaction failure")
	}
	if err := q.CheckLease(ctx, delivered); err != nil {
		t.Fatal("completion failed to roll back", err)
	}
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM nudge_conversations WHERE team_id='T' AND thread_ts='root'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial history committed", err)
	}
	if _, err := db.Exec(ctx, `ALTER TABLE nudge_ai_interactions DROP CONSTRAINT test_reject_job`); err != nil {
		t.Fatal(err)
	}
	finish(delivered, true)
	next, err := q.ClaimNext(ctx)
	if err != nil || next.EventKey != "E2" {
		t.Fatalf("next conversation request not released: %+v %v", next, err)
	}
	finish(next, false)
	enqueue("T", "E1", "root") // Completed events remain deduplicated after restart.
	assertEmpty()
	var completed, failed int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FILTER(WHERE status='completed'),COUNT(*) FILTER(WHERE status='failed') FROM nudge_slack_jobs`).Scan(&completed, &failed); err != nil || completed != 3 || failed != 1 {
		t.Fatalf("completed=%d failed=%d err=%v", completed, failed, err)
	}
}
