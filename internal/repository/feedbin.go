package repository

import (
	"context"

	"nudge/internal/domain"
)

type FeedbinConnectionRepository interface {
	Save(ctx context.Context, connection domain.FeedbinConnection) error
	GetByUserID(ctx context.Context, userID int64) (domain.FeedbinConnection, error)
}
