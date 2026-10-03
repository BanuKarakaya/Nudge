package domain

import "time"

type Bookmark struct {
	ID                 int64
	UserID             int64
	RaindropBookmarkID int64
	Title              string
	URL                string
	Summary            string
	SavedAt            time.Time
	IsRead             bool
	ReadAt             *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
