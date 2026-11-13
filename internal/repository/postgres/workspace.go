package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/workspace"
	"nudge/migrations"
)

type WorkspaceStore struct{ db *pgxpool.Pool }

func NewWorkspaceStore(ctx context.Context, db *pgxpool.Pool) (*WorkspaceStore, error) {
	err := migrations.Apply(ctx, db)
	if err != nil {
		return nil, err
	}
	return &WorkspaceStore{db}, nil
}
func (s *WorkspaceStore) Member(ctx context.Context, user int64) (workspace.Member, error) {
	var m workspace.Member
	err := s.db.QueryRow(ctx, `SELECT u.id,u.team_id,i.team_name,u.slack_user_id,COALESCE(i.installer_slack_user_id=u.slack_user_id,FALSE),COALESCE(i.installer_slack_user_id,'')='' FROM users u JOIN slack_installations i ON i.team_id=u.team_id AND i.bot_token<>'' WHERE u.id=$1`, user).Scan(&m.UserID, &m.Team, &m.Name, &m.SlackUser, &m.Manager, &m.OwnerMissing)
	return m, err
}
func (s *WorkspaceStore) Scope(ctx context.Context, user int64, kind string) (workspace.Scope, error) {
	var scope workspace.Scope
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT COALESCE(i.installer_slack_user_id=u.slack_user_id,FALSE),COALESCE(w.enabled,FALSE),COALESCE(w.resources,'[]'::jsonb) FROM users u JOIN slack_installations i ON i.team_id=u.team_id LEFT JOIN workspace_integrations w ON w.team_id=u.team_id AND w.provider=$2 WHERE u.id=$1`, user, kind).Scan(&scope.Manager, &scope.Enabled, &raw)
	if err == nil {
		err = json.Unmarshal(raw, &scope.Resources)
	}
	return scope, err
}
func (s *WorkspaceStore) SaveScope(ctx context.Context, user int64, kind string, scope workspace.Scope) error {
	if !workspace.ValidProvider(kind) || len(scope.Resources) > 2000 || (scope.Enabled && len(scope.Resources) == 0) {
		return workspace.ErrDenied
	}
	seen := map[string]bool{}
	for _, r := range scope.Resources {
		if r.ID == "" || len(r.ID) > 300 || len(r.Name) > 500 || seen[r.ID] {
			return workspace.ErrDenied
		}
		seen[r.ID] = true
	}
	if scope.Resources == nil {
		scope.Resources = []workspace.Resource{}
	}
	raw, err := json.Marshal(scope.Resources)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(ctx, `INSERT INTO workspace_integrations(team_id,provider,enabled,resources,updated_by)
 SELECT u.team_id,$2,$3,$4,u.id FROM users u JOIN slack_installations i ON i.team_id=u.team_id
 WHERE u.id=$1 AND i.installer_slack_user_id=u.slack_user_id
 ON CONFLICT(team_id,provider) DO UPDATE SET enabled=EXCLUDED.enabled,resources=EXCLUDED.resources,updated_by=EXCLUDED.updated_by,updated_at=NOW()`, user, kind, scope.Enabled, raw)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return workspace.ErrDenied
	}
	return nil
}
func (s *WorkspaceStore) CreateSession(ctx context.Context, user int64, hash string, expires time.Time, linkHash string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM workspace_sessions WHERE expires_at<=NOW()`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM workspace_used_links WHERE expires_at<=NOW()`); err != nil {
		return err
	}
	if linkHash != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO workspace_used_links(token_hash,expires_at) VALUES($1,NOW()+INTERVAL '21 minutes')`, linkHash); err != nil {
			return workspace.ErrDenied
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO workspace_sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, hash, user, expires); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *WorkspaceStore) SessionUser(ctx context.Context, hash string) (int64, error) {
	var user int64
	err := s.db.QueryRow(ctx, `SELECT user_id FROM workspace_sessions WHERE token_hash=$1 AND expires_at>NOW()`, hash).Scan(&user)
	return user, err
}
func (s *WorkspaceStore) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM workspace_sessions WHERE token_hash=$1`, hash)
	return err
}

var _ workspace.Store = (*WorkspaceStore)(nil)

func (s *WorkspaceStore) UseConnectionLink(ctx context.Context, hash string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO workspace_used_links(token_hash,expires_at) VALUES($1,NOW()+INTERVAL '21 minutes')`, hash)
	return err
}
