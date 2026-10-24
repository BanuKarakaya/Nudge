package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/slack"
	"strings"
	"testing"
)

type onboardingInstallations struct {
	repository.InstallationRepository
	saved bool
}

func (s *onboardingInstallations) Save(_ context.Context, i domain.SlackInstallation) (int64, error) {
	if i.TeamID != "T" || i.SlackUserID != "U" {
		return 0, errors.New("wrong identity")
	}
	s.saved = true
	return 41, nil
}

type onboardingHook struct {
	ids   []int64
	store *onboardingInstallations
}

func (s *onboardingHook) EnqueueWorkspace(_ context.Context, id int64) error {
	if !s.store.saved {
		return errors.New("enqueued before save")
	}
	s.ids = append(s.ids, id)
	return nil
}

type onboardingTransport func(*http.Request) (*http.Response, error)

func (f onboardingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSlackInstallationAutomaticallyQueuesInvitations(t *testing.T) {
	store := &onboardingInstallations{}
	hook := &onboardingHook{store: store}
	oauth := slack.OAuthClient{ClientID: "client", RedirectURL: "https://nudgy.example/slack/oauth/callback", HTTPClient: &http.Client{Transport: onboardingTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"access_token":"bot","team":{"id":"T","name":"Workspace"},"authed_user":{"id":"U"}}`)), Header: make(http.Header)}, nil
	})}}
	h := NewSlackHandler(oauth, store).WithOnboarding(hook)
	start := httptest.NewRecorder()
	h.install(start, httptest.NewRequest("GET", "https://nudgy.example/slack/install", nil))
	u, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u.Query().Get("scope"), "im:write") {
		t.Fatal("missing DM scope")
	}
	callback := "https://nudgy.example/slack/oauth/callback?code=valid&state=" + url.QueryEscape(u.Query().Get("state"))
	forged := httptest.NewRecorder()
	h.callback(forged, httptest.NewRequest("GET", callback, nil))
	if forged.Code != 400 || len(hook.ids) != 0 {
		t.Fatal("unbound callback scheduled messages")
	}
	req := httptest.NewRequest("GET", callback, nil)
	for _, cookie := range start.Result().Cookies() {
		req.AddCookie(cookie)
	}
	result := httptest.NewRecorder()
	h.callback(result, req)
	if result.Code != 200 || len(hook.ids) != 1 || hook.ids[0] != 41 {
		t.Fatalf("missing installer discovery: status=%d ids=%v", result.Code, hook.ids)
	}
	replay := httptest.NewRecorder()
	h.callback(replay, req)
	if replay.Code != 400 || len(hook.ids) != 1 {
		t.Fatal("OAuth replay scheduled messages")
	}
}
