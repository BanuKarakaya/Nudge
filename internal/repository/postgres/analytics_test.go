package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/analytics"
	"nudge/internal/repository"
	"nudge/migrations"
)

func TestAnalyticsOperationsPostgres(t *testing.T) {
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
	schema := fmt.Sprintf("nudge_operations_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
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
	// Simulate an existing deployment with one pre-trace interaction.
	if _, err := db.Exec(ctx, strings.Split(migrations.AnalyticsSchema, "-- Additive")[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO nudge_ai_interactions(request_id,team_id,channel_id,thread_ts,slack_user_id,request_text,created_at) VALUES('legacy','T','C','1','U','old',NOW()-INTERVAL '1 hour')`); err != nil {
		t.Fatal(err)
	}
	repo, err := NewAnalyticsRepository(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewAnalyticsRepository(ctx, db); err != nil {
		t.Fatal(err)
	}
	record := repository.InteractionRecord{RequestID: "new", RequestText: "RSS getir", Status: "success", Operations: []analytics.Operation{{Tool: "rss_articles", Title: "RSS başlıkları getirildi", Detail: "3 kayıt", Status: "success", StartedAt: time.Now()}}, Usage: []analytics.AIUsage{{Model: "test-model"}}}
	if err := repo.RecordInteraction(ctx, record); err != nil {
		t.Fatal(err)
	}
	record.Operations = append(record.Operations, analytics.Operation{Tool: "web_search", Status: "failed"})
	if err := repo.RecordInteraction(ctx, record); err != nil {
		t.Fatal(err)
	}
	data, err := repo.Dashboard(ctx, repository.DashboardFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if data.TotalMessages != 2 || len(data.Recent) != 2 || data.Recent[0].TraceVersion != 1 || len(data.Recent[0].Operations) != 2 || data.Recent[0].Operations[1].Status != "failed" || data.Recent[1].TraceVersion != 0 || len(data.Recent[1].Operations) != 0 {
		t.Fatalf("unexpected dashboard: %+v", data)
	}
	page, err := repo.Dashboard(ctx, repository.DashboardFilters{Limit: 1, Offset: 1})
	if err != nil || len(page.Recent) != 1 || page.Recent[0].Request != "old" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	filtered, err := repo.Dashboard(ctx, repository.DashboardFilters{Model: "test-model", Status: "success"})
	if err != nil || filtered.TotalMessages != 1 || len(filtered.Recent[0].Operations) != 2 {
		t.Fatalf("filters=%+v err=%v", filtered, err)
	}
}
