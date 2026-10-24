package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/coding"
	"nudge/internal/secure"
	"nudge/migrations"
)

type CodingStore struct {
	db     *pgxpool.Pool
	cipher secure.Cipher
}

var _ coding.Store = (*CodingStore)(nil)

func NewCodingStore(ctx context.Context, db *pgxpool.Pool, cipher secure.Cipher) (*CodingStore, error) {
	if _, err := db.Exec(ctx, migrations.CodingSchema); err != nil {
		return nil, err
	}
	return &CodingStore{db: db, cipher: cipher}, nil
}

func (s *CodingStore) encode(c coding.Credentials) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return s.cipher.Encrypt(string(raw))
}

func (s *CodingStore) SaveConnection(ctx context.Context, c coding.Connection) error {
	value, err := s.encode(c.Credentials)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO coding_connections(user_id,github_user_id,login,credentials) VALUES($1,$2,$3,$4)
ON CONFLICT(user_id) DO UPDATE SET github_user_id=EXCLUDED.github_user_id,login=EXCLUDED.login,credentials=EXCLUDED.credentials,updated_at=NOW()`, c.UserID, c.GitHubUserID, c.Login, value)
	return err
}

func (s *CodingStore) WithConnection(ctx context.Context, userID int64, fn func(*coding.Connection) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var c coding.Connection
	var encrypted string
	err = tx.QueryRow(ctx, `SELECT user_id,github_user_id,login,credentials FROM coding_connections WHERE user_id=$1 FOR UPDATE`, userID).Scan(&c.UserID, &c.GitHubUserID, &c.Login, &encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return coding.ErrConnect
	}
	if err != nil {
		return err
	}
	raw, err := s.cipher.Decrypt(encrypted)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), &c.Credentials); err != nil {
		return err
	}
	before := c.Credentials
	if err := fn(&c); err != nil {
		return err
	}
	if before != c.Credentials {
		value, err := s.encode(c.Credentials)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE coding_connections SET credentials=$2,updated_at=NOW() WHERE user_id=$1`, userID, value); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *CodingStore) Disconnect(ctx context.Context, userID int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM coding_connections WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM coding_oauth_states WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE coding_tasks SET status='cancelled' WHERE user_id=$1 AND status='draft'`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *CodingStore) PutOAuthState(ctx context.Context, hash string, state coding.OAuthState) error {
	verifier, err := s.cipher.Encrypt(state.Verifier)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM coding_oauth_states WHERE expires_at <= NOW() OR user_id=$1`, state.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO coding_oauth_states(state_hash,user_id,verifier,expires_at) VALUES($1,$2,$3,$4)`, hash, state.UserID, verifier, state.Expires); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *CodingStore) ConsumeOAuthState(ctx context.Context, hash string) (coding.OAuthState, error) {
	var state coding.OAuthState
	var encrypted string
	err := s.db.QueryRow(ctx, `DELETE FROM coding_oauth_states WHERE state_hash=$1 AND expires_at>NOW() RETURNING user_id,verifier,expires_at`, hash).Scan(&state.UserID, &encrypted, &state.Expires)
	if err != nil {
		return state, coding.ErrConnect
	}
	state.Verifier, err = s.cipher.Decrypt(encrypted)
	return state, err
}

const codingColumns = `id,user_id,team_id,channel_id,thread_ts,event_id,provider,repository,instruction,status,created_at`

func scanCodingTask(row pgx.Row) (coding.Task, error) {
	var t coding.Task
	var repo []byte
	err := row.Scan(&t.ID, &t.Identity.UserID, &t.Identity.Team, &t.Identity.Channel, &t.Identity.Thread, &t.Identity.Event, &t.Provider, &repo, &t.Instruction, &t.Status, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, coding.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	err = json.Unmarshal(repo, &t.Repository)
	return t, err
}

func (s *CodingStore) CreateDraft(ctx context.Context, t coding.Task) (coding.Task, error) {
	raw, err := json.Marshal(t.Repository)
	if err != nil {
		return coding.Task{}, err
	}
	// A retry of the same Slack event returns the original immutable draft.
	return scanCodingTask(s.db.QueryRow(ctx, `INSERT INTO coding_tasks (`+codingColumns+`)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'draft',$10)
ON CONFLICT(user_id,team_id,channel_id,event_id) DO UPDATE SET event_id=coding_tasks.event_id RETURNING `+codingColumns,
		t.ID, t.Identity.UserID, t.Identity.Team, t.Identity.Channel, t.Identity.Thread, t.Identity.Event, t.Provider, raw, t.Instruction, t.CreatedAt))
}

func (s *CodingStore) Task(ctx context.Context, i coding.Identity, id string) (coding.Task, error) {
	return scanCodingTask(s.db.QueryRow(ctx, `SELECT `+codingColumns+` FROM coding_tasks WHERE id=$1 AND user_id=$2 AND team_id=$3 AND channel_id=$4 AND thread_ts=$5`, id, i.UserID, i.Team, i.Channel, i.Thread))
}

func (s *CodingStore) CancelDraft(ctx context.Context, i coding.Identity, id string) (coding.Task, error) {
	return scanCodingTask(s.db.QueryRow(ctx, `UPDATE coding_tasks SET status='cancelled' WHERE id=$1 AND user_id=$2 AND team_id=$3 AND channel_id=$4 AND thread_ts=$5 AND status IN ('draft','cancelled') RETURNING `+codingColumns, id, i.UserID, i.Team, i.Channel, i.Thread))
}
