package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"nudge/internal/coding"
)

var _ coding.EditStore = (*CodingStore)(nil)

func (s *CodingStore) ListEditTasks(ctx context.Context, i coding.Identity, accountID int64) ([]coding.Task, error) {
	rows, err := s.db.Query(ctx, `SELECT `+editColumns+` FROM coding_edits WHERE user_id=$1 AND team_id=$2 AND channel_id=$3 AND thread_ts=$4 ORDER BY created_at DESC LIMIT 20`, i.UserID, i.Team, i.Channel, i.Thread)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]coding.Task, 0)
	for rows.Next() {
		e, err := s.scanEdit(rows)
		if err != nil {
			return nil, err
		}
		if e.AccountID == accountID {
			result = append(result, e.Task)
		}
	}
	return result, rows.Err()
}

const editColumns = `payload,user_id,team_id,channel_id,thread_ts,event_id,status`

func (s *CodingStore) encodeEdit(e coding.Edit) (string, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return s.cipher.Encrypt(string(raw))
}

func (s *CodingStore) scanEdit(row pgx.Row) (coding.Edit, error) {
	var e coding.Edit
	var identity coding.Identity
	var encrypted, status string
	err := row.Scan(&encrypted, &identity.UserID, &identity.Team, &identity.Channel, &identity.Thread, &identity.Event, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, coding.ErrNotFound
	}
	if err != nil {
		return e, err
	}
	raw, err := s.cipher.Decrypt(encrypted)
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return e, err
	}
	e.Task.Identity = identity
	e.Task.Status = status
	return e, nil
}

func (s *CodingStore) CreateEdit(ctx context.Context, e coding.Edit) (coding.Edit, error) {
	value, err := s.encodeEdit(e)
	if err != nil {
		return coding.Edit{}, err
	}
	i := e.Task.Identity
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return coding.Edit{}, err
	}
	defer tx.Rollback(context.Background())
	// Serialize admission per owner, without keeping this lock during cloning.
	var owner int64
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND team_id=$2 FOR NO KEY UPDATE`, i.UserID, i.Team).Scan(&owner); err != nil {
		return coding.Edit{}, coding.ErrDenied
	}
	existing, err := s.scanEdit(tx.QueryRow(ctx, `SELECT `+editColumns+` FROM coding_edits WHERE user_id=$1 AND team_id=$2 AND channel_id=$3 AND event_id=$4`, i.UserID, i.Team, i.Channel, i.Event))
	if err == nil {
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, coding.ErrNotFound) {
		return coding.Edit{}, err
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM coding_edits WHERE user_id=$1 AND status NOT IN ('pushed','cancelled')`, i.UserID).Scan(&active); err != nil {
		return coding.Edit{}, err
	}
	if active >= 10 {
		return coding.Edit{}, errors.New("en fazla 10 açık kod görevi olabilir; önce mevcut bir görevi tamamla veya iptal et")
	}
	created, err := s.scanEdit(tx.QueryRow(ctx, `INSERT INTO coding_edits(id,user_id,team_id,channel_id,thread_ts,event_id,status,payload)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT(user_id,team_id,channel_id,event_id) DO UPDATE SET event_id=coding_edits.event_id
RETURNING `+editColumns, e.Task.ID, i.UserID, i.Team, i.Channel, i.Thread, i.Event, e.Task.Status, value))
	if err != nil {
		return coding.Edit{}, err
	}
	return created, tx.Commit(ctx)
}

func (s *CodingStore) GetEdit(ctx context.Context, userID int64, id string) (coding.Edit, error) {
	return s.scanEdit(s.db.QueryRow(ctx, `SELECT `+editColumns+` FROM coding_edits WHERE id=$1 AND user_id=$2`, id, userID))
}

func (s *CodingStore) WithEdit(ctx context.Context, userID int64, id string, fn func(*coding.Edit) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	e, err := s.scanEdit(tx.QueryRow(ctx, `SELECT `+editColumns+` FROM coding_edits WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, userID))
	if err != nil {
		return err
	}
	if err := fn(&e); err != nil {
		return err
	}
	value, err := s.encodeEdit(e)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE coding_edits SET payload=$3,status=$4,updated_at=NOW() WHERE id=$1 AND user_id=$2`, id, userID, value, e.Task.Status); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
