package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"

	"nudge/internal/coding"
)

func (s *CodingStore) GetMergePin(ctx context.Context, i coding.Identity) (coding.MergePin, bool, error) {
	provider := "github"
	if s.provider == "gitlab" {
		provider = "gitlab"
	}
	var raw []byte
	var channel, thread string
	err := s.db.QueryRow(ctx, `SELECT pin,channel_id,thread_ts FROM nudge_merge_intents WHERE provider=$1 AND user_id=$2 AND team_id=$3 AND event_id=$4`, provider, i.UserID, i.Team, i.Event).Scan(&raw, &channel, &thread)
	if errors.Is(err, pgx.ErrNoRows) {
		return coding.MergePin{}, false, nil
	}
	if err != nil {
		return coding.MergePin{}, false, err
	}
	if channel != i.Channel || thread != i.Thread {
		return coding.MergePin{}, false, coding.ErrDenied
	}
	var pin coding.MergePin
	err = json.Unmarshal(raw, &pin)
	return pin, err == nil, err
}

func (s *CodingStore) PinMerge(ctx context.Context, i coding.Identity, pin coding.MergePin) (coding.MergePin, error) {
	if i.Event == "" || i.Channel == "" || i.Thread == "" {
		return coding.MergePin{}, coding.ErrArguments
	}
	raw, err := json.Marshal(pin)
	if err != nil {
		return coding.MergePin{}, err
	}
	provider := "github"
	if s.provider == "gitlab" {
		provider = "gitlab"
	}
	// Persist the first revision before the remote write. A no-op conflict update
	// returns that original revision, including under concurrent requests.
	var saved []byte
	var channel, thread string
	err = s.db.QueryRow(ctx, `INSERT INTO nudge_merge_intents(provider,user_id,team_id,event_id,channel_id,thread_ts,pin)
 SELECT $1,u.id,u.team_id,$4,$5,$6,$7 FROM users u JOIN slack_installations si ON si.team_id=u.team_id WHERE u.id=$2 AND u.team_id=$3
 ON CONFLICT(provider,user_id,team_id,event_id) DO UPDATE SET pin=nudge_merge_intents.pin
 RETURNING pin,channel_id,thread_ts`, provider, i.UserID, i.Team, i.Event, i.Channel, i.Thread, raw).Scan(&saved, &channel, &thread)
	if err != nil {
		return coding.MergePin{}, err
	}
	if channel != i.Channel || thread != i.Thread {
		return coding.MergePin{}, coding.ErrDenied
	}
	var result coding.MergePin
	err = json.Unmarshal(saved, &result)
	return result, err
}

var _ coding.MergeStore = (*CodingStore)(nil)
