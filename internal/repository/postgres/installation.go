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
