package migrations

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func migrationDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("NUDGE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set NUDGE_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("migration_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("clean migration schema: %v", err)
		}
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestAllEmbeddedMigrationsAreRegisteredOnce(t *testing.T) {
	files, err := Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, name := range ordered {
		if seen[name] {
			t.Fatalf("duplicate migration: %s", name)
		}
		seen[name] = true
		if _, err := Files.ReadFile(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		if !seen[file.Name()] {
			t.Errorf("embedded migration is never applied: %s", file.Name())
		}
	}
}

func TestMigrationConcurrentStartupAndRestartPreserveData(t *testing.T) {
	db := migrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	errors := make(chan error, 4)
	for range 4 {
		go func() { errors <- Apply(ctx, db) }()
	}
	for range 4 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO slack_installations(team_id,team_name,bot_token) VALUES('TTEST','Migration test','synthetic')`); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM nudge_schema_migrations`).Scan(&count); err != nil || count != len(ordered) {
		t.Fatalf("migration count=%d err=%v", count, err)
	}
	var name string
	if err := db.QueryRow(ctx, `SELECT team_name FROM slack_installations WHERE team_id='TTEST'`).Scan(&name); err != nil || name != "Migration test" {
		t.Fatalf("restart lost existing data: %q %v", name, err)
	}
}

func TestMigrationAdoptsLegacyBootstrap(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	for _, name := range ordered[:3] {
		raw, err := Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO slack_installations(team_id,team_name,bot_token) VALUES('TLEGACY','Legacy data','synthetic')`); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM slack_installations WHERE team_id='TLEGACY'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy data lost: %d %v", count, err)
	}
}

func TestMigrationChecksumMismatchRollsBackPendingChanges(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	if err := Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	// Reapply an earlier, idempotent migration before encountering a corrupt
	// checksum. Its new version record must roll back with the transaction.
	if _, err := db.Exec(ctx, `DELETE FROM nudge_schema_migrations WHERE version='002_raindrop_token_expiry.sql'; UPDATE nudge_schema_migrations SET checksum='corrupt' WHERE version='003_feedbin.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db); err == nil || !strings.Contains(err.Error(), "003_feedbin.sql checksum changed") {
		t.Fatalf("changed migration was accepted: %v", err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM nudge_schema_migrations WHERE version='002_raindrop_token_expiry.sql'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed upgrade partially committed: %d %v", count, err)
	}
}

func TestMigrationFailedFreshInstallIsAtomic(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	// A conflicting preexisting table forces the base migration to fail after
	// it creates other tables. None of those partial changes may remain.
	if _, err := db.Exec(ctx, `CREATE TABLE bookmarks(marker text); INSERT INTO bookmarks VALUES('keep')`); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db); err == nil {
		t.Fatal("expected conflicting schema to reject installation")
	}
	var clean bool
	if err := db.QueryRow(ctx, `SELECT to_regclass('users') IS NULL AND to_regclass('slack_installations') IS NULL AND to_regclass('nudge_schema_migrations') IS NULL`).Scan(&clean); err != nil || !clean {
		t.Fatalf("partial schema survived failed install: %t %v", clean, err)
	}
	var marker string
	if err := db.QueryRow(ctx, `SELECT marker FROM bookmarks`).Scan(&marker); err != nil || marker != "keep" {
		t.Fatalf("preexisting data changed: %q %v", marker, err)
	}
}
