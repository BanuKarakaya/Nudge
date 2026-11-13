package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/secure"
	"nudge/internal/workspace"
	"nudge/migrations"
)

type WorkspaceAIStore struct {
	db     *pgxpool.Pool
	cipher secure.Cipher
}

func NewWorkspaceAIStore(ctx context.Context, db *pgxpool.Pool, cipher secure.Cipher) (*WorkspaceAIStore, error) {
	if _, err := cipher.Encrypt("probe"); err != nil {
		return nil, err
	}
	err := migrations.Apply(ctx, db)
	if err != nil {
		return nil, err
	}
	return &WorkspaceAIStore{db, cipher}, nil
}
func (s *WorkspaceAIStore) AISettings(ctx context.Context, user int64) (workspace.AISettings, error) {
	var out workspace.AISettings
	err := s.db.QueryRow(ctx, `SELECT COALESCE(a.provider,''),COALESCE(a.model,''),COALESCE(a.complex_model,''),COALESCE(a.search_model,''),COALESCE(a.has_key,FALSE),COALESCE(a.has_search_key,FALSE),COALESCE(a.revision,''),COALESCE(a.tool_models,'{}'::jsonb) FROM users u LEFT JOIN workspace_ai_settings a ON a.team_id=u.team_id WHERE u.id=$1`, user).Scan(&out.Provider, &out.Model, &out.ComplexModel, &out.SearchModel, &out.HasKey, &out.HasSearchKey, &out.Revision, &out.ToolModels)
	return out, err
}

// The encrypted envelope binds credentials to a team, so copying a row's ciphertext
// cannot silently make another workspace spend that account's balance.
type aiEnvelope struct{ Team, APIKey, SearchAPIKey string }

func (s *WorkspaceAIStore) AICredentials(ctx context.Context, user int64) (workspace.AICredentials, error) {
	var out workspace.AICredentials
	var team, encrypted string
	err := s.db.QueryRow(ctx, `SELECT u.team_id,a.provider,a.model,a.complex_model,a.search_model,a.has_key,a.has_search_key,a.revision,a.credentials,a.tool_models FROM users u JOIN workspace_ai_settings a ON a.team_id=u.team_id WHERE u.id=$1`, user).Scan(&team, &out.Provider, &out.Model, &out.ComplexModel, &out.SearchModel, &out.HasKey, &out.HasSearchKey, &out.Revision, &encrypted, &out.ToolModels)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, workspace.ErrAINotConfigured
	}
	if err != nil {
		return out, err
	}
	raw, err := s.cipher.DecryptFor(encrypted, "workspace-ai:"+team)
	if err != nil {
		return workspace.AICredentials{}, workspace.ErrDenied
	}
	var envelope aiEnvelope
	if json.Unmarshal([]byte(raw), &envelope) != nil || envelope.Team != team {
		return workspace.AICredentials{}, workspace.ErrDenied
	}
	out.APIKey = envelope.APIKey
	out.SearchAPIKey = envelope.SearchAPIKey
	return out, nil
}
func (s *WorkspaceAIStore) SaveAI(ctx context.Context, user int64, c workspace.AICredentials, expected string) error {
	if !workspace.ValidToolModels(c.ToolModels) || (c.APIKey != "" && workspace.ProviderURL(c.Provider) == "") || (c.Model != "" && (!workspace.ValidModel(c.Model) || c.APIKey == "")) || (c.ComplexModel != "" && (!workspace.ValidModel(c.ComplexModel) || c.Model == "")) || (c.SearchAPIKey != "" && !workspace.ValidModel(c.SearchModel)) || len(c.APIKey) > 4096 || len(c.SearchAPIKey) > 4096 || strings.ContainsAny(c.APIKey+c.SearchAPIKey, "\r\n\x00") {
		return workspace.ErrDenied
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var team string
	// Lock the installation to serialize first-save, update and removal alike.
	err = tx.QueryRow(ctx, `SELECT i.team_id FROM users u JOIN slack_installations i ON i.team_id=u.team_id WHERE u.id=$1 AND i.installer_slack_user_id=u.slack_user_id FOR UPDATE OF i`, user).Scan(&team)
	if errors.Is(err, pgx.ErrNoRows) {
		return workspace.ErrDenied
	}
	if err != nil {
		return err
	}
	var revision string
	err = tx.QueryRow(ctx, `SELECT revision FROM workspace_ai_settings WHERE team_id=$1`, team).Scan(&revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if revision != expected {
		return workspace.ErrAIConflict
	}
	raw, _ := json.Marshal(aiEnvelope{team, c.APIKey, c.SearchAPIKey})
	encrypted, err := s.cipher.EncryptFor(string(raw), "workspace-ai:"+team)
	if err != nil {
		return err
	}
	if c.ToolModels == nil {
		c.ToolModels = map[string]string{}
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workspace_ai_settings(team_id,provider,model,complex_model,search_model,credentials,has_key,has_search_key,revision,updated_by,tool_models) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(team_id) DO UPDATE SET provider=EXCLUDED.provider,model=EXCLUDED.model,complex_model=EXCLUDED.complex_model,search_model=EXCLUDED.search_model,credentials=EXCLUDED.credentials,has_key=EXCLUDED.has_key,has_search_key=EXCLUDED.has_search_key,revision=EXCLUDED.revision,updated_by=EXCLUDED.updated_by,tool_models=EXCLUDED.tool_models,updated_at=NOW()`, team, c.Provider, c.Model, c.ComplexModel, c.SearchModel, encrypted, c.APIKey != "", c.SearchAPIKey != "", hex.EncodeToString(nonce), user, c.ToolModels)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Member listing is limited to the requesting manager's installation.
func (s *WorkspaceStore) Members(ctx context.Context, user int64) ([]workspace.Member, error) {
	m, err := s.Member(ctx, user)
	if err != nil {
		return nil, err
	}
	if !m.Manager {
		return nil, workspace.ErrDenied
	}
	rows, err := s.db.Query(ctx, `SELECT u.id,u.slack_user_id,COALESCE(i.installer_slack_user_id=u.slack_user_id,FALSE) FROM users u JOIN slack_installations i ON i.team_id=u.team_id WHERE u.team_id=$1 ORDER BY u.id LIMIT 1000`, m.Team)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workspace.Member{}
	for rows.Next() {
		v := workspace.Member{Team: m.Team}
		if err := rows.Scan(&v.UserID, &v.SlackUser, &v.Manager); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
