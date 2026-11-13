package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"nudge/internal/coding"
)

var _ coding.EditStore = (*CodingStore)(nil)

func (s *CodingStore) ListEditTasks(ctx context.Context, i coding.Identity, accountID int64) ([]coding.Task, error) {
	// Hydrate only pre-upgrade metadata once; subsequent listings never select
	// or decrypt the snapshot payload.
	legacy, err := s.db.Query(ctx, `SELECT id FROM `+s.table("coding_edits")+` WHERE user_id=$1 AND team_id=$2 AND channel_id=$3 AND thread_ts=$4 AND metadata IS NULL ORDER BY created_at DESC LIMIT 20`, i.UserID, i.Team, i.Channel, i.Thread)
	if err != nil {
		return nil, err
	}
	var ids []string
	for legacy.Next() {
		var id string
		if err = legacy.Scan(&id); err != nil {
			legacy.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = legacy.Err()
	legacy.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		e, err := s.GetEdit(ctx, i.UserID, id)
		if err != nil {
			return nil, err
		}
		value, err := s.encodeEdit(coding.Edit{Task: e.Task, AccountID: e.AccountID})
		if err != nil {
			return nil, err
		}
		if _, err = s.db.Exec(ctx, `UPDATE `+s.table("coding_edits")+` SET metadata=$3 WHERE id=$1 AND user_id=$2 AND metadata IS NULL`, id, i.UserID, value); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.Query(ctx, `SELECT metadata,user_id,team_id,channel_id,thread_ts,event_id,status,id FROM `+s.table("coding_edits")+` WHERE user_id=$1 AND team_id=$2 AND channel_id=$3 AND thread_ts=$4 ORDER BY created_at DESC LIMIT 20`, i.UserID, i.Team, i.Channel, i.Thread)
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

const editColumns = `payload,user_id,team_id,channel_id,thread_ts,event_id,status,id`

func (s *CodingStore) encodeEdit(e coding.Edit) (string, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return s.cipher.EncryptFor(string(raw), s.table("coding_edits")+":"+strconv.FormatInt(e.Task.Identity.UserID, 10)+":"+e.Task.ID)
}

func (s *CodingStore) scanEdit(row pgx.Row) (coding.Edit, error) {
	var e coding.Edit
	var identity coding.Identity
	var encrypted, status, id string
	err := row.Scan(&encrypted, &identity.UserID, &identity.Team, &identity.Channel, &identity.Thread, &identity.Event, &status, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, coding.ErrNotFound
	}
	if err != nil {
		return e, err
	}
	raw, err := s.cipher.DecryptFor(encrypted, s.table("coding_edits")+":"+strconv.FormatInt(identity.UserID, 10)+":"+id)
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return e, err
	}
	if e.Task.ID != id {
		return coding.Edit{}, coding.ErrDenied
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
	metadata, err := s.encodeEdit(coding.Edit{Task: e.Task, AccountID: e.AccountID})
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
	existing, err := s.scanEdit(tx.QueryRow(ctx, `SELECT `+editColumns+` FROM `+s.table("coding_edits")+` WHERE user_id=$1 AND team_id=$2 AND channel_id=$3 AND event_id=$4`, i.UserID, i.Team, i.Channel, i.Event))
	if err == nil {
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, coding.ErrNotFound) {
		return coding.Edit{}, err
	}
	// Recover abandoned clones without touching an actively leased operation.
	if _, err := tx.Exec(ctx, `UPDATE `+s.table("coding_edits")+` SET status='failed',updated_at=NOW() WHERE user_id=$1 AND status='cloning' AND updated_at<NOW()-INTERVAL '10 minutes' AND (edit_lease_until IS NULL OR edit_lease_until<NOW())`, i.UserID); err != nil {
		return coding.Edit{}, err
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM `+s.table("coding_edits")+` WHERE user_id=$1 AND status NOT IN ('pushed','cancelled','failed')`, i.UserID).Scan(&active); err != nil {
		return coding.Edit{}, err
	}
	if active >= 10 {
		return coding.Edit{}, errors.New("en fazla 10 açık kod görevi olabilir; önce mevcut bir görevi tamamla veya iptal et")
	}
	created, err := s.scanEdit(tx.QueryRow(ctx, `INSERT INTO `+s.table("coding_edits")+`(id,user_id,team_id,channel_id,thread_ts,event_id,status,payload,metadata)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT(user_id,team_id,channel_id,event_id) DO UPDATE SET event_id=`+s.table("coding_edits")+`.event_id
RETURNING `+editColumns, e.Task.ID, i.UserID, i.Team, i.Channel, i.Thread, i.Event, e.Task.Status, value, metadata))
	if err != nil {
		return coding.Edit{}, err
	}
	return created, tx.Commit(ctx)
}

func (s *CodingStore) GetEdit(ctx context.Context, userID int64, id string) (coding.Edit, error) {
	return s.scanEdit(s.db.QueryRow(ctx, `SELECT `+editColumns+` FROM `+s.table("coding_edits")+` WHERE id=$1 AND user_id=$2`, id, userID))
}

// WithEdit leases ownership using a short statement. No database connection is
// held while fn performs git or HTTP operations. All saves are fenced by claim.
func (s *CodingStore) WithEdit(ctx context.Context, userID int64, id string, fn func(*coding.Edit) error) error {
	claim, err := coding.RandomID()
	if err != nil {
		return err
	}
	var e coding.Edit
	for {
		e, err = s.scanEdit(s.db.QueryRow(ctx, `UPDATE `+s.table("coding_edits")+` SET edit_claim=$3,edit_lease_until=NOW()+INTERVAL '5 minutes'
		WHERE id=$1 AND user_id=$2 AND (edit_claim IS NULL OR edit_lease_until<NOW()) RETURNING `+editColumns, id, userID, claim))
		if !errors.Is(err, coding.ErrNotFound) {
			break
		}
		var exists bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+s.table("coding_edits")+` WHERE id=$1 AND user_id=$2)`, id, userID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return coding.ErrNotFound
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = s.db.Exec(cleanup, `UPDATE `+s.table("coding_edits")+` SET edit_claim=NULL,edit_lease_until=NULL WHERE id=$1 AND user_id=$2 AND edit_claim=$3`, id, userID, claim)
	}()
	if err := fn(&e); err != nil {
		return err
	}
	value, err := s.encodeEdit(e)
	if err != nil {
		return err
	}
	metadata, err := s.encodeEdit(coding.Edit{Task: e.Task, AccountID: e.AccountID})
	if err != nil {
		return err
	}
	result, err := s.db.Exec(ctx, `UPDATE `+s.table("coding_edits")+` SET payload=$3,status=$4,metadata=$6,updated_at=NOW(),edit_claim=NULL,edit_lease_until=NULL WHERE id=$1 AND user_id=$2 AND edit_claim=$5 AND edit_lease_until>NOW()`, id, userID, value, e.Task.Status, claim, metadata)
	if err == nil && result.RowsAffected() != 1 {
		return errors.New("edit ownership lost")
	}
	return err
}
