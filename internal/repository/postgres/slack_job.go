package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/repository"
	"nudge/migrations"
)

type SlackJobRepository struct{ db *pgxpool.Pool }

func NewSlackJobRepository(ctx context.Context, db *pgxpool.Pool) (*SlackJobRepository, error) {
	if err := migrations.Apply(ctx, db); err != nil {
		return nil, err
	}
	return &SlackJobRepository{db: db}, nil
}

func (r *SlackJobRepository) Enqueue(ctx context.Context, j repository.SlackJob) error {
	if j.Key.Team == "" || j.Key.Channel == "" || j.Key.Thread == "" || j.Key.User == "" || j.EventKey == "" || !json.Valid(j.Payload) {
		return errors.New("invalid Slack job")
	}
	_, err := r.db.Exec(ctx, `INSERT INTO nudge_slack_jobs(team_id,event_key,channel_id,thread_ts,slack_user_id,payload)
        VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(team_id,event_key) DO NOTHING`,
		j.Key.Team, j.EventKey, j.Key.Channel, j.Key.Thread, j.Key.User, j.Payload)
	return err
}

func (r *SlackJobRepository) ClaimNext(ctx context.Context) (repository.SlackJob, error) {
	var j repository.SlackJob
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return j, err
	}
	if _, err := r.db.Exec(ctx, `UPDATE nudge_slack_jobs SET status='failed',lease_until=NULL,claim_token=NULL,finished_at=NOW(),updated_at=NOW() WHERE attempts>=8 AND (status='queued' OR (status='running' AND lease_until<=NOW()))`); err != nil {
		return j, err
	}
	// Only the oldest unfinished request for a conversation is eligible, even
	// while it is backing off. SKIP LOCKED allows unrelated conversations to run.
	err := r.db.QueryRow(ctx, `WITH candidate AS (
        SELECT q.id FROM nudge_slack_jobs q
        WHERE ((q.status='queued' AND q.available_at<=NOW()) OR (q.status='running' AND q.lease_until<=NOW()))
        AND NOT EXISTS (SELECT 1 FROM nudge_slack_jobs p
            WHERE p.team_id=q.team_id AND p.channel_id=q.channel_id AND p.thread_ts=q.thread_ts
            AND p.slack_user_id=q.slack_user_id AND p.id<q.id AND p.status IN ('queued','running'))
        ORDER BY q.id FOR UPDATE OF q SKIP LOCKED LIMIT 1
    ) UPDATE nudge_slack_jobs q SET status='running',attempts=attempts+1,
        claim_token=$1,lease_until=NOW()+($2*INTERVAL '1 second'),updated_at=NOW()
      FROM candidate WHERE q.id=candidate.id
      RETURNING q.id,q.team_id,q.channel_id,q.thread_ts,q.slack_user_id,q.event_key,
        q.payload,q.claim_token,q.attempts,q.result,q.delivered_at IS NOT NULL`,
		hex.EncodeToString(token[:]), repository.SlackJobLease.Seconds()).Scan(
		&j.ID, &j.Key.Team, &j.Key.Channel, &j.Key.Thread, &j.Key.User, &j.EventKey,
		&j.Payload, &j.ClaimToken, &j.Attempts, &j.Result, &j.Delivered)
	return j, err
}

const ownedSlackJob = `id=$1 AND claim_token=$2 AND status='running' AND lease_until>NOW()`

func (r *SlackJobRepository) CheckLease(ctx context.Context, j repository.SlackJob) error {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM nudge_slack_jobs WHERE `+ownedSlackJob+`)`, j.ID, j.ClaimToken).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return repository.ErrSlackJobLeaseLost
	}
	return nil
}

func jobUpdated(tag pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return repository.ErrSlackJobLeaseLost
	}
	return nil
}

func (r *SlackJobRepository) SaveResult(ctx context.Context, j repository.SlackJob, result json.RawMessage) error {
	return jobUpdated(r.db.Exec(ctx, `UPDATE nudge_slack_jobs SET result=$3,updated_at=NOW()
        WHERE `+ownedSlackJob+` AND result IS NULL`, j.ID, j.ClaimToken, result))
}

func (r *SlackJobRepository) MarkDelivered(ctx context.Context, j repository.SlackJob) error {
	return jobUpdated(r.db.Exec(ctx, `UPDATE nudge_slack_jobs SET delivered_at=COALESCE(delivered_at,NOW()),updated_at=NOW()
        WHERE `+ownedSlackJob+` AND result IS NOT NULL`, j.ID, j.ClaimToken))
}

func (r *SlackJobRepository) Complete(ctx context.Context, j repository.SlackJob, state json.RawMessage, record repository.InteractionRecord, success bool) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	status := "failed"
	if success {
		status = "completed"
	}
	if err := jobUpdated(tx.Exec(ctx, `UPDATE nudge_slack_jobs SET status=$3,claim_token=NULL,
        lease_until=NULL,updated_at=NOW(),finished_at=NOW() WHERE `+ownedSlackJob+` AND delivered_at IS NOT NULL`, j.ID, j.ClaimToken, status)); err != nil {
		return err
	}
	if state != nil {
		_, err = tx.Exec(ctx, `INSERT INTO nudge_conversations(team_id,channel_id,thread_ts,slack_user_id,state) VALUES($1,$2,$3,$4,$5)
            ON CONFLICT(team_id,channel_id,thread_ts,slack_user_id) DO UPDATE SET state=EXCLUDED.state,updated_at=NOW()`,
			j.Key.Team, j.Key.Channel, j.Key.Thread, j.Key.User, state)
		if err != nil {
			return err
		}
	}
	if err := recordInteraction(ctx, tx, record); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *SlackJobRepository) Retry(ctx context.Context, j repository.SlackJob, delay time.Duration) error {
	return jobUpdated(r.db.Exec(ctx, `UPDATE nudge_slack_jobs SET status=CASE WHEN attempts>=8 THEN 'failed' ELSE 'queued' END,finished_at=CASE WHEN attempts>=8 THEN NOW() ELSE NULL END,claim_token=NULL,
        lease_until=NULL,available_at=NOW()+($3*INTERVAL '1 second'),updated_at=NOW() WHERE `+ownedSlackJob,
		j.ID, j.ClaimToken, delay.Seconds()))
}

var _ repository.SlackJobRepository = (*SlackJobRepository)(nil)
