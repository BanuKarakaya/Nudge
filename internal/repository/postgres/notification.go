package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/repository"
)

var _ repository.NotificationRepository = (*NotificationRepository)(nil)

type NotificationRepository struct {
	db *pgxpool.Pool
}

func NewNotificationRepository(db *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{db: db}
}

func (r *NotificationRepository) Claim(ctx context.Context, userID int64, notificationType, periodKey string) (bool, error) {
	const query = `
		INSERT INTO notifications (user_id, notification_type, period_key)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, notification_type, period_key) DO NOTHING
		RETURNING id`

	var id int64
	err := r.db.QueryRow(ctx, query, userID, notificationType, periodKey).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
