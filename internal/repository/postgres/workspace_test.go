package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/domain"
	"nudge/internal/workspace"
)

func TestWorkspaceStorePostgres(t *testing.T) {
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
	schema := pgx.Identifier{fmt.Sprintf("workspace_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
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
	if _, err = db.Exec(ctx, strings.Split(string(initial), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO slack_installations(team_id,team_name,bot_token) VALUES('legacy','Existing','old');INSERT INTO users(team_id,slack_user_id) VALUES('legacy','U-old')`); err != nil {
		t.Fatal(err)
	}
	s, err := NewWorkspaceStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewWorkspaceStore(ctx, db); err != nil {
		t.Fatal("migration not repeatable", err)
	}
	installs := NewInstallationRepository(db)
	old, err := installs.GetUserIDBySlackUserID(ctx, "legacy", "U-old")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Member(ctx, old)
	if err != nil || m.Manager || !m.OwnerMissing {
		t.Fatalf("guessed historical owner: %+v %v", m, err)
	}
	owner, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "legacy", TeamName: "Existing", BotToken: "new", SlackUserID: "OWNER"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "legacy", TeamName: "Existing", BotToken: "rotated", SlackUserID: "MEMBER"})
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.Member(ctx, owner)
	if err != nil || !m.Manager {
		t.Fatal("installer did not become owner", m, err)
	}
	m, err = s.Member(ctx, member)
	if err != nil || m.Manager {
		t.Fatal("reinstall stole ownership", m, err)
	}
	other, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "second", TeamName: "Individual", BotToken: "bot", SlackUserID: "OWNER"})
	if err != nil {
		t.Fatal(err)
	}
	scope := workspace.Scope{Enabled: true, Resources: []workspace.Resource{{ID: "10", Name: "Team repo"}}}
	if err = s.SaveScope(ctx, member, "github", scope); err == nil {
		t.Fatal("member wrote workspace scope")
	}
	if err = s.SaveScope(ctx, owner, "github", scope); err != nil {
		t.Fatal(err)
	}
	got, err := s.Scope(ctx, member, "github")
	if err != nil || !got.Allows("10") || got.Manager {
		t.Fatal("member scope", got, err)
	}
	got, err = s.Scope(ctx, other, "github")
	if err != nil || got.Enabled || !got.Manager {
		t.Fatal("tenant isolation", got, err)
	}
	got, err = s.Scope(ctx, owner, "trello")
	if err != nil || got.Enabled {
		t.Fatal("provider isolation", got, err)
	}
	if err = s.SaveScope(ctx, owner, "github", workspace.Scope{Enabled: true}); err == nil {
		t.Fatal("enabled empty scope")
	}
	if err = s.SaveScope(ctx, owner, "unknown", scope); err == nil {
		t.Fatal("unknown provider")
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if s.CreateSession(ctx, member, fmt.Sprintf("session-%d", n), time.Now().Add(time.Hour), "one-link") == nil {
				winners.Add(1)
			}
		}(n)
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("replay winners: %d", winners.Load())
	}
	if err = s.CreateSession(ctx, member, "valid", time.Now().Add(time.Hour), ""); err != nil {
		t.Fatal(err)
	}
	if id, e := s.SessionUser(ctx, "valid"); e != nil || id != member {
		t.Fatal("session identity", id, e)
	}
	if err = s.DeleteSession(ctx, "valid"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SessionUser(ctx, "valid"); err == nil {
		t.Fatal("revoked session accepted")
	}
	if err = s.CreateSession(ctx, member, "expired", time.Now().Add(-time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SessionUser(ctx, "expired"); err == nil {
		t.Fatal("expired session accepted")
	}
}
