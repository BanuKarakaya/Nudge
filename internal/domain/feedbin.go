package domain

import "time"

// FeedbinConnection stores the credentials needed to read a user's Feedbin
// entries. Feedbin uses HTTP Basic Auth for its API.
type FeedbinConnection struct {
	UserID    int64
	Email     string
	Password  string
	CreatedAt time.Time
	UpdatedAt time.Time
}
