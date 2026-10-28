package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nudge/internal/coding"
	"nudge/internal/domain"
	"nudge/internal/secure"
	"nudge/internal/service"
)

type codingHandlerStore struct {
	coding.Store
	state coding.OAuthState
	hash  string
	saved coding.Connection
}

func (*codingHandlerStore) SlackTeam(_ context.Context, id int64) (string, error) {
	if id == 1 {
		return "T", nil
	}
	return "", coding.ErrDenied
}

func (s *codingHandlerStore) PutOAuthState(_ context.Context, hash string, state coding.OAuthState) error {
	s.hash, s.state = hash, state
	return nil
}
func (s *codingHandlerStore) ConsumeOAuthState(_ context.Context, hash string) (coding.OAuthState, error) {
	if hash != s.hash || hash == "" {
		return coding.OAuthState{}, errors.New("invalid state")
	}
	s.hash = ""
	return s.state, nil
}
func (s *codingHandlerStore) SaveConnection(_ context.Context, c coding.Connection) error {
	s.saved = c
	return nil
}

func (s *codingHandlerStore) ConnectionBindings(context.Context) ([]coding.ConnectionBinding, error) {
	return []coding.ConnectionBinding{{UserID: s.saved.UserID, Team: s.saved.Team, SlackUserID: "U", Account: coding.Account{ID: s.saved.GitHubUserID, Login: s.saved.Login}}}, nil
}

func (s *codingHandlerStore) WithConnection(_ context.Context, userID int64, fn func(*coding.Connection) error) error {
	if userID != s.saved.UserID {
		return coding.ErrConnect
	}
	c := s.saved
	return fn(&c)
}

type codingHandlerProvider struct{ coding.Provider }

func (codingHandlerProvider) InstallationURL(state string) string {
	return "https://github.com/apps/nudgy-github/installations/new?state=" + state
}
func (codingHandlerProvider) VerifyInstallation(_ context.Context, token string, installationID int64, account coding.Account) error {
	if token != "ghu_never_expose" || installationID != 2 || account.ID != 42 {
		return coding.ErrDenied
	}
	return nil
}
func (codingHandlerProvider) AuthorizeURL(state, verifier string) string {
	return "https://github.com/login/oauth/authorize?state=" + state
}
func (codingHandlerProvider) Exchange(context.Context, string, string) (coding.Credentials, error) {
	return coding.Credentials{Access: "ghu_never_expose"}, nil
}
func (codingHandlerProvider) User(context.Context, string) (int64, string, error) {
	return 42, "developer", nil
}

func (codingHandlerProvider) Repositories(context.Context, string) ([]coding.Repository, error) {
	return []coding.Repository{{ID: 1, InstallationID: 2, FullName: "developer/private", DefaultBranch: "main"}}, nil
}

type codingInstallations struct{ conversationInstallations }

func (codingInstallations) GetByUserID(context.Context, int64) (domain.SlackInstallation, error) {
	return domain.SlackInstallation{TeamID: "T", SlackUserID: "U", BotToken: "test"}, nil
}

type codingDM struct{ sent []string }

func (*codingDM) OpenDirectMessage(_ context.Context, token, user string) (string, error) {
	if token != "test" || user != "U" {
		return "", errors.New("wrong recipient")
	}
	return "D", nil
}
func (d *codingDM) PostMessage(_ context.Context, token, channel, text string) error {
	if channel != "D" {
		return errors.New("not private")
	}
	d.sent = append(d.sent, text)
	return nil
}

func codingHandlerFixture(t *testing.T) (*CodingHandler, *codingHandlerStore, secure.LinkSigner, *codingDM) {
	t.Helper()
	store := &codingHandlerStore{}
	s, err := coding.New(store, codingHandlerProvider{})
	if err != nil {
		t.Fatal(err)
	}
	links, err := secure.NewLinkSigner(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32))))
	if err != nil {
		t.Fatal(err)
	}
	dm := &codingDM{}
	return NewCodingHandler(s, codingInstallations{}, dm, links, "https://nudgy.example"), store, links, dm
}

func TestGitHubLinkIsPrivateAndUserBound(t *testing.T) {
	h, _, links, dm := codingHandlerFixture(t)
	tools := h.Tools(service.NewAgentTools(nil, nil, nil, 1, time.Now(), nil), coding.Identity{UserID: 1, Team: "T"})
	result, err := tools.Call(context.Background(), "github_connect", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(dm.sent) != 1 || strings.Contains(result, "token=") {
		t.Fatal("link delivery not private")
	}
	link := strings.Split(dm.sent[0], "\n")[1]
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/github/install" || !links.Verify(u.Query().Get("token"), 1, "github", time.Now()) {
		t.Fatal("incorrect capability")
	}
	if links.Verify(u.Query().Get("token"), 2, "github", time.Now()) || links.Verify(u.Query().Get("token"), 1, "feedbin", time.Now()) || links.Verify(u.Query().Get("token"), 1, "github", time.Now().Add(21*time.Minute)) {
		t.Fatal("capability not scoped/short-lived")
	}
}

func TestGitHubCallbackRequiresSignedLinkBrowserCookieAndSingleUseState(t *testing.T) {
	h, store, links, _ := codingHandlerFixture(t)
	unauth := httptest.NewRecorder()
	h.install(unauth, httptest.NewRequest("GET", "https://nudgy.example/github/install?user_id=1", nil))
	if unauth.Code != 401 || store.hash != "" {
		t.Fatal("unsigned installation accepted")
	}
	link, _ := links.URL("https://nudgy.example", 1, "github", time.Now())
	start := httptest.NewRecorder()
	h.install(start, httptest.NewRequest("GET", link, nil))
	if start.Code != 200 {
		t.Fatalf("install=%d %s", start.Code, start.Body.String())
	}
	var u *url.URL
	installState := start.Result().Cookies()[0].Value
	if !strings.Contains(start.Body.String(), "/apps/nudgy-github/installations/new?state=") || store.saved.UserID != 0 {
		t.Fatal("connection did not start with repo selection")
	}
	setupRequest := httptest.NewRequest("GET", "https://nudgy.example/github/setup?installation_id=2&setup_action=install&state="+installState, nil)
	setupRequest.AddCookie(start.Result().Cookies()[0])
	setup := httptest.NewRecorder()
	h.setup(setup, setupRequest)
	if setup.Code != 302 || store.saved.UserID != 0 {
		t.Fatal("setup must continue to OAuth without saving a connection")
	}
	u, _ = url.Parse(setup.Header().Get("Location"))
	state := u.Query().Get("state")
	if u.Path != "/login/oauth/authorize" || state == installState {
		t.Fatal("OAuth state not rotated")
	}
	callback := "https://nudgy.example/github/oauth/callback?code=code&state=" + state
	bad := httptest.NewRecorder()
	h.callback(bad, httptest.NewRequest("GET", callback, nil))
	if bad.Code != 400 || store.saved.UserID != 0 {
		t.Fatal("cookie-less callback accepted")
	}
	request := httptest.NewRequest("GET", callback, nil)
	var cookies []*http.Cookie
	for _, cookie := range setup.Result().Cookies() {
		if cookie.Name == "nudgy_github_oauth" {
			cookies = append(cookies, cookie)
		}
	}
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("insecure OAuth cookie")
	}
	request.AddCookie(cookies[0])
	ok := httptest.NewRecorder()
	h.callback(ok, request)
	if ok.Code != 200 || store.saved.UserID != 1 || store.saved.Team != "T" || store.saved.GitHubUserID != 42 {
		t.Fatalf("callback=%d connection=%d", ok.Code, store.saved.UserID)
	}
	if !strings.Contains(ok.Body.String(), "@developer") {
		t.Fatal("verified GitHub account missing from confirmation")
	}
	if strings.Contains(ok.Body.String(), "GitHub repolarımı getir") || strings.Contains(ok.Body.String(), `id="copy"`) {
		t.Fatal("connection page solicits unrelated actions")
	}
	if strings.Contains(ok.Body.String(), "ghu_") || ok.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("callback leaked secret or cached page")
	}
	replay := httptest.NewRecorder()
	h.callback(replay, request)
	if replay.Code == 200 {
		t.Fatal("state replay accepted")
	}
}

func TestEnablingCodingToolsRebootsNativeThreadButPreservesDeliveredHistory(t *testing.T) {
	codingHandler, _, _, _ := codingHandlerFixture(t)
	store := make(conversationTestStore)
	key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1.0", User: "U"}
	state := deliveredState()
	state.AgentThreadID = "old-native"
	state.AgentTokenUsage = &service.AgentTokenUsage{Total: 120}
	if err := store.Save(context.Background(), key, state); err != nil {
		t.Fatal(err)
	}
	h := (&SlackEventHandler{installations: conversationInstallations{}, history: store, client: &conversationSender{}, runtime: agentFunc(func(_ context.Context, r service.AgentRequest) (service.AgentResult, error) {
		if r.ThreadID != "" || r.History.AgentTokenUsage != nil || len(r.History.Turns) != len(state.Turns) {
			t.Fatal("tool change resumed incompatible native context")
		}
		found := false
		for _, d := range r.Tools.Definitions() {
			if d.Name == "repositories_list" {
				found = true
			}
		}
		if !found {
			t.Fatal("coding tools missing")
		}
		return service.AgentResult{ThreadID: "new-native", Text: "repo tools ready"}, nil
	})}).WithCoding(codingHandler)
	h.replyWithSummary(conversationEvent("GitHub repolarımı göster"))
	got, err := store.Load(context.Background(), key)
	if err != nil || got.AgentThreadID != "new-native" || got.AgentToolVersion != "coding-connect-v4" {
		t.Fatalf("new tool version not persisted: %+v", got)
	}
}

func TestCodingRoutesRemainBehindAdminAuthorization(t *testing.T) {
	h, _, _, _ := codingHandlerFixture(t)
	public, admin := http.NewServeMux(), http.NewServeMux()
	h.RegisterRoutes(public, admin)
	public.Handle("/api/v1/", RequireAPIToken("admin", admin))
	w := httptest.NewRecorder()
	public.ServeHTTP(w, httptest.NewRequest("DELETE", "https://nudgy.example/api/v1/coding/github/connection", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatal("anonymous disconnect")
	}
}

func TestGitHubConfirmationEscapesAccountData(t *testing.T) {
	w := httptest.NewRecorder()
	renderGitHubConnectionPage(w, http.StatusOK, githubPageData{Success: true, Title: "GitHub’la tanıştık.", Login: `<img src=x onerror="alert(1)">`})
	if strings.Contains(w.Body.String(), `<img src=x`) || !strings.Contains(w.Body.String(), "&lt;img") {
		t.Fatal("unescaped GitHub account in HTML")
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("account page must not be cached or leak OAuth URL through referer")
	}
}

func TestGitHubDiagnosticsAreAdminOnlyAndNeverExposeCredentials(t *testing.T) {
	h, store, _, _ := codingHandlerFixture(t)
	store.saved = coding.Connection{UserID: 1, Team: "T", GitHubUserID: 42, Login: "developer", Credentials: coding.Credentials{Access: "ghu_secret", Refresh: "ghr_secret"}}
	public, admin := http.NewServeMux(), http.NewServeMux()
	h.RegisterRoutes(public, admin)
	public.Handle("/api/v1/", RequireAPIToken("admin", admin))
	for _, path := range []string{"connections", "connection"} {
		for _, token := range []string{"", "wrong", "admin"} {
			req := httptest.NewRequest("GET", "/api/v1/coding/github/"+path, nil)
			req.Header.Set("X-User-ID", "1")
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			public.ServeHTTP(w, req)
			want := http.StatusUnauthorized
			if token == "admin" {
				want = http.StatusOK
			}
			if w.Code != want || strings.Contains(w.Body.String(), "ghu_") || strings.Contains(w.Body.String(), "ghr_") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unsafe diagnostic route: path=%s status=%d body=%s", path, w.Code, w.Body.String())
			}
		}
	}
	store.saved.GitHubUserID = 999
	req := httptest.NewRequest("GET", "/api/v1/coding/github/connection", nil)
	req.Header.Set("Authorization", "Bearer admin")
	req.Header.Set("X-User-ID", "1")
	w := httptest.NewRecorder()
	public.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "developer/private") {
		t.Fatal("diagnostic bypassed identity check")
	}
}

func TestGitHubSetupRejectsUnboundAndPendingSelections(t *testing.T) {
	for _, tc := range []struct {
		name, method, query, origin string
		cookie                      bool
		want                        int
	}{
		{"missing browser", "GET", "installation_id=2&setup_action=install", "", false, 400},
		{"wrong state", "GET", "installation_id=2&setup_action=install&state=wrong", "", true, 400},
		{"missing installation", "GET", "setup_action=install", "", true, 400},
		{"pending approval", "GET", "installation_id=2&setup_action=request", "", true, 400},
		{"cross-origin form", "POST", "", "https://attacker.invalid", true, 403},
		{"missing form state", "POST", "", "https://nudgy.example", true, 400},
		{"existing update without query state", "GET", "installation_id=2&setup_action=update", "", true, 302},
		{"existing personal selection", "POST", "valid-form", "https://nudgy.example", true, 302},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, store, links, _ := codingHandlerFixture(t)
			link, _ := links.URL("https://nudgy.example", 1, "github", time.Now())
			start := httptest.NewRecorder()
			h.install(start, httptest.NewRequest("GET", link, nil))
			cookie := start.Result().Cookies()[0]
			var req *http.Request
			if tc.method == "POST" {
				body := ""
				if tc.query == "valid-form" {
					body = url.Values{"state": {cookie.Value}}.Encode()
				}
				req = httptest.NewRequest("POST", "https://nudgy.example/github/setup", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			} else {
				req = httptest.NewRequest("GET", "https://nudgy.example/github/setup?"+tc.query, nil)
			}
			if tc.cookie {
				req.AddCookie(cookie)
			}
			req.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			h.setup(w, req)
			if w.Code != tc.want || store.saved.UserID != 0 {
				t.Fatalf("setup status=%d want=%d saved=%d", w.Code, tc.want, store.saved.UserID)
			}
			if tc.want == 302 {
				u, _ := url.Parse(w.Header().Get("Location"))
				if u.Host != "github.com" || u.Path != "/login/oauth/authorize" || u.Query().Get("state") == cookie.Value {
					t.Fatal("setup did not start a separate OAuth session")
				}
				replay := httptest.NewRecorder()
				h.setup(replay, req)
				if replay.Code == 302 {
					t.Fatal("selection replay accepted")
				}
			}
		})
	}
}
