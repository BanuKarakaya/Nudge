package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nudge/internal/domain"
	"nudge/internal/repository"
)

type endpointDigestFunc func(context.Context, int64, time.Time, time.Time) (string, error)

func (f endpointDigestFunc) Generate(ctx context.Context, userID int64, since, until time.Time) (string, error) {
	return f(ctx, userID, since, until)
}

type digestInstallations struct {
	repository.InstallationRepository
}

func (digestInstallations) GetByUserID(context.Context, int64) (domain.SlackInstallation, error) {
	return domain.SlackInstallation{BotToken: "token", SlackUserID: "U"}, nil
}

type digestSlack struct {
	opened, sent int
	text         string
}

func (s *digestSlack) OpenDirectMessage(context.Context, string, string) (string, error) {
	s.opened++
	return "D", nil
}

func (s *digestSlack) PostMessage(_ context.Context, _, _, text string) error {
	s.sent++
	s.text = text
	return nil
}

func TestFeedbinDigestEndpointDelegatesGeneration(t *testing.T) {
	loc := time.FixedZone("Istanbul", 3*60*60)
	for _, tc := range []struct {
		name, token   string
		err           error
		status, calls int
	}{
		{name: "success", token: "test", status: http.StatusOK, calls: 1},
		{name: "generation error", token: "test", err: errors.New("runtime unavailable"), status: http.StatusBadGateway, calls: 1},
		{name: "unauthorized", token: "wrong", status: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now()
			calls := 0
			generator := endpointDigestFunc(func(_ context.Context, userID int64, since, until time.Time) (string, error) {
				calls++
				if userID != 42 || until.Before(before) || until.After(time.Now()) || !since.Before(until) {
					t.Fatalf("invalid identity/window: %d [%s, %s)", userID, since, until)
				}
				expected := time.Date(until.Year(), until.Month(), until.Day(), 23, 0, 0, 0, loc)
				if until.Before(expected) {
					expected = expected.AddDate(0, 0, -1)
				}
				if !since.Equal(expected) {
					t.Fatalf("start=%s want=%s", since, expected)
				}
				return "Agent-written digest", tc.err
			})
			client := &digestSlack{}
			h := NewFeedbinDigestHandler(digestInstallations{}, generator, client, loc, "test")
			mux := http.NewServeMux()
			h.RegisterRoutes(mux)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/feedbin/test-digest", nil)
			request.Header.Set("X-Test-Token", tc.token)
			request.Header.Set("X-User-ID", "42")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != tc.status || calls != tc.calls {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
			}
			if tc.status == http.StatusOK {
				if client.opened != 1 || client.sent != 1 || client.text != "Agent-written digest" {
					t.Fatalf("unexpected delivery: %+v", client)
				}
			} else if client.opened != 0 || client.sent != 0 {
				t.Fatal("failed generation/auth delivered a digest")
			}
		})
	}
}
