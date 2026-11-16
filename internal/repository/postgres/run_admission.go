package postgres

import (
	"context"
	"errors"
	"os"
	"strconv"

	"nudge/internal/analytics"
	"nudge/internal/coding"
)

func runConcurrencyLimit(name string, fallback int64) int64 {
	v, e := strconv.ParseInt(os.Getenv(name), 10, 64)
	if e != nil || v <= 0 {
		return fallback
	}
	return v
}
func (s *WorkspaceAIStore) BeginRun(ctx context.Context, user int64) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(context.Background())
	var team string
	if err = tx.QueryRow(ctx, `SELECT u.team_id FROM users u JOIN slack_installations i ON i.team_id=u.team_id AND i.bot_token<>'' WHERE u.id=$1`, user).Scan(&team); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,17))`, team); err != nil {
		return "", err
	}
	var running int64
	err = tx.QueryRow(ctx, `SELECT count(*) FROM nudge_run_budgets WHERE team_id=$1 AND running AND lease_until>NOW()`, team).Scan(&running)
	if err != nil {
		return "", err
	}
	if running >= runConcurrencyLimit("NUDGY_WORKSPACE_CONCURRENCY", 2) {
		return "", analytics.Failure("workspace_busy", errors.New("çalışma alanının eşzamanlı iş sınırına ulaşıldı; biraz sonra tekrar dene"))
	}
	id, err := coding.RandomID()
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO nudge_run_budgets(id,user_id,team_id,tokens) VALUES($1,$2,$3,0)`, id, user, team)
	if err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

// The legacy table remains for migration compatibility; only active run leases
// are used. Historical token reservations never restrict new requests.
func (s *WorkspaceAIStore) FinishRun(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE nudge_run_budgets SET running=FALSE WHERE id=$1 AND running`, id)
	return err
}
