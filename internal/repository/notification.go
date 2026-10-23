package repository

import (
	"context"
	"time"

	"nudge/internal/domain"
)

type NotificationMessage struct {
	Channel  string           `json:"channel"`
	Text     string           `json:"text,omitempty"`
	Bookmark *domain.Bookmark `json:"bookmark,omitempty"`
}
type NotificationDelivery struct {
	ID         int64
	UserID     int64
	ClaimToken string
	Attempts   int
	Message    NotificationMessage
}
type NotificationRepository interface {
	Exists(context.Context, int64, string, string) (bool, error)
	Enqueue(context.Context, int64, string, string, NotificationMessage) error
	ClaimNext(context.Context) (NotificationDelivery, error)
	Complete(context.Context, NotificationDelivery) error
	Retry(context.Context, NotificationDelivery, time.Duration) error
}
