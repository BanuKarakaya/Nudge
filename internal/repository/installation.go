package repository

import (
	"context"

	"nudge/internal/domain"
)

type InstallationRepository interface {
	Save(ctx context.Context, installation domain.SlackInstallation) (int64, error)
	GetByUserID(ctx context.Context, userID int64) (domain.SlackInstallation, error)
	UpsertUser(ctx context.Context, teamID, slackUserID string) (int64, error)
	ListUsersByTeam(ctx context.Context, teamID string) ([]domain.WorkspaceUser, error)
}
