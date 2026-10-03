package domain

import "time"

type RaindropConnection struct {
	UserID         int64
	RaindropUserID *int64
	AccessToken    string
	RefreshToken   string
	ExpiresAt      *time.Time
}
