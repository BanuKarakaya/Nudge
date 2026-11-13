package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/secure"
	"nudge/internal/service"
	"nudge/internal/workspace"
)

type ActionStore struct {
	db     *pgxpool.Pool
	cipher secure.Cipher
}

func NewActionStore(db *pgxpool.Pool, cipher secure.Cipher) *ActionStore {
	return &ActionStore{db, cipher}
}
func (s *ActionStore) ActionMode(ctx context.Context, user int64, tool string) (string, error) {
	var mode string
	err := s.db.QueryRow(ctx, `SELECT COALESCE(p.mode,'approval') FROM users u JOIN slack_installations i ON i.team_id=u.team_id AND i.bot_token<>'' LEFT JOIN nudge_action_policies p ON p.team_id=u.team_id AND p.tool=$2 WHERE u.id=$1`, user, tool).Scan(&mode)
	return mode, err
}
func (s *ActionStore) SetActionMode(ctx context.Context, user int64, tool, mode string) error {
	if !service.MutatingTool(tool) || len(tool) > 100 || (mode != "auto" && mode != "approval" && mode != "disabled") {
		return workspace.ErrDenied
	}
	tag, err := s.db.Exec(ctx, `INSERT INTO nudge_action_policies(team_id,tool,mode,updated_by) SELECT u.team_id,$2,$3,u.id FROM users u JOIN slack_installations i ON i.team_id=u.team_id WHERE u.id=$1 AND i.bot_token<>'' AND i.installer_slack_user_id=u.slack_user_id ON CONFLICT(team_id,tool) DO UPDATE SET mode=EXCLUDED.mode,updated_by=EXCLUDED.updated_by`, user, tool, mode)
	if err == nil && tag.RowsAffected() != 1 {
		return workspace.ErrDenied
	}
	return err
}
func (s *ActionStore) ProposeAction(ctx context.Context, p service.ActionProposal) (service.ActionProposal, error) {
	if len(p.Arguments) > 16000 || !json.Valid(p.Arguments) || p.Identity.Event == "" {
		return p, errors.New("onay isteği geçersiz veya çok uzun")
	}
	var canonical any
	_ = json.Unmarshal(p.Arguments, &canonical)
	p.Arguments, _ = json.Marshal(canonical)
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	p.ID = hex.EncodeToString(sum[:])
	raw, _ = json.Marshal(p)
	encrypted, err := s.cipher.EncryptFor(string(raw), "action:"+p.ID)
	if err != nil {
		return p, err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO nudge_action_proposals(id,user_id,team_id,channel_id,thread_ts,tool,payload) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, p.ID, p.Identity.UserID, p.Identity.Team, p.Identity.Channel, p.Identity.Thread, p.Tool, encrypted)
	return p, err
}
func (s *ActionStore) ApproveAction(ctx context.Context, id string, user int64, team, channel, thread string) error {
	tag, err := s.db.Exec(ctx, `UPDATE nudge_action_proposals SET status='approved' WHERE id=$1 AND user_id=$2 AND team_id=$3 AND channel_id=$4 AND thread_ts=$5 AND status IN ('pending','approved') AND expires_at>NOW()`, id, user, team, channel, thread)
	if err == nil && tag.RowsAffected() != 1 {
		return workspace.ErrDenied
	}
	return err
}
func (s *ActionStore) StartAction(ctx context.Context, id string, user int64) (service.ActionProposal, error) {
	var p service.ActionProposal
	var encrypted string
	err := s.db.QueryRow(ctx, `UPDATE nudge_action_proposals SET status='executing' WHERE id=$1 AND user_id=$2 AND status='approved' AND expires_at>NOW() RETURNING payload`, id, user).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, service.ErrApprovalUnavailable
	}
	if err != nil {
		return p, err
	}
	raw, err := s.cipher.DecryptFor(encrypted, "action:"+id)
	if err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(raw), &p)
	if err == nil && (p.ID != id || p.Identity.UserID != user) {
		return p, workspace.ErrDenied
	}
	return p, err
}
func (s *ActionStore) FinishAction(ctx context.Context, id string, success bool) error {
	status := "failed"
	if success {
		status = "done"
	}
	_, err := s.db.Exec(ctx, `WITH done AS (UPDATE nudge_action_proposals SET status=$2 WHERE id=$1 AND status='executing' RETURNING team_id,user_id,tool) INSERT INTO nudge_audit_log(team_id,actor_id,action,target) SELECT team_id,user_id,'action.'||$2,tool FROM done`, id, status)
	return err
}
