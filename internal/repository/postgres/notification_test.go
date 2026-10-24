package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/repository"
)

func TestNotificationDeliveryPostgres(t *testing.T) {
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
	schema := fmt.Sprintf("nudge_test_%d", time.Now().UnixNano())
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
	initial, err := os.ReadFile("../../../migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, strings.Split(string(initial), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO slack_installations(team_id,team_name,bot_token) VALUES('T','test','test'); INSERT INTO users(id,team_id,slack_user_id) VALUES(1,'T','U'); INSERT INTO notifications(user_id,notification_type,period_key) VALUES(1,'daily_feedbin','old')`); err != nil {
		t.Fatal(err)
	}
	q, err := NewNotificationRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	// Migration is rerunnable, and historical sent rows must not be replayed.
	if _, err := NewNotificationRepository(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ClaimNext(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("historical row replayed: %v", err)
	}
	msg := repository.NotificationMessage{Channel: "D", Text: "hello"}
	for _, period := range []string{"one", "one", "two"} {
		if err := q.Enqueue(ctx, 1, "test", period, msg); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan repository.NotificationDelivery, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); d, e := q.ClaimNext(ctx); results <- d; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var claimed []repository.NotificationDelivery
	for d := range results {
		claimed = append(claimed, d)
	}
	if claimed[0].ID == claimed[1].ID {
		t.Fatal("concurrent workers claimed the same message")
	}
	if _, err := q.ClaimNext(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("in-flight or duplicate message claimed")
	}
	first := claimed[0]
	second := claimed[1]
	if err := q.Complete(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := q.Retry(ctx, second, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ClaimNext(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("backoff ignored")
	}
	if _, err := db.Exec(ctx, `UPDATE notifications SET available_at=NOW()-INTERVAL '1 second' WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	retried, err := q.ClaimNext(ctx)
	if err != nil || retried.ID != second.ID || retried.Attempts != 2 {
		t.Fatalf("retry=%+v error=%v", retried, err)
	}
	// Simulate a crash. Another repository instance recovers the expired lease.
	if _, err := db.Exec(ctx, `UPDATE notifications SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, retried.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewNotificationRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.ClaimNext(ctx)
	if err != nil || recovered.ID != second.ID || recovered.ClaimToken == retried.ClaimToken {
		t.Fatalf("recovery=%+v error=%v", recovered, err)
	}
	if err := q.Complete(ctx, retried); err == nil {
		t.Fatal("stale worker completed new lease")
	}
	if err := q.Retry(ctx, retried, time.Second); err == nil {
		t.Fatal("stale worker rescheduled new lease")
	}
	if err := restarted.Complete(ctx, recovered); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ClaimNext(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("sent message replayed")
	}
	var total, sent int
	if err := db.QueryRow(ctx, `SELECT COUNT(*),COUNT(*) FILTER(WHERE sent_at IS NOT NULL AND delivery_status='sent') FROM notifications`).Scan(&total, &sent); err != nil {
		t.Fatal(err)
	}
	if total != 3 || sent != 3 {
		t.Fatalf("rows=%d sent=%d", total, sent)
	}
	// Typed onboarding deliveries need no channel or pre-generated bearer link.
	for _, message := range []repository.NotificationMessage{{Connection: "github"}, {WorkspaceOnboarding: true}} {
		if err := q.Enqueue(ctx, 1, "onboarding", fmt.Sprint(message), message); err != nil {
			t.Fatal(err)
		}
		got, err := q.ClaimNext(ctx)
		if err != nil || got.Message != message {
			t.Fatalf("typed payload roundtrip: %+v %v", got, err)
		}
		if err := q.Complete(ctx, got); err != nil {
			t.Fatal(err)
		}
	}
	for _, message := range []repository.NotificationMessage{{Connection: "unknown"}, {Connection: "github", WorkspaceOnboarding: true}, {Connection: "github", Channel: "public", Text: "secret"}} {
		if err := q.Enqueue(ctx, 1, "invalid", "key", message); err == nil {
			t.Fatal("accepted invalid typed payload")
		}
	}
}
