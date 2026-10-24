package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/coding"
	"nudge/internal/secure"
)

func TestCodingStorePostgres(t *testing.T) {
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
	schema := pgx.Identifier{fmt.Sprintf("coding_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err := db.Exec(ctx, strings.Split(string(initial), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO slack_installations(team_id,team_name,bot_token) VALUES('T','test','test'); INSERT INTO users(id,team_id,slack_user_id) VALUES(1,'T','U1'),(2,'T','U2')`); err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipherFromBase64(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewCodingStore(ctx, db, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCodingStore(ctx, db, cipher); err != nil {
		t.Fatal(err)
	}
	c := coding.Connection{UserID: 1, GitHubUserID: 42, Login: "test", Credentials: coding.Credentials{Access: "ghu_testsecret", Refresh: "ghr_testsecret"}}
	if err := s.SaveConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	var encrypted string
	if err := db.QueryRow(ctx, `SELECT credentials FROM coding_connections WHERE user_id=1`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "testsecret") || !strings.HasPrefix(encrypted, "v1:") {
		t.Fatal("credentials not encrypted")
	}
	// Concurrent refresh contenders must see the committed replacement token.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var refreshes int
	for n := 0; n < 5; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.WithConnection(ctx, 1, func(got *coding.Connection) error {
				if got.GitHubUserID != 42 {
					return errors.New("wrong account")
				}
				if got.Credentials.Access == c.Credentials.Access {
					mu.Lock()
					refreshes++
					mu.Unlock()
					got.Credentials.Access = "ghu_refreshed"
				}
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if refreshes != 1 {
		t.Fatalf("refreshes=%d", refreshes)
	}
	if err := s.WithConnection(ctx, 2, func(*coding.Connection) error { t.Error("other user credential exposed"); return nil }); !errors.Is(err, coding.ErrConnect) {
		t.Fatal(err)
	}
	state := coding.OAuthState{UserID: 1, Verifier: "pkce-secret", Expires: time.Now().Add(time.Minute)}
	if err := s.PutOAuthState(ctx, "hash", state); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT verifier FROM coding_oauth_states WHERE state_hash='hash'`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, state.Verifier) {
		t.Fatal("PKCE plaintext")
	}
	if got, err := s.ConsumeOAuthState(ctx, "hash"); err != nil || got.Verifier != state.Verifier {
		t.Fatalf("state roundtrip: %v", err)
	}
	if _, err := s.ConsumeOAuthState(ctx, "hash"); !errors.Is(err, coding.ErrConnect) {
		t.Fatal("state replay allowed")
	}
	state.Expires = time.Now().Add(-time.Minute)
	if err := s.PutOAuthState(ctx, "expired", state); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeOAuthState(ctx, "expired"); !errors.Is(err, coding.ErrConnect) {
		t.Fatal("expired state accepted")
	}
	identity := coding.Identity{UserID: 1, Team: "T", Channel: "C", Thread: "thread", Event: "event"}
	draft := coding.Task{ID: strings.Repeat("a", 32), Identity: identity, Provider: "github", Repository: coding.Repository{ID: 1, FullName: "owner/repo", DefaultBranch: "main", InstallationID: 3}, Instruction: "original request", CreatedAt: time.Now()}
	first, err := s.CreateDraft(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	draft.ID = strings.Repeat("b", 32)
	draft.Instruction = "changed by retry"
	for n := 0; n < 5; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.CreateDraft(ctx, draft)
			if err != nil || got.ID != first.ID || got.Instruction != first.Instruction {
				t.Errorf("retry changed draft: %+v %v", got, err)
			}
		}()
	}
	wg.Wait()
	for _, other := range []coding.Identity{{UserID: 2, Team: "T", Channel: "C", Thread: "thread"}, {UserID: 1, Team: "other", Channel: "C", Thread: "thread"}, {UserID: 1, Team: "T", Channel: "other", Thread: "thread"}, {UserID: 1, Team: "T", Channel: "C", Thread: "other"}} {
		if _, err := s.Task(ctx, other, first.ID); !errors.Is(err, coding.ErrNotFound) {
			t.Fatal("cross-context read allowed")
		}
		if _, err := s.CancelDraft(ctx, other, first.ID); !errors.Is(err, coding.ErrNotFound) {
			t.Fatal("cross-context cancellation allowed")
		}
	}
	state.Expires = time.Now().Add(time.Minute)
	if err := s.PutOAuthState(ctx, "pending", state); err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.WithConnection(ctx, 1, func(*coding.Connection) error { return nil }); !errors.Is(err, coding.ErrConnect) {
		t.Fatal("disconnect retained credentials")
	}
	if _, err := s.ConsumeOAuthState(ctx, "pending"); !errors.Is(err, coding.ErrConnect) {
		t.Fatal("disconnect retained OAuth state")
	}
	if got, err := s.Task(ctx, identity, first.ID); err != nil || got.Status != "cancelled" {
		t.Fatalf("disconnect did not cancel: %+v %v", got, err)
	}
	if got, err := s.CancelDraft(ctx, identity, first.ID); err != nil || got.Status != "cancelled" {
		t.Fatal("repeat cancel failed")
	}
}
