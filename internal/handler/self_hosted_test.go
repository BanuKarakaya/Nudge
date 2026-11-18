package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"nudge/internal/repository"
	"nudge/internal/slack"
)

type restrictedInstallations struct {
	repository.InstallationRepository
	team string
}

func (s restrictedInstallations) AllowedWorkspace() (string, bool) { return s.team, true }

func TestSelfHostedInstallationChecksVerifiedWorkspace(t *testing.T) {
	for _, team := range []string{"T", "OTHER"} {
		t.Run(team, func(t *testing.T) {
			store := &onboardingInstallations{}
			hook := &onboardingHook{store: store}
			oauth := slack.OAuthClient{ClientID: "client", RedirectURL: "https://nudgy.test/slack/oauth/callback", HTTPClient: &http.Client{Transport: onboardingTransport(func(*http.Request) (*http.Response, error) {
				body := fmt.Sprintf(`{"ok":true,"access_token":"synthetic","team":{"id":%q},"authed_user":{"id":"U"}}`, team)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			h := NewSlackHandler(oauth, restrictedInstallations{store, "T"}).WithOnboarding(hook)
			start := httptest.NewRecorder()
			h.install(start, httptest.NewRequest("GET", "/slack/install?team=OTHER", nil))
			u, _ := url.Parse(start.Header().Get("Location"))
			if start.Code != http.StatusFound || u.Query().Get("team") != "T" {
				t.Fatal("caller changed the operator workspace")
			}
			r := httptest.NewRequest("GET", "/slack/oauth/callback?code=code&team=T&state="+u.Query().Get("state"), nil)
			for _, c := range start.Result().Cookies() {
				r.AddCookie(c)
			}
			w := httptest.NewRecorder()
			h.callback(w, r)
			if team == "OTHER" {
				if w.Code != http.StatusForbidden || store.saved || len(hook.ids) != 0 {
					t.Fatalf("foreign OAuth saved a token or scheduled onboarding: %d", w.Code)
				}
			} else if w.Code != http.StatusOK || !store.saved || len(hook.ids) != 1 {
				t.Fatalf("owner installation broken: %d", w.Code)
			}
		})
	}
	// A fresh server cannot be claimed by its first visitor.
	h := NewSlackHandler(slack.OAuthClient{ClientID: "client", RedirectURL: "https://nudgy.test/slack/oauth/callback"}, restrictedInstallations{})
	w := httptest.NewRecorder()
	h.install(w, httptest.NewRequest("GET", "/slack/install?team=T", nil))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Location") != "" || len(w.Result().Cookies()) != 0 {
		t.Fatal("unconfigured server offered public signup")
	}
}

func TestSelfHostedRejectsForeignSlackTrafficAndQueuedWork(t *testing.T) {
	repo := restrictedInstallations{team: "T"}
	h := &SlackEventHandler{signingSecret: "signing", installations: repo}
	body := `{"type":"event_callback","team_id":"OTHER","event_id":"event","event":{"type":"app_mention","user":"U","channel":"C","ts":"1","text":"hello"}}`
	r := httptest.NewRequest("POST", "/slack/events", strings.NewReader(body))
	signSlackRequest(r, body)
	w := httptest.NewRecorder()
	h.handle(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("foreign event reached queue", w.Code)
	}
	err := h.processSlackEvent(context.Background(), slackEventEnvelope{TeamID: "OTHER"}, nil)
	if !errors.Is(err, repository.ErrWorkspaceNotAllowed) {
		t.Fatal("historical foreign job was processed", err)
	}
	interaction := &SlackInteractionHandler{signingSecret: "signing", installations: repo}
	body = url.Values{"payload": {`{"team":{"id":"OTHER"},"actions":[{"action_id":"nudgy_action_approve"}]}`}}.Encode()
	r = httptest.NewRequest("POST", "/slack/interactions", strings.NewReader(body))
	signSlackRequest(r, body)
	w = httptest.NewRecorder()
	interaction.handle(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("foreign interaction reached approval handler", w.Code)
	}
}

func TestSelfHostedRejectsExistingForeignDashboardSessions(t *testing.T) {
	portal, store, mux := workspaceFixture(t)
	portal.installations = restrictedInstallations{team: "T"}
	if err := portal.startSession(httptest.NewRecorder(), httptest.NewRequest("GET", "/dashboard/install", nil), 3, ""); !errors.Is(err, repository.ErrWorkspaceNotAllowed) {
		t.Fatal("foreign signed link created session", err)
	}
	for _, user := range []int64{1, 3} {
		w := workspaceRequest(store, mux, user, "GET", "/dashboard", nil)
		want := http.StatusOK
		if user == 3 {
			want = http.StatusForbidden
		}
		if w.Code != want {
			t.Fatalf("user %d status %d", user, w.Code)
		}
	}
}

type restrictedSignIn struct{ *signInInstallations }

func (s restrictedSignIn) AllowedWorkspace() (string, bool) { return "T", true }

func TestSelfHostedRejectsForeignSignInBeforeSavingMember(t *testing.T) {
	portal, store, _ := workspaceFixture(t)
	repo := restrictedSignIn{&signInInstallations{}}
	h := NewSlackHandler(slack.OAuthClient{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://nudgy.test/slack/oauth/callback"}, repo).WithWorkspacePortal(portal)
	h.exchangeSignIn = func(context.Context, string, string) (slack.SignInIdentity, error) {
		return slack.SignInIdentity{Team: "OTHER", User: "U"}, nil
	}
	w := httptest.NewRecorder()
	h.signIn(w, httptest.NewRequest("GET", "/slack/signin", nil))
	u, _ := url.Parse(w.Header().Get("Location"))
	r := httptest.NewRequest("GET", "/slack/oauth/callback?code=code&state="+u.Query().Get("state"), nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	w = httptest.NewRecorder()
	h.callback(w, r)
	if w.Code != http.StatusForbidden || repo.calls != 0 || len(store.sessions) != 0 {
		t.Fatal("foreign sign-in wrote a member or session", w.Code)
	}
}
