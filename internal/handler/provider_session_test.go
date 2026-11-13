package handler

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestProviderLinksRequireOwnerAndSameLiveSession(t *testing.T) {
	for _, kind := range []string{"github", "gitlab", "jira", "trello", "feedbin", "raindrop"} {
		t.Run(kind, func(t *testing.T) {
			portal, store, _ := workspaceFixture(t)
			for _, user := range []int64{0, 2} {
				req := httptest.NewRequest("GET", "https://nudgy.test/"+kind+"/install", nil)
				if user > 0 {
					testProviderSession(store, req, user)
				}
				w := httptest.NewRecorder()
				if beginProviderSession(w, req, portal, 1, kind) {
					t.Fatal("non-owner started OAuth")
				}
				if len(w.Result().Cookies()) > 0 {
					t.Fatal("non-owner received provider session")
				}
			}
			req := httptest.NewRequest("GET", "https://nudgy.test/"+kind+"/install", nil)
			session := testProviderSession(store, req, 1)
			w := httptest.NewRecorder()
			if !beginProviderSession(w, req, portal, 1, kind) {
				t.Fatal("owner denied")
			}
			callback := func(cookie *http.Cookie) bool {
				req := httptest.NewRequest("GET", "https://nudgy.test/"+kind+"/oauth/callback", nil)
				req.AddCookie(cookie)
				for _, c := range w.Result().Cookies() {
					req.AddCookie(c)
				}
				return checkProviderSession(httptest.NewRecorder(), req, portal, kind)
			}
			if !callback(session) {
				t.Fatal("original session denied")
			}
			other := httptest.NewRequest("GET", "https://nudgy.test", nil)
			if callback(testProviderSession(store, other, 2)) {
				t.Fatal("account switching accepted")
			}
			delete(store.sessions, workspaceHash(session.Value))
			if callback(session) {
				t.Fatal("logged-out session accepted")
			}
		})
	}
}

func TestBrowserCarriesPanelSessionToProviderCallback(t *testing.T) {
	portal, _, _ := workspaceFixture(t)
	req := httptest.NewRequest("GET", "https://nudgy.test/dashboard/slack/callback", nil)
	w := httptest.NewRecorder()
	if err := portal.startSession(w, req, 1, ""); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	origin, _ := url.Parse("https://nudgy.test/dashboard/slack/callback")
	jar.SetCookies(origin, w.Result().Cookies())
	for _, path := range []string{"/github/install", "/gitlab/oauth/callback", "/jira/install", "/feedbin/connect", "/raindrop/oauth/callback"} {
		u, _ := url.Parse("https://nudgy.test" + path)
		found := false
		for _, c := range jar.Cookies(u) {
			if c.Name == workspaceCookie {
				found = true
			}
		}
		if !found {
			t.Fatalf("browser omitted authenticated session for %s", path)
		}
	}
}
