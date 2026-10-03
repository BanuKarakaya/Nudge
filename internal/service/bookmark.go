package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"nudge/internal/domain"
	"nudge/internal/repository"
)

var (
	ErrInvalidBookmark  = errors.New("invalid bookmark")
	ErrInvalidDateRange = errors.New("invalid date range")
)

type BookmarkService interface {
	Create(ctx context.Context, bookmark domain.Bookmark) (*domain.Bookmark, error)
	GetByID(ctx context.Context, userID, bookmarkID int64) (*domain.Bookmark, error)
	ListByDate(ctx context.Context, userID int64, from, to time.Time) ([]domain.Bookmark, error)
	ListUnreadBetween(ctx context.Context, userID int64, from, to time.Time) ([]domain.Bookmark, error)
	MarkRead(ctx context.Context, userID, bookmarkID int64) error
	MarkUnread(ctx context.Context, userID, bookmarkID int64) error
}

type bookmarkService struct {
	repository repository.BookmarkRepository
}

func NewBookmarkService(bookmarkRepository repository.BookmarkRepository) BookmarkService {
	return &bookmarkService{repository: bookmarkRepository}
}

func (s *bookmarkService) Create(ctx context.Context, bookmark domain.Bookmark) (*domain.Bookmark, error) {
	if err := validateBookmark(bookmark); err != nil {
		return nil, err
	}

	bookmark.Title = strings.TrimSpace(bookmark.Title)
	bookmark.URL = strings.TrimSpace(bookmark.URL)
	return s.repository.Create(ctx, bookmark)
}

func (s *bookmarkService) GetByID(ctx context.Context, userID, bookmarkID int64) (*domain.Bookmark, error) {
	if userID <= 0 || bookmarkID <= 0 {
		return nil, fmt.Errorf("%w: user and bookmark IDs must be positive", ErrInvalidBookmark)
	}
	return s.repository.GetByID(ctx, userID, bookmarkID)
}

func (s *bookmarkService) ListByDate(ctx context.Context, userID int64, from, to time.Time) ([]domain.Bookmark, error) {
	if err := validateDateRange(userID, from, to); err != nil {
		return nil, err
	}
	return s.repository.ListByDate(ctx, userID, from, to)
}

func (s *bookmarkService) ListUnreadBetween(ctx context.Context, userID int64, from, to time.Time) ([]domain.Bookmark, error) {
	if err := validateDateRange(userID, from, to); err != nil {
		return nil, err
	}
	return s.repository.ListUnreadBetween(ctx, userID, from, to)
}

func (s *bookmarkService) MarkRead(ctx context.Context, userID, bookmarkID int64) error {
	if userID <= 0 || bookmarkID <= 0 {
		return fmt.Errorf("%w: user and bookmark IDs must be positive", ErrInvalidBookmark)
	}
	return s.repository.MarkRead(ctx, userID, bookmarkID, time.Now().UTC())
}

func (s *bookmarkService) MarkUnread(ctx context.Context, userID, bookmarkID int64) error {
	if userID <= 0 || bookmarkID <= 0 {
		return fmt.Errorf("%w: user and bookmark IDs must be positive", ErrInvalidBookmark)
	}
	return s.repository.MarkUnread(ctx, userID, bookmarkID)
}

func validateBookmark(bookmark domain.Bookmark) error {
	if bookmark.UserID <= 0 {
		return fmt.Errorf("%w: user ID must be positive", ErrInvalidBookmark)
	}
	if bookmark.RaindropBookmarkID <= 0 {
		return fmt.Errorf("%w: Raindrop bookmark ID must be positive", ErrInvalidBookmark)
	}
	if strings.TrimSpace(bookmark.Title) == "" {
		return fmt.Errorf("%w: title is required", ErrInvalidBookmark)
	}
	if bookmark.SavedAt.IsZero() {
		return fmt.Errorf("%w: saved_at is required", ErrInvalidBookmark)
	}

	parsedURL, err := url.Parse(strings.TrimSpace(bookmark.URL))
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("%w: URL must be valid", ErrInvalidBookmark)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("%w: URL must use http or https", ErrInvalidBookmark)
	}
	return nil
}

func validateDateRange(userID int64, from, to time.Time) error {
	if userID <= 0 {
		return fmt.Errorf("%w: user ID must be positive", ErrInvalidBookmark)
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return ErrInvalidDateRange
	}
	return nil
}
