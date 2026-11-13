package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/coding"
	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/secure"
	"nudge/internal/service"
	"nudge/migrations"
)

func reviewDB(t *testing.T, max int32) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("NUDGE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("review_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = max
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); admin.Close() })
	if err = migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}
func reviewCipher(t *testing.T) secure.Cipher {
	t.Helper()
	c, e := secure.NewCipherFromBase64(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if e != nil {
		t.Fatal(e)
	}
	return c
}

type reviewMembers struct{ called bool }

func (m *reviewMembers) ListUsers(context.Context, string) ([]domain.SlackMember, error) {
	m.called = true
	return []domain.SlackMember{{ID: "SECOND"}}, nil
}
func (*reviewMembers) OpenDirectMessage(context.Context, string, string) (string, error) {
	return "D", nil
}
func (*reviewMembers) PostMessage(context.Context, string, string, string) error { return nil }
func TestProductionWorkspaceOnboardingQueue(t *testing.T) {
	db := reviewDB(t, 2)
	ctx := context.Background()
	installs := NewInstallationRepository(db, reviewCipher(t))
	owner, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "T", SlackUserID: "OWNER", BotToken: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	links, _ := secure.NewLinkSigner(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	client := &reviewMembers{}
	queue, err := NewNotificationRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	onboarding := service.NewOnboardingService(installs, client, "https://nudgy.test", links).WithQueue(queue)
	onboarding.WithWorkspacePortal()
	if err = onboarding.DiscoverWorkspace(ctx, owner); err != nil || !client.called {
		t.Fatal("workspace discovery stopped", err)
	}
	var count int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE payload->>'connection'='workspace'`).Scan(&count)
	if count != 2 {
		t.Fatalf("expected owner and member invitations, got %d", count)
	}
	var token string
	_ = db.QueryRow(ctx, `SELECT bot_token FROM slack_installations WHERE team_id='T'`).Scan(&token)
	if !strings.HasPrefix(token, "v2:") || strings.Contains(token, "secret") {
		t.Fatal("Slack token not encrypted")
	}
}
func TestEditLeaseReleasesPoolAndListsOnlyMetadata(t *testing.T) {
	db := reviewDB(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	installs := NewInstallationRepository(db)
	user, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "T", SlackUserID: "U", BotToken: "x"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewCodingStore(ctx, db, reviewCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	id := coding.Identity{UserID: user, Team: "T", Channel: "C", Thread: "1", Event: "E"}
	e, err := store.CreateEdit(ctx, coding.Edit{Task: coding.Task{ID: strings.Repeat("a", 32), Identity: id, Status: "cloning"}, AccountID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WithEdit(ctx, user, e.Task.ID, func(e *coding.Edit) error {
		if err := db.Ping(ctx); err != nil {
			return err
		}
		e.Task.Status = "editing"
		return nil
	}); err != nil {
		t.Fatalf("network callback retained database slot: %v", err)
	}
	// Corrupt the large snapshot: listing must continue from the small envelope.
	_, _ = db.Exec(ctx, `UPDATE coding_edits SET payload='invalid' WHERE id=$1`, e.Task.ID)
	tasks, err := store.ListEditTasks(ctx, id, 42)
	if err != nil || len(tasks) != 1 || tasks[0].Status != "editing" {
		t.Fatal("listing decoded snapshot", err, tasks)
	}
}
func TestQueueExhaustionUnblocksNextJob(t *testing.T) {
	db := reviewDB(t, 2)
	ctx := context.Background()
	jobs, err := NewSlackJobRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	key := repository.SlackJobKey{Team: "T", Channel: "C", Thread: "1", User: "U"}
	for _, event := range []string{"first", "next"} {
		if err = jobs.Enqueue(ctx, repository.SlackJob{Key: key, EventKey: event, Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 8; n++ {
		j, err := jobs.ClaimNext(ctx)
		if err != nil || j.EventKey != "first" {
			t.Fatal(j, err)
		}
		if err = jobs.Retry(ctx, j, 0); err != nil {
			t.Fatal(err)
		}
	}
	next, err := jobs.ClaimNext(ctx)
	if err != nil || next.EventKey != "next" {
		t.Fatal("failed job blocked thread", next, err)
	}
}
func TestActionApprovalOwnerBindingAndOneExecution(t *testing.T) {
	db := reviewDB(t, 2)
	ctx := context.Background()
	installs := NewInstallationRepository(db)
	user, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "T", SlackUserID: "U", BotToken: "x"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := installs.UpsertUser(ctx, "T", "OTHER")
	store := NewActionStore(db, reviewCipher(t))
	p, err := store.ProposeAction(ctx, service.ActionProposal{Identity: coding.Identity{UserID: user, Team: "T", Channel: "C", Thread: "1", Event: "E"}, Tool: "trello_card_create", Arguments: []byte(`{"title":"test"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ApproveAction(ctx, p.ID, other, "T", "C", "1"); err == nil {
		t.Fatal("coworker approved")
	}
	if _, err = store.StartAction(ctx, p.ID, user); err == nil {
		t.Fatal("unapproved action executed")
	}
	if err = store.ApproveAction(ctx, p.ID, user, "T", "C", "1"); err != nil {
		t.Fatal(err)
	}
	got, err := store.StartAction(ctx, p.ID, user)
	if err != nil || got.Identity != p.Identity {
		t.Fatal(got, err)
	}
	if _, err = store.StartAction(ctx, p.ID, user); !errors.Is(err, service.ErrApprovalUnavailable) {
		t.Fatal("replayed external effect", err)
	}
}
func TestUninstallRevokesSessionTokenAndPendingWork(t *testing.T) {
	db := reviewDB(t, 2)
	ctx := context.Background()
	installs := NewInstallationRepository(db)
	user, _ := installs.Save(ctx, domain.SlackInstallation{TeamID: "T", SlackUserID: "U", BotToken: "x"})
	other, _ := installs.Save(ctx, domain.SlackInstallation{TeamID: "OTHER", SlackUserID: "V", BotToken: "y"})
	q, _ := NewNotificationRepository(ctx, db)
	_ = q.Enqueue(ctx, user, "test", "p", repository.NotificationMessage{Connection: "workspace"})
	if err := installs.Deactivate(ctx, "T", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := installs.GetByUserID(ctx, user); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("revoked token usable", err)
	}
	if _, err := installs.GetByUserID(ctx, other); err != nil {
		t.Fatal("other workspace affected", err)
	}
	seeds, err := installs.ListWorkspaceSeeds(ctx)
	if err != nil || len(seeds) != 1 || seeds[0] != other {
		t.Fatal("revoked workspace still scheduled", err, seeds)
	}
	if _, err = q.ClaimNext(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("revoked notification queued", err)
	}
}

func TestPrivacyPreferencesBudgetsAndRetention(t *testing.T) {
	db := reviewDB(t, 3)
	ctx := context.Background()
	installs := NewInstallationRepository(db, reviewCipher(t))
	user, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "T", SlackUserID: "OWNER", BotToken: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewWorkspaceStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SavePreferences(ctx, user, "Europe/Istanbul", true); err != nil {
		t.Fatal(err)
	}
	zone, enabled, err := store.Preferences(ctx, user)
	if err != nil || zone != "Europe/Istanbul" || !enabled {
		t.Fatal(zone, enabled, err)
	}
	if _, err = store.ExportPersonalData(ctx, user); err != nil {
		t.Fatal(err)
	}
	ai, err := NewWorkspaceAIStore(ctx, db, reviewCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("NUDGY_RUN_TOKEN_LIMIT", "100")
	t.Setenv("NUDGY_USER_DAILY_TOKENS", "150")
	id, err := ai.BeginBudget(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ai.BeginBudget(ctx, user); err == nil {
		t.Fatal("quota exceeded")
	}
	if err = ai.FinishBudget(ctx, id, 10); err != nil {
		t.Fatal(err)
	}
	if _, err = ai.BeginBudget(ctx, user); err != nil {
		t.Fatal("unused reservation not released", err)
	}
	if err = Cleanup(ctx, db, 90); err != nil {
		t.Fatal("retention SQL failed", err)
	}
	if err = store.DeletePersonalData(ctx, user); err != nil {
		t.Fatal("privacy deletion failed", err)
	}
	if _, err = store.Member(ctx, user); err != nil {
		t.Fatal("shared membership lost", err)
	}
	if _, err = store.AuditLog(ctx, user); err != nil {
		t.Fatal(err)
	}
}
