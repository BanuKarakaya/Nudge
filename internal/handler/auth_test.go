package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nudge/internal/repository"
	"nudge/internal/secure"
	"nudge/internal/service"
)

func TestAdministrativeAPIRequiresCredentialBeforeUserSelection(t *testing.T) {
	for _, path := range []string{"/api/v1/bookmarks", "/api/v1/bookmarks/4/read", "/api/v1/slack/test-message", "/api/v1/raindrop/sync"} {
		for _, tc := range []struct {
			configured, supplied string
			want                 int
		}{{"secret", "", 401}, {"secret", "Bearer wrong", 401}, {"", "Bearer secret", 503}, {"secret", "Bearer secret", 204}} {
			called := false
			h := RequireAPIToken(tc.configured, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
			r := httptest.NewRequest("POST", path, nil)
			r.Header.Set("X-User-ID", "99")
			r.Header.Set("Authorization", tc.supplied)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || called != (tc.want == 204) {
				t.Fatalf("%s: status=%d called=%v", path, w.Code, called)
			}
		}
	}
}

type dashboardRepo struct {
	repository.AnalyticsRepository
	calls int
}

func (r *dashboardRepo) Dashboard(context.Context, repository.DashboardFilters) (repository.DashboardSummary, error) {
	r.calls++
	return repository.DashboardSummary{}, nil
}
func TestDashboardFailsClosedAndRejectsQueryTokens(t *testing.T) {
	for _, tc := range []struct {
		name, key, header, path string
		want                    int
	}{
		{"disabled", "", "", "/api/dashboard", 503},
		{"missing", "secret", "", "/api/dashboard", 401},
		{"URL token", "secret", "", "/api/dashboard?token=secret", 401},
		{"wrong", "secret", "wrong", "/api/dashboard", 401},
		{"valid", "secret", "secret", "/api/dashboard", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &dashboardRepo{}
			h := NewDashboardHandler(repo, tc.key)
			r := httptest.NewRequest("GET", tc.path, nil)
			r.Header.Set("X-Dashboard-Token", tc.header)
			w := httptest.NewRecorder()
			h.api(w, r)
			if w.Code != tc.want || (repo.calls > 0) != (tc.want == 200) {
				t.Fatalf("status=%d calls=%d", w.Code, repo.calls)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("sensitive response can be cached")
			}
		})
	}
}
func testLinks(t *testing.T) secure.LinkSigner {
	t.Helper()
	s, err := secure.NewLinkSigner(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestAccountConnectionRejectsForgedUserAndCrossServiceToken(t *testing.T) {
	signer := testLinks(t)
	valid, _ := signer.Token(1, "feedbin", time.Now().Add(time.Hour))
	for _, token := range []string{"", valid} {
		// nil services deliberately panic if unauthorized requests reach business logic.
		h := NewFeedbinHandler(nil, signer)
		r := httptest.NewRequest("GET", "/feedbin/install?user_id=2&token="+url.QueryEscape(token), nil)
		w := httptest.NewRecorder()
		h.install(w, r)
		if w.Code != 401 {
			t.Fatalf("GET status=%d", w.Code)
		}
		body := url.Values{"user_id": {"2"}, "token": {token}, "email": {"x@example.test"}, "password": {"secret"}}
		r = httptest.NewRequest("POST", "/feedbin/connect", strings.NewReader(body.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w = httptest.NewRecorder()
		h.connect(w, r)
		if w.Code != 401 {
			t.Fatalf("POST status=%d", w.Code)
		}
	}
	h := &RaindropHandler{links: signer}
	r := httptest.NewRequest("GET", "/raindrop/install?user_id=1&token="+url.QueryEscape(valid), nil)
	w := httptest.NewRecorder()
	h.install(w, r)
	if w.Code != 401 {
		t.Fatalf("cross-service status=%d", w.Code)
	}
	// A legitimate signed link survives form validation and is safely included for POST.
	r = httptest.NewRequest("POST", "/feedbin/connect", strings.NewReader(url.Values{"user_id": {"1"}, "token": {valid}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	NewFeedbinHandler(nil, signer).connect(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), valid) || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("valid connection form lost its capability")
	}
}
func TestOAuthCallbackRequiresInitiatingBrowser(t *testing.T) {
	h := &SlackHandler{states: newOAuthStateStore()}
	h.states.add("state", time.Minute)
	r := httptest.NewRequest("GET", "/slack/oauth/callback?state=state&error=access_denied", nil)
	w := httptest.NewRecorder()
	h.callback(w, r)
	if !strings.Contains(w.Body.String(), "browser session mismatch") {
		t.Fatal(w.Body.String())
	}
	// Missing cookie must not consume the legitimate browser's state.
	r.AddCookie(&http.Cookie{Name: "nudge_slack_oauth", Value: "state"})
	w = httptest.NewRecorder()
	h.callback(w, r)
	if !strings.Contains(w.Body.String(), "authorization was declined") {
		t.Fatal(w.Body.String())
	}
}
func NewSlackHandlerForCookieTest() *SlackHandler { return &SlackHandler{states: newOAuthStateStore()} }

type actionBookmarks struct {
	service.BookmarkService
	calls int
}

func (b *actionBookmarks) MarkRead(context.Context, int64, int64) error { b.calls++; return nil }
func signSlackRequest(r *http.Request, body string) {
	stamp := fmt.Sprint(time.Now().Unix())
	mac := hmac.New(sha256.New, []byte("signing"))
	mac.Write([]byte("v0:" + stamp + ":" + body))
	r.Header.Set("X-Slack-Request-Timestamp", stamp)
	r.Header.Set("X-Slack-Signature", fmt.Sprintf("v0=%x", mac.Sum(nil)))
}
func TestSignedSlackInteractionStillChecksBookmarkOwner(t *testing.T) {
	for _, id := range []int{1, 2} {
		bookmarks := &actionBookmarks{}
		h := NewSlackInteractionHandler("signing", bookmarks, conversationInstallations{})
		payload := fmt.Sprintf(`{"team":{"id":"T"},"user":{"id":"U"},"actions":[{"action_id":"bookmark_read","value":"{\"user_id\":%d,\"bookmark_id\":9}"}]}`, id)
		body := url.Values{"payload": {payload}}.Encode()
		r := httptest.NewRequest("POST", "/slack/interactions", strings.NewReader(body))
		signSlackRequest(r, body)
		w := httptest.NewRecorder()
		h.handle(w, r)
		if id == 1 && (w.Code != 200 || bookmarks.calls != 1) {
			t.Fatalf("owner denied: %s", w.Body.String())
		}
		if id == 2 && (w.Code != 403 || bookmarks.calls != 0) {
			t.Fatal("another user's bookmark changed")
		}
	}
}
func TestSlackChallengeRequiresSignature(t *testing.T) {
	body := `{"type":"url_verification","challenge":"test"}`
	for _, signed := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/slack/events", strings.NewReader(body))
		if signed {
			signSlackRequest(r, body)
		}
		w := httptest.NewRecorder()
		(&SlackEventHandler{signingSecret: "signing"}).handle(w, r)
		want := 401
		if signed {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("signed=%v status=%d", signed, w.Code)
		}
	}
}
