package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nudge/internal/coding"
	"nudge/internal/service"
)

type glTransport func(*http.Request) (*http.Response, error)

func (f glTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func glHandlerFixture(t *testing.T) (*CodingHandler, *codingHandlerStore) {
	t.Helper()
	base, _, links, _ := codingHandlerFixture(t)
	store := &codingHandlerStore{}
	g := coding.GitLab{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://nudgy.example/gitlab/oauth/callback", HTTPClient: &http.Client{Transport: glTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/oauth/token":
			body = `{"access_token":"gitlab-private-test-token","refresh_token":"gitlab-private-refresh","expires_in":7200,"token_type":"Bearer"}`
		case "/api/v4/user":
			body = `{"id":42,"username":"developer","state":"active"}`
		case "/api/v4/projects", "/api/v4/projects/10":
			body = `{"id":10,"path_with_namespace":"team/sub/app","default_branch":"main","permissions":{"group_access":{"access_level":40}}}`
			if r.URL.Path == "/api/v4/projects" {
				body = "[" + body + "]"
			}
		default:
			t.Fatal("unexpected GitLab API", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	s, err := coding.NewGitLab(store, g)
	if err != nil {
		t.Fatal(err)
	}
	h := NewCodingHandler(s, base.installations, base.client, links, "https://nudgy.example")
	return h, store
}
func glSelectionForm(t *testing.T, h *CodingHandler) (string, *http.Cookie, string) {
	t.Helper()
	link, _ := h.links.URL(h.publicURL, 1, "gitlab", time.Now())
	start := httptest.NewRecorder()
	h.gitLabInstall(start, httptest.NewRequest("GET", link, nil))
	if start.Code != 302 {
		t.Fatal("start failed", start.Code)
	}
	u, _ := url.Parse(start.Header().Get("Location"))
	if u.Host != "gitlab.com" {
		t.Fatal("wrong OAuth host")
	}
	req := httptest.NewRequest("GET", h.publicURL+"/gitlab/oauth/callback?state="+u.Query().Get("state")+"&code=code", nil)
	req.AddCookie(start.Result().Cookies()[0])
	w := httptest.NewRecorder()
	h.gitLabCallback(w, req)
	if w.Code != 200 {
		t.Fatal("callback failed", w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "nudgy_gitlab_selection" {
			return c.Value, c, w.Body.String()
		}
	}
	t.Fatal("selection cookie missing")
	return "", nil, ""
}
func TestGitLabConnectionRequiresBrowserBoundProjectSelection(t *testing.T) {
	for _, scenario := range []string{"success", "missing cookie", "cross site", "unselected project", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			h, store := glHandlerFixture(t)
			state, cookie, body := glSelectionForm(t, h)
			if store.saved.UserID != 0 || !strings.Contains(body, "team/sub/app") || strings.Contains(body, "gitlab-private") {
				t.Fatal("credentials leaked or connection activated early")
			}
			form := url.Values{"state": {state}, "project": {"10"}}
			if scenario == "unselected project" {
				form.Set("project", "999")
			}
			if scenario == "empty" {
				form.Del("project")
			}
			req := httptest.NewRequest("POST", h.publicURL+"/gitlab/select", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", "null")
			if scenario != "missing cookie" {
				req.AddCookie(cookie)
			}
			if scenario == "cross site" {
				req.Header.Set("Sec-Fetch-Site", "cross-site")
			}
			w := httptest.NewRecorder()
			h.gitLabSelect(w, req)
			if scenario != "success" {
				if w.Code == 200 || store.saved.UserID != 0 {
					t.Fatal("invalid selection accepted")
				}
				return
			}
			if w.Code != 200 || store.saved.Credentials.SelectedProjects != "[10]" || !strings.Contains(w.Body.String(), "@developer") {
				t.Fatal("selection not saved", w.Body.String())
			}
			replay := httptest.NewRequest("POST", h.publicURL+"/gitlab/select", strings.NewReader(form.Encode()))
			replay.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			replay.AddCookie(cookie)
			again := httptest.NewRecorder()
			h.gitLabSelect(again, replay)
			if again.Code == 200 {
				t.Fatal("selection replay accepted")
			}
		})
	}
}
func TestGitLabToolsCoexistWithGitHubWithoutCrossProviderCalls(t *testing.T) {
	gh, _, _, _ := codingHandlerFixture(t)
	gl, store := glHandlerFixture(t)
	i := coding.Identity{UserID: 1, Team: "T", Channel: "C", Thread: "1", Event: "E", Request: "gitlab projelerimi listele"}
	store.saved = coding.Connection{UserID: 1, Team: "T", GitHubUserID: 42, Login: "developer", Credentials: coding.Credentials{Access: "gitlab-private-test-token", SelectedProjects: "[10]"}}
	base := service.NewAgentTools(nil, nil, nil, 1, time.Now(), time.UTC)
	both := gl.Tools(gh.Tools(base, i), i)
	names := map[string]bool{}
	for _, d := range both.Definitions() {
		if names[d.Name] {
			t.Fatal("duplicate tool", d.Name)
		}
		names[d.Name] = true
	}
	if !names["github_connect"] || !names["gitlab_connect"] || !names["gitlab_repositories_list"] {
		t.Fatal("provider tools missing")
	}
	out, err := both.Call(context.Background(), "gitlab_repositories_list", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(out, `"gitlab_account"`) || !strings.Contains(out, "team/sub/app") || strings.Contains(out, "gitlab-private") {
		t.Fatal("wrong provider result", out, err)
	}
	for _, args := range []string{`{"user_id":2}`, `{"provider":"github"}`} {
		if _, err := both.Call(context.Background(), "gitlab_repositories_list", json.RawMessage(args)); err == nil {
			t.Fatal("identity injection accepted")
		}
	}
	if _, err := both.Call(context.Background(), "repositories_list", json.RawMessage(`{}`)); err == nil {
		t.Fatal("GitHub used GitLab credentials")
	}
	ghToken, _ := gh.links.Token(1, "github", time.Now().Add(time.Hour))
	w := httptest.NewRecorder()
	gl.gitLabInstall(w, httptest.NewRequest("GET", gl.publicURL+"/gitlab/install?user_id=1&token="+url.QueryEscape(ghToken), nil))
	if w.Code != 401 {
		t.Fatal("GitHub invitation accepted by GitLab")
	}
}

func TestGitLabReviewUsesSeparateCapabilityAndCorrectBranchLink(t *testing.T) {
	gl, conn := glHandlerFixture(t)
	_, ghStore, _ := reviewHandlerFixture(t)
	e := ghStore.edit
	e.Task.Provider = "gitlab"
	e.Task.Repository = coding.Repository{ID: 10, Provider: "gitlab", FullName: "team/sub/app", DefaultBranch: "main"}
	e.Snapshot.Repository = e.Task.Repository
	e.Review.Repository = e.Task.Repository
	e.Task.Status = "pushed"
	e.Commit = strings.Repeat("b", 40)
	e.Digest, _ = e.Review.Digest()
	store := &buttonEditStore{e: e}
	_ = gl.service.EnableEditing(store, &buttonGit{})
	// Same Slack and numeric account IDs as GitHub, distinct GitLab credentials.
	conn.saved = coding.Connection{UserID: 1, Team: "T", GitHubUserID: 42, Login: "developer", Credentials: coding.Credentials{Access: "gitlab-private-test-token", SelectedProjects: "[10]"}}
	for _, purpose := range []string{reviewPurpose(e.Task.ID, e.Digest), gl.reviewPurpose(e.Task.ID, e.Digest)} {
		token, _ := gl.links.Token(1, purpose, time.Now().Add(time.Hour))
		q := url.Values{"user_id": {"1"}, "task_id": {e.Task.ID}, "digest": {e.Digest}, "token": {token}}
		w := httptest.NewRecorder()
		gl.review(w, httptest.NewRequest("GET", gl.publicURL+"/gitlab/review?"+q.Encode(), nil))
		if purpose == reviewPurpose(e.Task.ID, e.Digest) {
			if w.Code != 401 {
				t.Fatal("GitHub capability opened GitLab review")
			}
			continue
		}
		if w.Code != 200 || strings.Contains(w.Body.String(), "GitHub") || !strings.Contains(w.Body.String(), "GitLab’a gönderildi") {
			t.Fatal("wrong provider review", w.Code, w.Body.String())
		}
	}
	reply := &codingReply{h: gl, identity: e.Task.Identity, reviews: map[string]coding.EditInfo{e.Task.ID: {Task: e.Task, Branch: e.Review.Branch}}}
	blocks, err := reply.blocks("GitLab dalı hazır")
	if err != nil {
		t.Fatal(err)
	}
	link := blocks[len(blocks)-1]["elements"].([]map[string]any)[0]["url"]
	if link != "https://gitlab.com/team/sub/app/-/tree/"+e.Review.Branch {
		t.Fatal("wrong branch link", link)
	}
}
