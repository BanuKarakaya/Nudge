package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"nudge/internal/repository"
	"nudge/internal/scheduler"
)

// Real PostgreSQL + production worker. Synthetic jobs exercise ordering,
// retries, tenant separation and delivery checkpoints; no paid AI or Slack.
func TestQueueConcurrentLoadAndRetryRecovery(t *testing.T) {
	db := reviewDB(t, 12)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	q, err := NewSlackJobRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	const conversations, perConversation, workers = 20, 20, 8
	for c := 0; c < conversations; c++ {
		for n := 0; n < perConversation; n++ {
			job := repository.SlackJob{Key: repository.SlackJobKey{Team: fmt.Sprintf("T%d", c%2), Channel: "C", Thread: fmt.Sprint(c / 2), User: "U"}, EventKey: fmt.Sprintf("%d-%d", c, n), Payload: json.RawMessage(fmt.Sprintf(`{"sequence":%d}`, n))}
			// Slack can redeliver an event; both inserts must still yield one job.
			for repeat := 0; repeat < 2; repeat++ {
				if err := q.Enqueue(ctx, job); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	var mu sync.Mutex
	next := map[string]int{}
	seen := map[int64]bool{}
	retried := map[int64]bool{}
	completed := 0
	w := scheduler.SlackJobWorker{Jobs: q, Process: func(ctx context.Context, j repository.SlackJob) error {
		var payload struct {
			Sequence int `json:"sequence"`
		}
		if err := json.Unmarshal(j.Payload, &payload); err != nil {
			return err
		}
		key := j.Key.Team + "/" + j.Key.Thread
		mu.Lock()
		if next[key] != payload.Sequence || seen[j.ID] {
			mu.Unlock()
			return fmt.Errorf("duplicate or out-of-order job %d", j.ID)
		}
		// Save once, then simulate a failed delivery. The retry must use that result.
		fail := payload.Sequence%7 == 0 && !retried[j.ID]
		if fail {
			retried[j.ID] = true
		}
		mu.Unlock()
		if len(j.Result) == 0 {
			if err := q.SaveResult(ctx, j, json.RawMessage(`{"answer":"saved"}`)); err != nil {
				return err
			}
		} else if j.Attempts != 2 {
			return fmt.Errorf("unexpected checkpoint attempt %d", j.Attempts)
		}
		if fail {
			// Immediate retry keeps CI bounded; worker error/backoff behavior is tested separately.
			return q.Retry(ctx, j, 0)
		}
		if err := q.MarkDelivered(ctx, j); err != nil {
			return err
		}
		record := repository.InteractionRecord{RequestID: fmt.Sprintf("slack-job-%d", j.ID), TeamID: j.Key.Team, ChannelID: j.Key.Channel, ThreadTS: j.Key.Thread, SlackUserID: j.Key.User, Status: "success"}
		// Hold the test ordering lock across terminal commit, preventing a new worker
		// from observing the next eligible row before the test's observation advances.
		mu.Lock()
		defer mu.Unlock()
		if err := q.Complete(ctx, j, json.RawMessage(`{"turns":[]}`), record, true); err != nil {
			return err
		}
		next[key]++
		seen[j.ID] = true
		completed++
		return nil
	}}
	failures := make(chan error, workers)
	var wg sync.WaitGroup
	started := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				mu.Lock()
				done := completed == conversations*perConversation
				mu.Unlock()
				if done {
					return
				}
				worked, err := w.ProcessNext(ctx)
				if err != nil {
					failures <- err
					cancel()
					return
				}
				if !worked {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Millisecond):
					}
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if completed != conversations*perConversation || len(retried) != conversations*3 {
		t.Fatalf("completed=%d retries=%d", completed, len(retried))
	}
	var jobs, records int
	if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM nudge_slack_jobs WHERE status='completed'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM nudge_ai_interactions`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if jobs != 400 || records != 400 {
		t.Fatalf("jobs=%d analytics=%d", jobs, records)
	}
	if _, err := q.ClaimNext(context.Background()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("remaining claim: %v", err)
	}
	t.Logf("400 jobs, 800 ingress inserts, 20 tenant-scoped conversations, 8 workers, 60 delivery retries; elapsed=%s", time.Since(started))
}

func TestQueueReconnectAfterDatabaseConnectionLoss(t *testing.T) {
	db := reviewDB(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	q, err := NewSlackJobRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Enqueue(ctx, repository.SlackJob{Key: repository.SlackJobKey{Team: "T", Channel: "C", Thread: "root", User: "U"}, EventKey: "E", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	first, err := q.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.SaveResult(ctx, first, json.RawMessage(`{"answer":"survived"}`)); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Terminate only the connection owned by this isolated test schema.
	pid := conn.Conn().PgConn().PID()
	if _, err = db.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	if err = conn.Conn().Ping(ctx); err == nil {
		conn.Release()
		t.Fatal("connection did not fail")
	}
	conn.Release()
	if err = db.Ping(ctx); err != nil {
		t.Fatal("pool failed to reconnect", err)
	}
	if _, err = db.Exec(ctx, `UPDATE nudge_slack_jobs SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewSlackJobRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.ClaimNext(ctx)
	if err != nil || recovered.ID != first.ID || recovered.ClaimToken == first.ClaimToken || string(recovered.Result) != `{"answer": "survived"}` {
		t.Fatalf("lost checkpoint: %+v %v", recovered, err)
	}
	if err = q.MarkDelivered(ctx, first); !errors.Is(err, repository.ErrSlackJobLeaseLost) {
		t.Fatal("stale owner retained access", err)
	}
	if err = restarted.MarkDelivered(ctx, recovered); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Complete(ctx, recovered, json.RawMessage(`{"turns":[]}`), repository.InteractionRecord{RequestID: "reconnected", TeamID: "T", SlackUserID: "U", Status: "success"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.ClaimNext(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("completed job reappeared", err)
	}
}
