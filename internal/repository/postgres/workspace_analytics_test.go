package postgres

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/analytics"
	"nudge/internal/domain"
	"nudge/internal/repository"
)

func testWorkspaceAnalytics(t *testing.T, db *pgxpool.Pool, repo *AnalyticsRepository) {
	t.Helper()
	ctx := context.Background()
	initial, err := os.ReadFile("../../../migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, strings.Split(string(initial), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = NewWorkspaceStore(ctx, db); err != nil {
		t.Fatal(err)
	}
	installs := NewInstallationRepository(db)
	owner, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "A", TeamName: "A", SlackUserID: "OWNER", BotToken: "bot"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := installs.UpsertUser(ctx, "A", "MEMBER")
	if err != nil {
		t.Fatal(err)
	}
	other, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "B", TeamName: "B", SlackUserID: "OWNER", BotToken: "other"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Minute)
	for _, row := range []struct {
		id, team, user, model, status string
		uid, tokens                   int64
	}{
		{"owner", "A", "OWNER", "model-a", "success", owner, 10},
		{"member", "A", "MEMBER", "model-b", "failed", member, 20},
		{"other", "B", "OWNER", "other-model", "success", other, 50},
		{"history", "A", "OWNER", "model-a", "success", 0, 5},
		{"mismatch", "A", "OWNER", "model-a", "success", member, 7},
	} {
		rec := repository.InteractionRecord{RequestID: row.id, TeamID: row.team, SlackUserID: row.user, UserID: row.uid, Status: row.status, Action: "AGENT", StartedAt: now, LatencyMS: 100, Usage: []analytics.AIUsage{{Model: row.model, TotalTokens: int(row.tokens)}}, Operations: []analytics.Operation{{Tool: "web_search", Status: "success", DurationMS: 10}}}
		if err := repo.RecordInteraction(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	// Legacy content must not be read back through the new customer interface.
	if _, err := db.Exec(ctx, `UPDATE nudge_ai_interactions SET request_text='PRIVATE_REQUEST',response_text='PRIVATE_RESPONSE',operations='[{"tool":"web_search","status":"success","duration_ms":10,"title":"PRIVATE_TITLE","detail":"PRIVATE_DETAIL","path":"PRIVATE_PATH","diff":"PRIVATE_DIFF"}]'::jsonb WHERE request_id='history'`); err != nil {
		t.Fatal(err)
	}
	own, err := repo.WorkspaceDashboard(ctx, owner, false, repository.DashboardFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if own.TotalMessages != 2 || own.TotalTokens != 15 || len(own.Recent) != 2 || len(own.Models) != 1 || own.Models[0].Model != "model-a" || len(own.Daily) != 1 || own.Daily[0].Tokens != 15 {
		t.Fatalf("personal isolation failed: %+v", own)
	}
	raw, _ := json.Marshal(own)
	if strings.Contains(string(raw), "PRIVATE_") || strings.Contains(string(raw), "other-model") {
		t.Fatal("private historical fields leaked")
	}
	for _, row := range own.Recent {
		if row.RequestID != "" || row.Request != "" || row.Response != "" || len(row.Operations) != 1 || row.Operations[0].Tool != "web_search" || row.Operations[0].DurationMS != 10 {
			t.Fatal("unsafe/incomplete operation projection", row)
		}
	}
	team, err := repo.WorkspaceDashboard(ctx, owner, true, repository.DashboardFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if team.TotalMessages != 4 || team.TotalTokens != 42 || team.ErrorCount != 1 || len(team.Recent) != 0 {
		t.Fatal("workspace totals leaked or incomplete", team)
	}
	if _, err := repo.WorkspaceDashboard(ctx, member, true, repository.DashboardFilters{}); err == nil {
		t.Fatal("member obtained team totals")
	}
	if _, err := repo.WorkspaceDashboard(ctx, 99999, false, repository.DashboardFilters{}); err == nil {
		t.Fatal("unknown user obtained data")
	}
	personal, err := repo.WorkspaceDashboard(ctx, member, false, repository.DashboardFilters{})
	if err != nil || personal.TotalTokens != 20 || len(personal.Recent) != 1 {
		t.Fatal("employee identity mismatch", personal, err)
	}
	second, err := repo.WorkspaceDashboard(ctx, other, true, repository.DashboardFilters{})
	if err != nil || second.TotalTokens != 50 || len(second.Recent) != 0 {
		t.Fatal("cross-workspace totals", second, err)
	}
	page, err := repo.WorkspaceDashboard(ctx, owner, false, repository.DashboardFilters{Limit: 1, Offset: 1})
	if err != nil || len(page.Recent) != 1 || page.TotalMessages != 2 {
		t.Fatal("pagination", page, err)
	}
	filtered, err := repo.WorkspaceDashboard(ctx, owner, true, repository.DashboardFilters{Model: "model-b", Status: "issues"})
	if err != nil || filtered.TotalTokens != 20 {
		t.Fatal("scoped filters", filtered, err)
	}
	future, err := repo.WorkspaceDashboard(ctx, owner, false, repository.DashboardFilters{From: time.Now().Add(time.Hour), To: time.Now().Add(2 * time.Hour)})
	if err != nil || future.TotalMessages != 0 {
		t.Fatal("date filtering", future, err)
	}
}
