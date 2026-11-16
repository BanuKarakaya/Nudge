package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"nudge/internal/feedbin"
	"strings"
	"testing"
	"time"
)

func TestRSSFailuresKeepSafeActionableCause(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		connection, provider error
		code                 string
	}{
		{"missing", pgx.ErrNoRows, nil, "rss_connection_required"},
		{"storage", errors.New("PRIVATE_KEY"), nil, "rss_credentials_unavailable"},
		{"auth", nil, &feedbin.APIError{StatusCode: 401}, "rss_access_denied"},
		{"forbidden", nil, &feedbin.APIError{StatusCode: 403}, "rss_access_denied"},
		{"rate_limit", nil, &feedbin.APIError{StatusCode: 429}, "rss_rate_limited"},
		{"outage", nil, &feedbin.APIError{StatusCode: 503}, "rss_unavailable"},
		{"network", nil, errors.New("PRIVATE_URL"), "rss_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := NewAgentTools(NewFeedbinDigestService(&agentConnectionFixture{err: tc.connection}, &agentFeedFixture{entryErr: tc.provider}), nil, nil, 42, time.Now(), time.UTC)
			output, err := tools.Call(context.Background(), "rss_inventory", json.RawMessage(`{"scope":"all_unread"}`))
			if err == nil {
				t.Fatal("failed RSS represented as success", output)
			}
			code, message := AgentToolFailure(err)
			if code != tc.code || strings.Contains(message+err.Error(), "PRIVATE") {
				t.Fatal("cause missing or leaked", code, message, err)
			}
		})
	}
}
