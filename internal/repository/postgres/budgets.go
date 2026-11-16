package postgres

import (
	"context"
	"errors"
	"os"
	"strconv"

	"nudge/internal/analytics"
	"nudge/internal/coding"
)

func budgetLimit(name string, fallback int64) int64 {
	v, e := strconv.ParseInt(os.Getenv(name), 10, 64)
	if e != nil || v <= 0 {
		return fallback
	}
	return v
}
func (s *WorkspaceAIStore) BeginBudget(ctx context.Context, user int64) (string, int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback(context.Background())
	var team string
	if err = tx.QueryRow(ctx, `SELECT u.team_id FROM users u JOIN slack_installations i ON i.team_id=u.team_id AND i.bot_token<>'' WHERE u.id=$1`, user).Scan(&team); err != nil {
		return "", 0, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,17))`, team); err != nil {
		return "", 0, err
	}
	var workspaceTokens, userTokens, running int64
	err = tx.QueryRow(ctx, `SELECT COALESCE(sum(tokens),0),COALESCE(sum(tokens) FILTER(WHERE user_id=$2),0),count(*) FILTER(WHERE running AND lease_until>NOW()) FROM nudge_run_budgets WHERE team_id=$1 AND day=(NOW() AT TIME ZONE 'UTC')::date`, team, user).Scan(&workspaceTokens, &userTokens, &running)
	if err != nil {
		return "", 0, err
	}
	reserve := budgetLimit("NUDGY_RUN_TOKEN_LIMIT", 100000)
	if running >= budgetLimit("NUDGY_WORKSPACE_CONCURRENCY", 2) {
		return "", 0, analytics.Failure("workspace_busy", errors.New("çalışma alanının eşzamanlı iş sınırına ulaşıldı; biraz sonra tekrar dene"))
	}
	// Reserve only the remaining daily allowance, not a full run's maximum.
	// The runtime receives this smaller limit and enforces it across model routing.
	reserve = min(reserve, budgetLimit("NUDGY_WORKSPACE_DAILY_TOKENS", 2000000)-workspaceTokens, budgetLimit("NUDGY_USER_DAILY_TOKENS", 500000)-userTokens)
	if reserve <= 0 {
		return "", 0, analytics.Failure("daily_quota", errors.New("günlük AI token kotası doldu; kota UTC gece yarısında yenilenir"))
	}
	id, err := coding.RandomID()
	if err != nil {
		return "", 0, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO nudge_run_budgets(id,user_id,team_id,tokens) VALUES($1,$2,$3,$4)`, id, user, team, reserve)
	if err != nil {
		return "", 0, err
	}
	return id, int(reserve), tx.Commit(ctx)
}
func (s *WorkspaceAIStore) FinishBudget(ctx context.Context, id string, tokens int) error {
	// Unknown usage retains the reservation; failure must not enable unlimited runs.
	_, err := s.db.Exec(ctx, `UPDATE nudge_run_budgets SET running=FALSE,tokens=CASE WHEN $2::bigint>0 THEN $2 ELSE tokens END WHERE id=$1 AND running`, id, tokens)
	return err
}
