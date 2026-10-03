package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/domain"
	"nudge/internal/repository"
)

var _ repository.BookmarkRepository = (*BookmarkRepository)(nil)

type BookmarkRepository struct {
	db *pgxpool.Pool
}

func NewBookmarkRepository(db *pgxpool.Pool) *BookmarkRepository {
	return &BookmarkRepository{db: db}
}

func (r *BookmarkRepository) Create(ctx context.Context, bookmark domain.Bookmark) (*domain.Bookmark, error) {
	const query = `
		INSERT INTO bookmarks (
			user_id, raindrop_bookmark_id, title, url, summary, saved_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, raindrop_bookmark_id) DO UPDATE SET
			title = EXCLUDED.title,
			url = EXCLUDED.url,
			summary = EXCLUDED.summary,
			updated_at = NOW()
		RETURNING id, user_id, raindrop_bookmark_id, title, url,
			summary, saved_at, is_read, read_at, created_at, updated_at`

	return scanBookmark(r.db.QueryRow(ctx, query,
		bookmark.UserID,
		bookmark.RaindropBookmarkID,
		bookmark.Title,
		bookmark.URL,
		bookmark.Summary,
		bookmark.SavedAt,
	))
}

func (r *BookmarkRepository) GetByID(ctx context.Context, userID, bookmarkID int64) (*domain.Bookmark, error) {
	const query = `
		SELECT id, user_id, raindrop_bookmark_id, title, url,
			summary, saved_at, is_read, read_at, created_at, updated_at
		FROM bookmarks
		WHERE user_id = $1 AND id = $2`

	return scanBookmark(r.db.QueryRow(ctx, query, userID, bookmarkID))
}

func (r *BookmarkRepository) ListByDate(ctx context.Context, userID int64, from, to time.Time) ([]domain.Bookmark, error) {
	const query = `
		SELECT id, user_id, raindrop_bookmark_id, title, url,
			summary, saved_at, is_read, read_at, created_at, updated_at
		FROM bookmarks
		WHERE user_id = $1 AND saved_at >= $2 AND saved_at < $3
		ORDER BY saved_at ASC`

	return r.list(ctx, query, userID, from, to)
}

func (r *BookmarkRepository) ListUnreadBetween(ctx context.Context, userID int64, from, to time.Time) ([]domain.Bookmark, error) {
	const query = `
		SELECT id, user_id, raindrop_bookmark_id, title, url,
			summary, saved_at, is_read, read_at, created_at, updated_at
		FROM bookmarks
		WHERE user_id = $1
		  AND saved_at >= $2 AND saved_at < $3
		  AND is_read = FALSE
		ORDER BY saved_at ASC`

	return r.list(ctx, query, userID, from, to)
}

func (r *BookmarkRepository) MarkRead(ctx context.Context, userID, bookmarkID int64, readAt time.Time) error {
	const query = `
		UPDATE bookmarks
		SET is_read = TRUE, read_at = $3, updated_at = NOW()
		WHERE user_id = $1 AND id = $2`

	result, err := r.db.Exec(ctx, query, userID, bookmarkID, readAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *BookmarkRepository) MarkUnread(ctx context.Context, userID, bookmarkID int64) error {
	const query = `
		UPDATE bookmarks
		SET is_read = FALSE, read_at = NULL, updated_at = NOW()
		WHERE user_id = $1 AND id = $2`

	result, err := r.db.Exec(ctx, query, userID, bookmarkID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *BookmarkRepository) list(ctx context.Context, query string, args ...any) ([]domain.Bookmark, error) {
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bookmarks := make([]domain.Bookmark, 0)
	for rows.Next() {
		bookmark, err := scanBookmarkRow(rows)
		if err != nil {
			return nil, err
		}
		bookmarks = append(bookmarks, *bookmark)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bookmarks, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBookmark(row rowScanner) (*domain.Bookmark, error) {
	bookmark, err := scanBookmarkRow(row)
	if err != nil {
		return nil, err
	}
	return bookmark, nil
}

func scanBookmarkRow(row rowScanner) (*domain.Bookmark, error) {
	bookmark := &domain.Bookmark{}
	err := row.Scan(
		&bookmark.ID,
		&bookmark.UserID,
		&bookmark.RaindropBookmarkID,
		&bookmark.Title,
		&bookmark.URL,
		&bookmark.Summary,
		&bookmark.SavedAt,
		&bookmark.IsRead,
		&bookmark.ReadAt,
		&bookmark.CreatedAt,
		&bookmark.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return bookmark, nil
}
