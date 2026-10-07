package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/domain"
	"nudge/internal/repository"
)

var _ repository.InstallationRepository = (*InstallationRepository)(nil)

type InstallationRepository struct {
	db *pgxpool.Pool
}

func NewInstallationRepository(db *pgxpool.Pool) *InstallationRepository {
	return &InstallationRepository{db: db}
}

func (r *InstallationRepository) Save(ctx context.Context, installation domain.SlackInstallation) (int64, error) {
	const query = `
		WITH saved_installation AS (
			INSERT INTO slack_installations (team_id, team_name, bot_token)
			VALUES ($1, $2, $3)
			ON CONFLICT (team_id) DO UPDATE SET
				team_name = EXCLUDED.team_name,
				bot_token = EXCLUDED.bot_token
			RETURNING team_id
		)
		INSERT INTO users (team_id, slack_user_id)
		SELECT team_id, $4 FROM saved_installation
		ON CONFLICT (team_id, slack_user_id) DO UPDATE SET
			slack_user_id = EXCLUDED.slack_user_id
		RETURNING id`

	var userID int64
	err := r.db.QueryRow(ctx, query,
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
		WHERE u.id = $1`

	var installation domain.SlackInstallation
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&installation.TeamID,
		&installation.TeamName,
		&installation.BotToken,
		&installation.SlackUserID,
	)
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
