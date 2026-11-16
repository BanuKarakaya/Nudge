package service

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"nudge/internal/feedbin"
)

var (
	errRSSUnavailable = errors.New("RSS service unavailable")
	errRSSConnection  = errors.New("RSS connection required")
	errRSSCredentials = errors.New("RSS credentials unavailable")
	errRSSAccess      = errors.New("RSS provider rejected access")
	errRSSRateLimit   = errors.New("RSS provider rate limited")
)

func rssConnectionFailure(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errRSSConnection
	}
	return errRSSCredentials
}
func rssProviderFailure(err error) error {
	var api *feedbin.APIError
	if errors.As(err, &api) {
		switch api.StatusCode {
		case 401, 403:
			return errRSSAccess
		case 429:
			return errRSSRateLimit
		}
	}
	return errRSSUnavailable
}
