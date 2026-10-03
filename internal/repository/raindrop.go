package repository

import (
	"context"

	"nudge/internal/domain"
)

type RaindropConnectionRepository interface {
	Save(ctx context.Context, connection domain.RaindropConnection) error
	GetByUserID(ctx context.Context, userID int64) (domain.RaindropConnection, error)
}
