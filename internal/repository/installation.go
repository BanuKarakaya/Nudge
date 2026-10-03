package repository

import (
	"context"

	"nudge/internal/domain"
)

type InstallationRepository interface {
	Save(ctx context.Context, installation domain.SlackInstallation) (int64, error)
	GetByUserID(ctx context.Context, userID int64) (domain.SlackInstallation, error)
}
