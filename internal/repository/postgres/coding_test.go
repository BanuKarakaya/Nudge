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
	c := coding.Connection{UserID: 1, Team: "T", GitHubUserID: 42, Login: "test", Credentials: coding.Credentials{Access: "ghu_testsecret", Refresh: "ghr_testsecret"}}
	if err := s.SaveConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	if team, err := s.SlackTeam(ctx, 1); err != nil || team != "T" {
		t.Fatalf("Slack mapping: %q %v", team, err)
	}
	wrong := c
	wrong.Team = "OTHER"
	wrong.Credentials.Access = "ghu_wrong"
	if err := s.SaveConnection(ctx, wrong); !errors.Is(err, coding.ErrDenied) {
		t.Fatalf("cross-workspace connection write accepted: %v", err)
	}
	// One Slack user in two installations must have separate GitHub connections.
	if _, err := db.Exec(ctx, `INSERT INTO slack_installations(team_id,team_name,bot_token) VALUES('T2','second','test'); INSERT INTO users(id,team_id,slack_user_id) VALUES(3,'T2','U1')`); err != nil {
		t.Fatal(err)
	}
	other := c
	other.UserID = 3
	other.Team = "T2"
	other.Credentials.Access = "ghu_other_workspace"
	if err := s.SaveConnection(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := s.WithConnection(ctx, 3, func(got *coding.Connection) error {
		if got.Team != "T2" || got.Credentials.Access != other.Credentials.Access {
			return errors.New("workspace credentials mixed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	provider := &workspaceRepoProvider{}
	service, err := coding.New(s, provider)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Repositories(ctx, coding.Identity{UserID: 1, Team: "T2"}); !errors.Is(err, coding.ErrDenied) {
		t.Fatalf("wrong Slack workspace accepted: %v", err)
	}
	if provider.calls != 0 {
		t.Fatal("foreign workspace reached GitHub")
	}
	for _, i := range []coding.Identity{{UserID: 1, Team: "T"}, {UserID: 3, Team: "T2"}} {
		repos, err := service.Repositories(ctx, i)
		if err != nil || len(repos) != 1 {
			t.Fatalf("mapped workspace failed: %v", err)
		}
		want := "owner/first"
		if i.UserID == 3 {
			want = "owner/second"
		}
		if repos[0].FullName != want {
			t.Fatalf("wrong repo access for user %d: %v", i.UserID, repos)
		}
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
				if got.GitHubUserID != 42 || got.Team != "T" {
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
	// Two connected people in the same Slack workspace must retain separate
	// encrypted credentials and immutable GitHub identities.
	second := coding.Connection{UserID: 2, Team: "T", GitHubUserID: 66, Login: "second", Credentials: coding.Credentials{Access: "ghu_second_user"}}
	if err := s.SaveConnection(ctx, second); err != nil {
		t.Fatal(err)
	}
	bindings, err := s.ConnectionBindings(ctx)
	if err != nil || len(bindings) != 3 || bindings[1].UserID != 2 || bindings[1].SlackUserID != "U2" || bindings[1].Account.ID != 66 {
		t.Fatalf("incorrect diagnostic binding: %+v %v", bindings, err)
	}
	access, err := service.RepositoryAccess(ctx, coding.Identity{UserID: 2, Team: "T"})
	if err != nil || access.Account.ID != 66 || len(access.Repositories) != 1 || access.Repositories[0].FullName != "second/private" {
		t.Fatalf("same-workspace users mixed: %+v %v", access, err)
	}
	// Simulate a swapped ciphertext: valid encryption is insufficient proof of
	// ownership. The live token subject must match before any repo API request.
	if _, err := db.Exec(ctx, `UPDATE coding_connections SET credentials=(SELECT credentials FROM coding_connections WHERE user_id=1) WHERE user_id=2`); err != nil {
		t.Fatal(err)
	}
	callsBefore := provider.calls
	if _, err := service.Repositories(ctx, coding.Identity{UserID: 2, Team: "T"}); !errors.Is(err, coding.ErrConnect) || provider.calls != callsBefore {
		t.Fatalf("swapped encrypted credentials reached repositories: %v", err)
	}
	if err := s.SaveConnection(ctx, second); err != nil {
		t.Fatal(err)
	}
	state := coding.OAuthState{UserID: 1, Verifier: "pkce-secret", Phase: "oauth", InstallationID: 123, Expires: time.Now().Add(time.Minute)}
	if err := s.PutOAuthState(ctx, "hash", state); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT verifier FROM coding_oauth_states WHERE state_hash='hash'`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, state.Verifier) {
		t.Fatal("PKCE plaintext")
	}
	if got, err := s.ConsumeOAuthState(ctx, "hash"); err != nil || got.Verifier != state.Verifier || got.Phase != "oauth" || got.InstallationID != 123 {
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

	t.Run("durable edits", func(t *testing.T) {
		job := coding.Edit{Task: coding.Task{ID: strings.Repeat("d", 32), Identity: identity, Repository: first.Repository, Instruction: "original signed request", Status: "editing"}, AccountID: 42, Snapshot: coding.Snapshot{Files: []coding.RepositoryFile{{Path: "private.go", Mode: "100644", Content: []byte("private source bytes")}}}}
		created, err := s.CreateEdit(ctx, job)
		if err != nil {
			t.Fatal(err)
		}
		job.Task.ID = strings.Repeat("e", 32)
		job.Task.Instruction = "retry attempted overwrite"
		retry, err := s.CreateEdit(ctx, job)
		if err != nil || retry.Task.ID != created.Task.ID || retry.Task.Instruction != "original signed request" {
			t.Fatal("edit dedup failed", err)
		}
		var encrypted string
		if err := db.QueryRow(ctx, `SELECT payload FROM coding_edits WHERE id=$1`, created.Task.ID).Scan(&encrypted); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(encrypted, "private source") || strings.Contains(encrypted, "original signed") {
			t.Fatal("plaintext edit in database")
		}
		if _, err := s.GetEdit(ctx, 2, created.Task.ID); !errors.Is(err, coding.ErrNotFound) {
			t.Fatal("cross-user edit read")
		}
		if err := s.WithEdit(ctx, 2, created.Task.ID, func(*coding.Edit) error { t.Fatal("cross-user edit lock"); return nil }); !errors.Is(err, coding.ErrNotFound) {
			t.Fatal("cross-user edit mutation")
		}
		if err := s.WithEdit(ctx, 1, created.Task.ID, func(e *coding.Edit) error { e.Task.Status = "pushed"; return errors.New("abort") }); err == nil {
			t.Fatal("expected rollback")
		}
		var editsWG sync.WaitGroup
		for range 5 {
			editsWG.Add(1)
			go func() {
				defer editsWG.Done()
				if err := s.WithEdit(ctx, 1, created.Task.ID, func(e *coding.Edit) error { e.Review.Summary += "x"; return nil }); err != nil {
					t.Error(err)
				}
			}()
		}
		editsWG.Wait()
		// Reconstruct the store as after a Railway restart.
		restarted, err := NewCodingStore(ctx, db, cipher)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := restarted.GetEdit(ctx, 1, created.Task.ID)
		if err != nil || restored.Task.Status != "editing" || restored.Review.Summary != "xxxxx" || restored.Task.Identity != identity || string(restored.Snapshot.Files[0].Content) != "private source bytes" {
			t.Fatal("edit restart/locking failed", err)
		}
	})
	state.Expires = time.Now().Add(time.Minute)
	if err := s.PutOAuthState(ctx, "pending", state); err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect(ctx, 1); err != nil {
		t.Fatal(err)
	}

	if e, err := s.GetEdit(ctx, 1, strings.Repeat("d", 32)); err != nil || e.Task.Status != "cancelled" {
		t.Fatal("disconnect retained pending edit", err)
	}
	if err := s.WithConnection(ctx, 1, func(*coding.Connection) error { return nil }); !errors.Is(err, coding.ErrConnect) {
		t.Fatal("disconnect retained credentials")
	}
	if err := s.WithConnection(ctx, 3, func(*coding.Connection) error { return nil }); err != nil {
		t.Fatal("disconnect affected another workspace")
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

type workspaceRepoProvider struct {
	coding.Provider
	calls int
}

func (*workspaceRepoProvider) User(_ context.Context, token string) (int64, string, error) {
	if token == "ghu_second_user" {
		return 66, "second", nil
	}
	return 42, "test", nil
}

func (p *workspaceRepoProvider) Repositories(_ context.Context, token string) ([]coding.Repository, error) {
	p.calls++
	name := "owner/first"
	if token == "ghu_other_workspace" {
		name = "owner/second"
	}
	if token == "ghu_second_user" {
		name = "second/private"
	}
	return []coding.Repository{{ID: 10, InstallationID: 20, FullName: name, DefaultBranch: "main"}}, nil
}
