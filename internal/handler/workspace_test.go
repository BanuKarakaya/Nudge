package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"nudge/internal/secure"
	"nudge/internal/workspace"
)

type workspaceMemory struct {
	members      map[int64]workspace.Member
	scopes       map[string]workspace.Scope
	sessions     map[string]int64
	used         map[string]bool
	disconnected int64
	saved        int
}

func (s *workspaceMemory) Member(_ context.Context, id int64) (workspace.Member, error) {
	m, ok := s.members[id]
	if !ok {
		return m, workspace.ErrDenied
	}
	return m, nil
}
func (s *workspaceMemory) Scope(ctx context.Context, id int64, kind string) (workspace.Scope, error) {
	m, e := s.Member(ctx, id)
	v := s.scopes[m.Team+kind]
	v.Manager = m.Manager
	return v, e
}
func (s *workspaceMemory) SaveScope(ctx context.Context, id int64, kind string, v workspace.Scope) error {
	m, e := s.Member(ctx, id)
	if e != nil || !m.Manager {
		return workspace.ErrDenied
	}
	s.scopes[m.Team+kind] = v
	s.saved++
	return nil
}
func (s *workspaceMemory) CreateSession(_ context.Context, id int64, hash string, expires time.Time, link string) error {
	if link != "" && s.used[link] {
		return workspace.ErrDenied
	}
	s.used[link] = true
	s.sessions[hash] = id
	return nil
}
func (s *workspaceMemory) SessionUser(_ context.Context, hash string) (int64, error) {
	id, ok := s.sessions[hash]
	if !ok {
		return 0, workspace.ErrDenied
	}
	return id, nil
}
func (s *workspaceMemory) DeleteSession(_ context.Context, hash string) error {
	delete(s.sessions, hash)
	return nil
}
func workspaceFixture(t *testing.T) (*WorkspaceHandler, *workspaceMemory, *http.ServeMux) {
	t.Helper()
	key, _ := secure.NewLinkSigner(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	s := &workspaceMemory{members: map[int64]workspace.Member{1: {UserID: 1, Team: "T", Name: "Company", Manager: true}, 2: {UserID: 2, Team: "T", Name: "Company"}, 3: {UserID: 3, Team: "OTHER", Name: "Personal", Manager: true}}, scopes: map[string]workspace.Scope{}, sessions: map[string]int64{}, used: map[string]bool{}}
	h := NewWorkspaceHandler(s, key, "https://nudgy.test", nil, nil)
	h.providers = []workspaceProvider{{Kind: "github", Name: "GitHub", Connected: func(context.Context, int64) (bool, error) { return true, nil }, Disconnect: func(_ context.Context, id int64) error { s.disconnected = id; return nil }, Resources: func(_ context.Context, m workspace.Member) ([]workspace.Resource, error) {
		if !m.Manager {
			return nil, errors.New("not manager")
		}
		return []workspace.Resource{{ID: "10", Name: "company/repo"}}, nil
	}}}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, s, mux
}
func workspaceRequest(s *workspaceMemory, mux *http.ServeMux, user int64, method, path string, form url.Values) *httptest.ResponseRecorder {
	raw := strings.Repeat(strconv.FormatInt(user, 10), 64)
	s.sessions[workspaceHash(raw)] = user
	if form == nil {
		form = url.Values{}
	}
	if !form.Has("csrf") {
		form.Set("csrf", workspaceHash("csrf:"+raw))
	}
	r := httptest.NewRequest(method, "https://nudgy.test"+path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://nudgy.test")
	r.AddCookie(&http.Cookie{Name: workspaceCookie, Value: raw})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}
func TestWorkspaceRolesScopeValidationAndIsolation(t *testing.T) {
	_, s, mux := workspaceFixture(t)
	for _, path := range []string{"/dashboard/integrations/github", "/dashboard/integrations/jira"} {
		w := workspaceRequest(s, mux, 2, "POST", path, url.Values{"enabled": {"true"}, "resource": {"10"}, "user_id": {"1"}})
		if w.Code != 403 {
			t.Fatalf("member changed shared settings: %d", w.Code)
		}
	}
	w := workspaceRequest(s, mux, 1, "POST", "/dashboard/integrations/github", url.Values{"enabled": {"true"}, "resource": {"forged"}})
	if w.Code != 400 || s.saved != 0 {
		t.Fatal("unverified resource accepted")
	}
	w = workspaceRequest(s, mux, 1, "POST", "/dashboard/integrations/github", url.Values{"enabled": {"true"}, "resource": {"10"}})
	if w.Code != 303 || s.saved != 1 {
		t.Fatalf("manager save: %d %s", w.Code, w.Body)
	}
	scope, _ := s.Scope(context.Background(), 2, "github")
	other, _ := s.Scope(context.Background(), 3, "github")
	if !scope.Allows("10") || other.Enabled {
		t.Fatal("workspace scope leaked")
	}
	w = workspaceRequest(s, mux, 2, "GET", "/dashboard/accounts", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Alanları yönet") || !strings.Contains(w.Body.String(), "Yeniden doğrula") {
		t.Fatal("incorrect employee UI", w.Code, w.Body)
	}
	w = workspaceRequest(s, mux, 2, "POST", "/dashboard/disconnect/github", url.Values{"user_id": {"1"}})
	if w.Code != 303 || s.disconnected != 2 {
		t.Fatal("disconnect used client-provided identity")
	}
	if !s.scopes["Tgithub"].Enabled {
		t.Fatal("personal disconnect changed workspace")
	}
	w = workspaceRequest(s, mux, 1, "POST", "/dashboard/integrations/github", url.Values{"enabled": {"false"}})
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	w = workspaceRequest(s, mux, 2, "POST", "/dashboard/connect/github", nil)
	if w.Code != 403 {
		t.Fatal("member connected disabled provider")
	}
	w = workspaceRequest(s, mux, 3, "GET", "/dashboard/integrations", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Çalışma alanı ayarları") {
		t.Fatal("individual lacks admin panel")
	}
}
func TestWorkspaceCSRFAndAuthentication(t *testing.T) {
	_, s, mux := workspaceFixture(t)
	for _, path := range []string{"/dashboard/connect/github", "/dashboard/disconnect/github", "/dashboard/integrations/github", "/dashboard/logout"} {
		w := workspaceRequest(s, mux, 1, "POST", path, url.Values{"csrf": {"wrong"}})
		if w.Code != 403 {
			t.Fatalf("csrf %s: %d", path, w.Code)
		}
		r := httptest.NewRequest("POST", "https://nudgy.test"+path, strings.NewReader("csrf=wrong"))
		r.Header.Set("Authorization", "Bearer operator-token")
		r.Header.Set("X-User-ID", "1")
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("operator header became member login")
		}
	}
	if s.saved != 0 || s.disconnected != 0 {
		t.Fatal("CSRF mutated state")
	}
}
func TestWorkspaceMagicLinkOneUseCookieBindingAndLogout(t *testing.T) {
	h, s, mux := workspaceFixture(t)
	link, err := h.links.URL(h.base, 2, "workspace", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link)
	r := httptest.NewRequest("GET", link, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || len(s.sessions) != 0 {
		t.Fatal("GET consumed login or started session")
	}
	cookie := w.Result().Cookies()[0]
	form := url.Values{"user_id": {"2"}, "token": {u.Query().Get("token")}, "csrf": {cookie.Value}}
	login := func(c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", h.base+"/dashboard/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", h.base)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if login(nil).Code != 403 {
		t.Fatal("login lacked browser binding")
	}
	w = login(cookie)
	if w.Code != 303 {
		t.Fatalf("login failed: %d %s", w.Code, w.Body)
	}
	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == workspaceCookie {
			session = c
		}
	}
	if session == nil || !session.HttpOnly || !session.Secure || session.Path != "/dashboard" {
		t.Fatal("insecure session cookie")
	}
	if login(cookie).Code != 401 {
		t.Fatal("magic link replay accepted")
	}
	r = httptest.NewRequest("POST", h.base+"/dashboard/logout", strings.NewReader(url.Values{"csrf": {workspaceHash("csrf:" + session.Value)}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(session)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 303 {
		t.Fatal("logout", w.Code)
	}
	r = httptest.NewRequest("GET", h.base+"/dashboard", nil)
	r.AddCookie(session)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Hoş geldin.") {
		t.Fatal("logout did not revoke session")
	}
}

func TestWorkspaceGitHubMemberOAuthSkipsInstallation(t *testing.T) {
	portal, members, _ := workspaceFixture(t)
	m := members.members[1]
	m.Manager = false
	members.members[1] = m
	members.scopes["Tgithub"] = workspace.Scope{Enabled: true, Resources: []workspace.Resource{{ID: "1"}}}
	h, store, links, _ := codingHandlerFixture(t)
	portal.RegisterCoding(h.service)
	h.WithWorkspacePortal(portal)
	public := http.NewServeMux()
	h.RegisterRoutes(public, http.NewServeMux())
	link, _ := links.URL(h.publicURL, 1, "github", time.Now())
	start := httptest.NewRecorder()
	public.ServeHTTP(start, httptest.NewRequest("GET", link+"&setup=1", nil))
	if start.Code != 302 || !strings.Contains(start.Header().Get("Location"), "/login/oauth/authorize") {
		t.Fatal("member sent to installation", start.Code, start.Body)
	}
	redirect, _ := url.Parse(start.Header().Get("Location"))
	r := httptest.NewRequest("GET", h.publicURL+"/github/oauth/callback?state="+redirect.Query().Get("state")+"&code=code", nil)
	r.AddCookie(start.Result().Cookies()[0])
	result := httptest.NewRecorder()
	public.ServeHTTP(result, r)
	if result.Code != 303 || result.Header().Get("Location") != "/dashboard/accounts?done=verified" || store.saved.UserID != 1 || store.saved.Login != "developer" {
		t.Fatal("member verification failed", result.Code, result.Body)
	}
}
func TestWorkspaceTrackersReturnToPanelWithoutSelection(t *testing.T) {
	for _, kind := range []string{"jira", "trello"} {
		t.Run(kind, func(t *testing.T) {
			portal, members, _ := workspaceFixture(t)
			m := members.members[1]
			m.Manager = false
			members.members[1] = m
			members.scopes["T"+kind] = workspace.Scope{Enabled: true, Resources: []workspace.Resource{{ID: "selected"}}}
			h, store := trackerHandlerFixture(t, kind)
			portal.RegisterTracker(h.service)
			h.WithWorkspacePortal(portal)
			public := http.NewServeMux()
			h.RegisterRoutes(public, http.NewServeMux())
			link, _ := h.links.URL(h.publicURL, 1, kind, time.Now())
			start := httptest.NewRecorder()
			public.ServeHTTP(start, httptest.NewRequest("GET", link, nil))
			if start.Code != 302 {
				t.Fatal("OAuth not started", start.Code)
			}
			redirect, _ := url.Parse(start.Header().Get("Location"))
			r := httptest.NewRequest("GET", h.publicURL+"/"+kind+"/oauth/callback?state="+redirect.Query().Get("state")+"&code=code", nil)
			r.AddCookie(start.Result().Cookies()[0])
			w := httptest.NewRecorder()
			public.ServeHTTP(w, r)
			if w.Code != 303 || w.Header().Get("Location") != "/dashboard/accounts?done=verified" || store.saved.UserID != 1 || len(store.saved.Selected) != 0 {
				t.Fatal("member asked to select resources", w.Code, w.Body)
			}
		})
	}
}
func TestWorkspaceOriginAndExpiredLogin(t *testing.T) {
	h, s, mux := workspaceFixture(t)
	for _, base := range []string{"https://example.com/path", "http://example.com", "https://user:pass@example.com", "https://example.com?x=1", ""} {
		h.base = base
		if h.Validate() == nil {
			t.Fatal("invalid canonical origin", base)
		}
	}
	h.base = "https://nudgy.test"
	if h.Validate() != nil {
		t.Fatal("valid origin rejected")
	}
	token, _ := h.links.Token(1, "workspace", time.Now().Add(-time.Minute))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", h.base+"/dashboard/install?user_id=1&token="+token, nil))
	if w.Code != 401 {
		t.Fatal("expired login accepted")
	}
	raw := strings.Repeat("a", 64)
	s.sessions[workspaceHash(raw)] = 1
	form := url.Values{"csrf": {workspaceHash("csrf:" + raw)}}
	r := httptest.NewRequest("POST", h.base+"/dashboard/disconnect/github", strings.NewReader(form.Encode()))
	r.AddCookie(&http.Cookie{Name: workspaceCookie, Value: raw})
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.test")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 || s.disconnected != 0 {
		t.Fatal("cross-origin mutation")
	}
}
