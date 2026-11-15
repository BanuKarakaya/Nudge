package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/connector"
	"nudge/internal/secure"
	"nudge/internal/workspace"
)

type ConnectorStore struct {
	db     *pgxpool.Pool
	cipher secure.Cipher
}

func NewConnectorStore(db *pgxpool.Pool, cipher secure.Cipher) *ConnectorStore {
	return &ConnectorStore{db, cipher}
}
func (s *ConnectorStore) List(ctx context.Context, user int64) ([]connector.Connection, error) {
	rows, err := s.db.Query(ctx, `SELECT c.id,c.team_id,c.name,c.endpoint,c.revision,c.enabled,c.tools FROM workspace_mcp_connections c JOIN users u ON u.team_id=c.team_id JOIN slack_installations i ON i.team_id=u.team_id AND i.bot_token<>'' WHERE u.id=$1 ORDER BY c.name,c.id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []connector.Connection{}
	for rows.Next() {
		var c connector.Connection
		var raw []byte
		if err = rows.Scan(&c.ID, &c.Team, &c.Name, &c.Endpoint, &c.Revision, &c.Enabled, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &c.Tools); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *ConnectorStore) Secret(ctx context.Context, user int64, id, revision string) (string, error) {
	var team, encrypted string
	err := s.db.QueryRow(ctx, `SELECT c.team_id,c.credentials FROM workspace_mcp_connections c JOIN users u ON u.team_id=c.team_id JOIN slack_installations i ON i.team_id=u.team_id AND i.bot_token<>'' WHERE u.id=$1 AND c.id=$2 AND c.revision=$3`, user, id, revision).Scan(&team, &encrypted)
	if err != nil {
		return "", workspace.ErrDenied
	}
	return s.cipher.DecryptFor(encrypted, "mcp:"+team+":"+id+":"+revision)
}
func (s *ConnectorStore) Save(ctx context.Context, user int64, c connector.Connection, token, expected string) error {
	if len(c.ID) != 32 || len(c.Revision) != 32 || len(c.Name) < 1 || len(c.Name) > 80 || len(c.Tools) > 32 || token == "" || len(token) > 4096 {
		return workspace.ErrDenied
	}
	raw, err := json.Marshal(c.Tools)
	if err != nil || len(raw) > 524288 {
		return workspace.ErrDenied
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var team string
	err = tx.QueryRow(ctx, `SELECT i.team_id FROM users u JOIN slack_installations i ON i.team_id=u.team_id WHERE u.id=$1 AND i.bot_token<>'' AND i.installer_slack_user_id=u.slack_user_id FOR UPDATE OF i`, user).Scan(&team)
	if err != nil || team != c.Team {
		return workspace.ErrDenied
	}
	var revision string
	err = tx.QueryRow(ctx, `SELECT revision FROM workspace_mcp_connections WHERE team_id=$1 AND id=$2`, team, c.ID).Scan(&revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if revision != expected {
		return connector.ErrConflict
	}
	if revision == "" {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM workspace_mcp_connections WHERE team_id=$1`, team).Scan(&count); err != nil {
			return err
		}
		if count >= 5 {
			return workspace.ErrDenied
		}
	}
	encrypted, err := s.cipher.EncryptFor(token, "mcp:"+team+":"+c.ID+":"+c.Revision)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workspace_mcp_connections(team_id,id,name,endpoint,revision,enabled,tools,credentials) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(team_id,id) DO UPDATE SET name=EXCLUDED.name,endpoint=EXCLUDED.endpoint,revision=EXCLUDED.revision,enabled=EXCLUDED.enabled,tools=EXCLUDED.tools,credentials=EXCLUDED.credentials,updated_at=NOW()`, team, c.ID, c.Name, c.Endpoint, c.Revision, c.Enabled, raw, encrypted)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO nudge_audit_log(team_id,actor_id,action,target) VALUES($1,$2,'mcp.saved',$3)`, team, user, c.ID+":"+c.Revision); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *ConnectorStore) Delete(ctx context.Context, user int64, id, revision string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var team string
	err = tx.QueryRow(ctx, `SELECT i.team_id FROM users u JOIN slack_installations i ON i.team_id=u.team_id WHERE u.id=$1 AND i.bot_token<>'' AND i.installer_slack_user_id=u.slack_user_id FOR UPDATE OF i`, user).Scan(&team)
	if err != nil {
		return workspace.ErrDenied
	}
	tag, err := tx.Exec(ctx, `DELETE FROM workspace_mcp_connections WHERE team_id=$1 AND id=$2 AND revision=$3`, team, id, revision)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return connector.ErrConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO nudge_audit_log(team_id,actor_id,action,target) VALUES($1,$2,'mcp.deleted',$3)`, team, user, id+":"+revision); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
