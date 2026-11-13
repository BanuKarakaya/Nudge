package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/secure"
)

var _ repository.InstallationRepository = (*InstallationRepository)(nil)

type InstallationRepository struct {
	db     *pgxpool.Pool
	cipher secure.Cipher
}

// One stable user per installed workspace is enough to discover its members.
func (r *InstallationRepository) ListWorkspaceSeeds(ctx context.Context) ([]int64, error) {
	rows, err := r.db.Query(ctx, `SELECT MIN(u.id) FROM users u JOIN slack_installations s ON s.team_id=u.team_id WHERE s.bot_token<>'' GROUP BY u.team_id ORDER BY MIN(u.id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func NewInstallationRepository(db *pgxpool.Pool, ciphers ...secure.Cipher) *InstallationRepository {
	r := &InstallationRepository{db: db}
	if len(ciphers) > 0 {
		r.cipher = ciphers[0]
	}
	return r
}

func (r *InstallationRepository) Save(ctx context.Context, installation domain.SlackInstallation) (int64, error) {
	encrypted, err := encryptToken(r.cipher, installation.BotToken, "slack:"+installation.TeamID)
	if err != nil {
		return 0, err
	}
	installation.BotToken = encrypted
	const query = `
		WITH saved_installation AS (
			INSERT INTO slack_installations (team_id, team_name, bot_token, installer_slack_user_id)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (team_id) DO UPDATE SET
				team_name = EXCLUDED.team_name,
 installed_at = NOW(),
				bot_token = EXCLUDED.bot_token,
				installer_slack_user_id = COALESCE(NULLIF(slack_installations.installer_slack_user_id, ''), EXCLUDED.installer_slack_user_id)
			RETURNING team_id
		)
		INSERT INTO users (team_id, slack_user_id)
		SELECT team_id, $4 FROM saved_installation
		ON CONFLICT (team_id, slack_user_id) DO UPDATE SET
			slack_user_id = EXCLUDED.slack_user_id
		RETURNING id`

	var userID int64
	err = r.db.QueryRow(ctx, query,
		installation.TeamID,
		installation.TeamName,
		installation.BotToken,
		installation.SlackUserID,
	).Scan(&userID)
	return userID, err
}

func (r *InstallationRepository) GetByUserID(ctx context.Context, userID int64) (domain.SlackInstallation, error) {
	const query = `
		SELECT si.team_id, si.team_name, si.bot_token, u.slack_user_id
		FROM users u
		JOIN slack_installations si ON si.team_id = u.team_id
		WHERE u.id = $1 AND si.bot_token<>''`

	var installation domain.SlackInstallation
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&installation.TeamID,
		&installation.TeamName,
		&installation.BotToken,
		&installation.SlackUserID,
	)
	if err == nil {
		installation.BotToken, err = decryptToken(r.cipher, installation.BotToken, "slack:"+installation.TeamID)
	}
	return installation, err
}

func (r *InstallationRepository) UpsertUser(ctx context.Context, teamID, slackUserID string) (int64, error) {
	const query = `
		INSERT INTO users (team_id, slack_user_id)
		VALUES ($1, $2)
		ON CONFLICT (team_id, slack_user_id) DO UPDATE SET
			slack_user_id = EXCLUDED.slack_user_id
		RETURNING id`

	var userID int64
	err := r.db.QueryRow(ctx, query, teamID, slackUserID).Scan(&userID)
	return userID, err
}

// SignInUser accepts a verified Slack identity only for an existing installation.
// Signing in cannot create an installation or claim its administrator role.
func (r *InstallationRepository) SignInUser(ctx context.Context, teamID, slackUserID string) (int64, error) {
	var id int64
	err := r.db.QueryRow(ctx, `INSERT INTO users (team_id, slack_user_id)
		SELECT team_id, $2 FROM slack_installations WHERE team_id=$1 AND bot_token<>'' AND $2<>''
		ON CONFLICT (team_id, slack_user_id) DO UPDATE SET slack_user_id=EXCLUDED.slack_user_id
		RETURNING id`, teamID, slackUserID).Scan(&id)
	return id, err
}

func (r *InstallationRepository) ListUsersByTeam(ctx context.Context, teamID string) ([]domain.WorkspaceUser, error) {
	const query = `
		SELECT id, team_id, slack_user_id
		FROM users
		WHERE team_id = $1
		ORDER BY id`

	rows, err := r.db.Query(ctx, query, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]domain.WorkspaceUser, 0)
	for rows.Next() {
		var user domain.WorkspaceUser
		if err := rows.Scan(&user.ID, &user.TeamID, &user.SlackUserID); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (r *InstallationRepository) GetUserIDBySlackUserID(ctx context.Context, teamID, slackUserID string) (int64, error) {
	const query = `
		SELECT id
		FROM users
		WHERE team_id = $1 AND slack_user_id = $2`

	var userID int64
	err := r.db.QueryRow(ctx, query, teamID, slackUserID).Scan(&userID)
	return userID, err
}

// Deactivate revokes local access atomically. User records are retained for an
// explicit export/delete request; no new invitations or jobs can use the token.
func (r *InstallationRepository) Deactivate(ctx context.Context, team string, eventTime int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	tag, err := tx.Exec(ctx, `UPDATE slack_installations SET bot_token='' WHERE team_id=$1 AND ($2::bigint=0 OR installed_at<=to_timestamp($2::double precision))`, team, eventTime)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	for _, query := range []string{
		`DELETE FROM workspace_sessions WHERE user_id IN (SELECT id FROM users WHERE team_id=$1)`,
		`UPDATE workspace_integrations SET enabled=FALSE WHERE team_id=$1`,
		`DELETE FROM notifications WHERE user_id IN (SELECT id FROM users WHERE team_id=$1) AND delivery_status IN ('pending','sending')`,
		`UPDATE nudge_slack_jobs SET status='failed',claim_token=NULL,lease_until=NULL,finished_at=NOW() WHERE team_id=$1 AND status IN ('queued','running')`,
	} {
		if _, err := tx.Exec(ctx, query, team); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
