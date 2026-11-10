package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/secure"
	"nudge/internal/tracker"
	"nudge/migrations"
)

type TrackerStore struct {
	db     *pgxpool.Pool
	cipher secure.Cipher
}

var _ tracker.Store = (*TrackerStore)(nil)

func NewTrackerStore(ctx context.Context, db *pgxpool.Pool, cipher secure.Cipher) (*TrackerStore, error) {
	if _, err := db.Exec(ctx, migrations.TrackerSchema); err != nil {
		return nil, err
	}
	return &TrackerStore{db, cipher}, nil
}
func (s *TrackerStore) encode(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return s.cipher.Encrypt(string(raw))
}
func (s *TrackerStore) decode(value string, out any) error {
	raw, err := s.cipher.Decrypt(value)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), out)
}
func (s *TrackerStore) SlackTeam(ctx context.Context, id int64) (string, error) {
	var team string
	err := s.db.QueryRow(ctx, `SELECT u.team_id FROM users u JOIN slack_installations i ON i.team_id=u.team_id WHERE u.id=$1`, id).Scan(&team)
	return team, err
}
func (s *TrackerStore) PutState(ctx context.Context, provider, hash string, state tracker.State) error {
	value, err := s.encode(state)
	if err != nil {
		return err
	}
	if _, err = s.db.Exec(ctx, `DELETE FROM tracker_oauth_states WHERE expires_at<=NOW()`); err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO tracker_oauth_states(provider,state_hash,user_id,encrypted,expires_at) VALUES($1,$2,$3,$4,$5)`, provider, hash, state.UserID, value, state.Expires)
	return err
}
func (s *TrackerStore) ConsumeState(ctx context.Context, provider, hash string) (tracker.State, error) {
	var raw string
	var state tracker.State
	err := s.db.QueryRow(ctx, `DELETE FROM tracker_oauth_states WHERE provider=$1 AND state_hash=$2 RETURNING encrypted`, provider, hash).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, tracker.ErrConnect
	}
	if err != nil {
		return state, err
	}
	err = s.decode(raw, &state)
	return state, err
}
func (s *TrackerStore) Save(ctx context.Context, c tracker.Connection) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var previous string
	err = tx.QueryRow(ctx, `SELECT encrypted FROM tracker_connections WHERE provider=$1 AND user_id=$2 FOR UPDATE`, c.Provider, c.UserID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		var old tracker.Connection
		if err = s.decode(previous, &old); err != nil {
			return err
		}
		// Reauthorization of the same account must not erase replay protection.
		if old.Identity == c.Identity && old.Account.ID == c.Account.ID {
			c.Mutations = old.Mutations
		}
	}
	raw, err := s.encode(c)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `INSERT INTO tracker_connections(provider,user_id,encrypted)
SELECT $1,u.id,$3 FROM users u JOIN slack_installations i ON i.team_id=u.team_id WHERE u.id=$2 AND u.team_id=$4
ON CONFLICT(provider,user_id) DO UPDATE SET encrypted=EXCLUDED.encrypted,updated_at=NOW()`, c.Provider, c.UserID, raw, c.Team)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return tracker.ErrDenied
	}
	return tx.Commit(ctx)
}
func (s *TrackerStore) WithConnection(ctx context.Context, provider string, user int64, fn func(*tracker.Connection) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var raw string
	var c tracker.Connection
	err = tx.QueryRow(ctx, `SELECT encrypted FROM tracker_connections WHERE provider=$1 AND user_id=$2 FOR UPDATE`, provider, user).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return tracker.ErrConnect
	}
	if err != nil {
		return err
	}
	if err = s.decode(raw, &c); err != nil {
		return err
	}
	if c.Provider != provider || c.UserID != user {
		return tracker.ErrDenied
	}
	before, _ := json.Marshal(c)
	if err = fn(&c); err != nil {
		return err
	}
	after, _ := json.Marshal(c)
	if string(before) != string(after) {
		raw, err = s.encode(c)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE tracker_connections SET encrypted=$3,updated_at=NOW() WHERE provider=$1 AND user_id=$2`, provider, user, raw); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *TrackerStore) Disconnect(ctx context.Context, provider string, user int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM tracker_connections WHERE provider=$1 AND user_id=$2`, provider, user); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM tracker_oauth_states WHERE provider=$1 AND user_id=$2`, provider, user); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
