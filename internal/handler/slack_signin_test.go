package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nudge/internal/repository"
	"nudge/internal/slack"
)

type signInInstallations struct {
	repository.InstallationRepository // Any installation mutation panics in these tests.
	team, user                        string
	id                                int64
	err                               error
	calls                             int
}

func (s *signInInstallations) SignInUser(_ context.Context, team, user string) (int64, error) {
	s.team, s.user = team, user
	s.calls++
	return s.id, s.err
}

func TestSlackSignInStateAndMembership(t *testing.T) {
	for _, name := range []string{"member", "manager", "no-cookie", "wrong-cookie", "expired-state", "cancelled", "missing-code", "invalid-token", "uninstalled", "wrong-team", "wrong-user", "replay"} {
		t.Run(name, func(t *testing.T) {
			portal, store, mux := workspaceFixture(t)
			user := int64(2)
			if name == "manager" {
				user = 1
			}
			m := store.members[user]
			m.SlackUser = "U"
			store.members[user] = m
			repo := &signInInstallations{id: user}
			h := NewSlackHandler(slack.OAuthClient{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://nudgy.test/slack/oauth/callback"}, repo).WithWorkspacePortal(portal)
			h.RegisterRoutes(mux)
			start := httptest.NewRecorder()
			mux.ServeHTTP(start, httptest.NewRequest("GET", "https://nudgy.test/slack/signin", nil))
			if start.Code != 302 {
				t.Fatal("sign-in not started", start.Code)
			}
			u, _ := url.Parse(start.Header().Get("Location"))
			state := u.Query().Get("state")
			if !strings.HasPrefix(state, "signin.") || u.Query().Get("scope") != "openid profile" || u.Query().Get("nonce") == "" {
				t.Fatal("bad authorization request")
			}
			cookies := start.Result().Cookies()
			if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
				t.Fatal("unsafe sign-in cookie")
			}
			exchanges := 0
			h.exchangeSignIn = func(_ context.Context, code, nonce string) (slack.SignInIdentity, error) {
				exchanges++
				if code != "code" || nonce != u.Query().Get("nonce") {
					t.Fatal("exchange lost binding")
				}
				if name == "invalid-token" {
					return slack.SignInIdentity{}, errors.New("bad token")
				}
				return slack.SignInIdentity{Team: "T", User: "U"}, nil
			}
			if name == "expired-state" {
				h.states.values[state] = time.Now().Add(-time.Second)
			}
			if name == "uninstalled" {
				repo.err = errors.New("not installed")
			}
			if name == "wrong-team" {
				m.Team = "OTHER"
				store.members[user] = m
			}
			if name == "wrong-user" {
				m.SlackUser = "OTHER"
				store.members[user] = m
			}
			query := url.Values{"state": {state}, "code": {"code"}, "user_id": {"1"}, "team_id": {"OTHER"}}
			if name == "cancelled" {
				query.Set("error", "access_denied")
			}
			if name == "missing-code" {
				query.Del("code")
			}
			callback := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", "https://nudgy.test/slack/oauth/callback?"+query.Encode(), nil)
				if name != "no-cookie" {
					c := *cookies[0]
					if name == "wrong-cookie" {
						c.Value = "wrong"
					}
					r.AddCookie(&c)
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				return w
			}
			w := callback()
			good := name == "member" || name == "manager" || name == "replay"
			if !good {
				if w.Code < 400 || len(store.sessions) != 0 || !strings.Contains(w.Body.String(), "Slack ile giriş yap") {
					t.Fatalf("rejected login created session: %d", w.Code)
				}
				switch name {
				case "no-cookie", "wrong-cookie", "expired-state", "cancelled", "missing-code":
					if exchanges != 0 {
						t.Fatal("invalid state exchanged code")
					}
				}
				return
			}
			if w.Code != 303 || w.Header().Get("Location") != "/dashboard" || len(store.sessions) != 1 || repo.team != "T" || repo.user != "U" {
				t.Fatal("login failed or trusted query identity", w.Code)
			}
			var session *http.Cookie
			for _, c := range w.Result().Cookies() {
				if c.Name == workspaceCookie {
					session = c
				}
			}
			if session == nil || !session.Secure || !session.HttpOnly || session.Path != "/" {
				t.Fatal("missing session")
			}
			page := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "https://nudgy.test/dashboard", nil)
			r.AddCookie(session)
			mux.ServeHTTP(page, r)
			if page.Code != 200 || (strings.Contains(page.Body.String(), "AI ayarları</span>") != (name == "manager")) {
				t.Fatal("wrong role", page.Code)
			}
			if name == "replay" {
				if callback().Code != 400 || exchanges != 1 || repo.calls != 1 {
					t.Fatal("callback replay accepted")
				}
			}
		})
	}
}

func TestDashboardSignedOutEntry(t *testing.T) {
	portal, _, mux := workspaceFixture(t)
	h := NewSlackHandler(slack.OAuthClient{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://nudgy.test/slack/oauth/callback"}, &signInInstallations{}).WithWorkspacePortal(portal)
	h.RegisterRoutes(mux)
	for _, path := range []string{"/dashboard", "/dashboard/ai", "/dashboard/team"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `href="/slack/signin"`) || strings.Contains(w.Body.String(), "Panele dön") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("signed-out page", path, w.Code)
		}
	}
}
