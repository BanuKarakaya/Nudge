package repository

import "context"

type NotificationRepository interface {
	Claim(ctx context.Context, userID int64, notificationType, periodKey string) (bool, error)
}
