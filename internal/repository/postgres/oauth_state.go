package postgres

import (
	"context"
	"errors"
	"time"
)

func (r *InstallationRepository) PutLoginState(ctx context.Context, hash, purpose string, user int64, expires time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(724910103)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM nudge_oauth_states WHERE expires_at<NOW()`); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM nudge_oauth_states`).Scan(&count); err != nil {
		return err
	}
	if count >= 10000 {
		return errors.New("OAuth capacity exceeded")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO nudge_oauth_states(state_hash,purpose,user_id,expires_at) VALUES($1,$2,$3,$4)`, hash, purpose, user, expires); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *InstallationRepository) ConsumeLoginState(ctx context.Context, hash, purpose string) (int64, error) {
	var user int64
	err := r.db.QueryRow(ctx, `DELETE FROM nudge_oauth_states WHERE state_hash=$1 AND purpose=$2 AND expires_at>NOW() RETURNING user_id`, hash, purpose).Scan(&user)
	return user, err
}
