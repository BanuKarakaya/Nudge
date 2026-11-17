package slack

import "fmt"

// APIError retains the machine-readable Slack code without response bodies.
type APIError struct {
	Method string
	Code   string
	Status int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Slack %s failed: %s (HTTP %d)", e.Method, e.Code, e.Status)
}
func (e *APIError) Permanent() bool {
	if e.Status == 429 || e.Status >= 500 {
		return false
	}
	switch e.Code {
	case "cant_dm_bot", "user_not_found", "user_disabled", "account_inactive", "invalid_auth", "token_revoked", "not_authed", "missing_scope", "channel_not_found", "not_in_channel", "is_archived", "restricted_action", "no_permission":
		return true
	}
	return false
}
