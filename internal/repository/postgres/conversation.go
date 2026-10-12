package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/service"
	"nudge/migrations"
)

type ConversationRepository struct{ db *pgxpool.Pool }

func NewConversationRepository(ctx context.Context, db *pgxpool.Pool) (*ConversationRepository, error) {
	if _, err := db.Exec(ctx, migrations.ConversationSchema); err != nil {
		return nil, err
	}
	return &ConversationRepository{db: db}, nil
}

func (r *ConversationRepository) Load(ctx context.Context, k service.ConversationKey) (service.ConversationState, error) {
	var state service.ConversationState
	var raw []byte
	err := r.db.QueryRow(ctx, `SELECT state FROM nudge_conversations WHERE team_id=$1 AND channel_id=$2 AND thread_ts=$3 AND slack_user_id=$4`, k.Team, k.Channel, k.Thread, k.User).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(raw, &state)
	return state, err
}

func (r *ConversationRepository) Save(ctx context.Context, k service.ConversationKey, state service.ConversationState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO nudge_conversations (team_id,channel_id,thread_ts,slack_user_id,state) VALUES($1,$2,$3,$4,$5)
	ON CONFLICT(team_id,channel_id,thread_ts,slack_user_id) DO UPDATE SET state=EXCLUDED.state,updated_at=NOW()`, k.Team, k.Channel, k.Thread, k.User, raw)
	return err
}
