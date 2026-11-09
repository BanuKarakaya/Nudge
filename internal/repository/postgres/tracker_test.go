package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/secure"
	"nudge/internal/tracker"
)

func TestTrackerStorePostgres(t *testing.T) {
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
	schema := pgx.Identifier{fmt.Sprintf("tracker_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	initial, err := os.ReadFile("../../../migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, strings.Split(string(initial), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO slack_installations(team_id,team_name,bot_token) VALUES('T','test','test'); INSERT INTO users(id,team_id,slack_user_id) VALUES(1,'T','U1'),(2,'T','U2')`); err != nil {
		t.Fatal(err)
	}
	cipher, _ := secure.NewCipherFromBase64(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	store, err := NewTrackerStore(ctx, db, cipher)
	if err != nil {
		t.Fatal(err)
	}
	c := tracker.Connection{Identity: tracker.Identity{UserID: 1, Team: "T"}, Provider: "jira", Account: tracker.Account{ID: "account"}, Credentials: tracker.Credentials{Access: "access-secret", Refresh: "refresh-secret"}, Selected: []string{"cloud:10"}}
	for _, p := range []string{"jira", "trello"} {
		c.Provider = p
		if err = store.Save(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err = store.PutState(ctx, p, "same-hash", tracker.State{UserID: 1, Phase: "select", Pending: &c, Expires: time.Now().Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	var raw string
	if err = db.QueryRow(ctx, `SELECT encrypted FROM tracker_connections WHERE provider='jira' AND user_id=1`).Scan(&raw); err != nil || strings.Contains(raw, "secret") || strings.Contains(raw, "cloud:10") {
		t.Fatal("connection unencrypted", err)
	}
	if err = db.QueryRow(ctx, `SELECT encrypted FROM tracker_oauth_states WHERE provider='jira'`).Scan(&raw); err != nil || strings.Contains(raw, "secret") {
		t.Fatal("pending token unencrypted", err)
	}
	restarted, err := NewTrackerStore(ctx, db, cipher)
	if err != nil {
		t.Fatal("migration/restart", err)
	}
	if err = restarted.WithConnection(ctx, "jira", 1, func(c *tracker.Connection) error {
		if c.Credentials.Access != "access-secret" || c.Selected[0] != "cloud:10" {
			t.Fatal("round-trip failed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var consumed atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := restarted.ConsumeState(ctx, "jira", "same-hash"); err == nil {
				consumed.Add(1)
			}
		}()
	}
	wg.Wait()
	if consumed.Load() != 1 {
		t.Fatal("state not atomic")
	}
	if state, err := restarted.ConsumeState(ctx, "trello", "same-hash"); err != nil || state.Pending.Provider != "trello" {
		t.Fatal("provider state isolation", err)
	}
	var rotated atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := restarted.WithConnection(ctx, "jira", 1, func(c *tracker.Connection) error {
				if c.Credentials.Refresh == "refresh-secret" {
					rotated.Add(1)
					c.Credentials.Refresh = "rotated"
				}
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if rotated.Load() != 1 {
		t.Fatal("rotation not serialized")
	}
	if err = restarted.WithConnection(ctx, "jira", 2, func(*tracker.Connection) error { t.Fatal("cross-user access"); return nil }); !errors.Is(err, tracker.ErrConnect) {
		t.Fatal(err)
	}
	c.Provider = "jira"
	c.Team = "OTHER"
	if err = store.Save(ctx, c); !errors.Is(err, tracker.ErrDenied) {
		t.Fatal("workspace mismatch saved", err)
	}
	if err = store.PutState(ctx, "jira", "pending", tracker.State{UserID: 1, Expires: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Disconnect(ctx, "jira", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.ConsumeState(ctx, "jira", "pending"); !errors.Is(err, tracker.ErrConnect) {
		t.Fatal("pending state survived disconnect")
	}
	if err = restarted.WithConnection(ctx, "trello", 1, func(*tracker.Connection) error { return nil }); err != nil {
		t.Fatal("disconnect affected Trello", err)
	}
}
