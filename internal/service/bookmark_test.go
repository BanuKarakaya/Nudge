package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"nudge/internal/domain"
)

type fakeBookmarkRepository struct {
	created  *domain.Bookmark
	readAt   time.Time
	markRead bool
}

func (f *fakeBookmarkRepository) Create(_ context.Context, bookmark domain.Bookmark) (*domain.Bookmark, error) {
	f.created = &bookmark
	return &bookmark, nil
}

func (f *fakeBookmarkRepository) GetByID(context.Context, int64, int64) (*domain.Bookmark, error) {
	return nil, nil
}

func (f *fakeBookmarkRepository) ListByDate(context.Context, int64, time.Time, time.Time) ([]domain.Bookmark, error) {
	return nil, nil
}

func (f *fakeBookmarkRepository) ListUnreadBetween(context.Context, int64, time.Time, time.Time) ([]domain.Bookmark, error) {
	return nil, nil
}

func (f *fakeBookmarkRepository) MarkRead(_ context.Context, _ int64, _ int64, readAt time.Time) error {
	f.markRead = true
	f.readAt = readAt
	return nil
}

func (f *fakeBookmarkRepository) MarkUnread(context.Context, int64, int64) error {
	return nil
}

func TestBookmarkServiceCreateTrimsFields(t *testing.T) {
	repo := &fakeBookmarkRepository{}
	svc := NewBookmarkService(repo)

	bookmark, err := svc.Create(context.Background(), domain.Bookmark{
		UserID:             1,
		RaindropBookmarkID: 42,
		Title:              "  Go documentation  ",
		URL:                " https://go.dev/doc/ ",
		SavedAt:            time.Now(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if bookmark.Title != "Go documentation" || bookmark.URL != "https://go.dev/doc/" {
		t.Fatalf("Create() did not trim fields: %+v", bookmark)
	}
}

func TestBookmarkServiceCreateRejectsInvalidURL(t *testing.T) {
	svc := NewBookmarkService(&fakeBookmarkRepository{})

	_, err := svc.Create(context.Background(), domain.Bookmark{
		UserID:             1,
		RaindropBookmarkID: 42,
		Title:              "A bookmark",
		URL:                "not-a-url",
		SavedAt:            time.Now(),
	})
	if !errors.Is(err, ErrInvalidBookmark) {
		t.Fatalf("Create() error = %v, want ErrInvalidBookmark", err)
	}
}

func TestBookmarkServiceMarkReadSetsTimestamp(t *testing.T) {
	repo := &fakeBookmarkRepository{}
	svc := NewBookmarkService(repo)

	if err := svc.MarkRead(context.Background(), 1, 42); err != nil {
		t.Fatalf("MarkRead() error = %v", err)
	}
	if !repo.markRead || repo.readAt.IsZero() {
		t.Fatal("MarkRead() did not pass a read timestamp to the repository")
	}
}
