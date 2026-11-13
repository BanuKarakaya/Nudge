package migrations

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.sql
var Files embed.FS

var ordered = []string{"001_init.sql", "002_raindrop_token_expiry.sql", "003_feedbin.sql", "004_conversations.sql", "005_analytics.sql", "006_notification_delivery.sql", "007_coding.sql", "008_slack_jobs.sql", "009_merge_intents.sql", "gitlab.sql", "trackers.sql", "branch_rules.sql", "workspace.sql", "workspace_ai.sql", "010_operations.sql"}

// Apply upgrades an empty or legacy database atomically. Advisory locking also
// serializes startup across replicas. Recorded files are immutable: subsequent
// schema changes belong in a new migration, never an edited shipped file.
func Apply(ctx context.Context, db *pgxpool.Pool) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(724910102)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS nudge_schema_migrations(version TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return err
	}
	for _, name := range ordered {
		raw, err := Files.ReadFile(name)
		if err != nil {
			return err
		}
		sql := strings.Split(string(raw), "-- +goose Down")[0]
		checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(sql)))
		var saved string
		if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT checksum FROM nudge_schema_migrations WHERE version=$1),'')`, name).Scan(&saved); err != nil {
			return err
		}
		if saved != "" {
			if saved != checksum {
				return fmt.Errorf("migration %s checksum changed", name)
			}
			continue
		}
		legacy := false
		if name == "001_init.sql" {
			if err = tx.QueryRow(ctx, `SELECT to_regclass('users') IS NOT NULL`).Scan(&legacy); err != nil {
				return err
			}
		}
		if !legacy {
			if _, err = tx.Exec(ctx, sql); err != nil {
				return fmt.Errorf("migration %s: %w", name, err)
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO nudge_schema_migrations(version,checksum) VALUES($1,$2)`, name, checksum); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
