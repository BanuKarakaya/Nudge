package repository

import (
	"context"
	"time"

	"nudge/internal/domain"
)

type BookmarkRepository interface {
	Create(ctx context.Context, bookmark domain.Bookmark) (*domain.Bookmark, error)
	GetByID(ctx context.Context, userID, bookmarkID int64) (*domain.Bookmark, error)
	ListByDate(ctx context.Context, userID int64, from, to time.Time) ([]domain.Bookmark, error)
	ListUnreadBetween(ctx context.Context, userID int64, from, to time.Time) ([]domain.Bookmark, error)
	MarkRead(ctx context.Context, userID, bookmarkID int64, readAt time.Time) error
	MarkUnread(ctx context.Context, userID, bookmarkID int64) error
}
