package coding

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestGitHubRejectsReadOnlyUninstalledSuspendedAndDifferentApp(t *testing.T) {
	var calls []string
	g := GitHub{AppID: 7, HTTPClient: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.github.com" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer ghu_private" {
			t.Fatal("wrong credential destination")
		}
		calls = append(calls, r.URL.Path)
		if r.URL.Path == "/user" {
			return response(`{"id":55,"login":"developer"}`), nil
		}
		if r.URL.Path == "/repos/owner/repo/collaborators/developer/permission" {
			return response(`{"permission":"write","user":{"id":55,"login":"developer"}}`), nil
		}
		if r.URL.Path == "/user/installations" {
			return response(`{"installations":[{"id":1,"app_id":7,"permissions":{"contents":"write","pull_requests":"write"}},{"id":2,"app_id":8,"permissions":{"contents":"write","pull_requests":"write"}},{"id":3,"app_id":7,"permissions":{"contents":"read","pull_requests":"write"}},{"id":4,"app_id":7,"suspended_at":"2026-01-01T00:00:00Z","permissions":{"contents":"write","pull_requests":"write"}}]}`), nil
		}
		if r.URL.Path != "/user/installations/1/repositories" {
			t.Fatal("accessed unauthorized installation")
		}
		return response(`{"repositories":[{"id":10,"full_name":"owner/repo","default_branch":"master","permissions":{"push":true}},{"id":11,"full_name":"owner/readonly","default_branch":"main","permissions":{"push":false}},{"id":12,"full_name":"owner/archived","archived":true,"permissions":{"push":true}},{"id":13,"full_name":"owner/disabled","disabled":true,"permissions":{"push":true}}]}`), nil
	})}}
	repos, err := g.Repositories(context.Background(), "ghu_private")
	if err != nil || len(repos) != 1 || repos[0].FullName != "owner/repo" || len(calls) != 4 {
		t.Fatalf("repos=%+v err=%v calls=%v", repos, err, calls)
	}
	if _, err := g.Repositories(context.Background(), "ghs_installation_token"); !errors.Is(err, ErrConnect) {
		t.Fatal("accepted bot-wide token")
	}
}

func TestPinnedRepositoryCheckRevalidatesAppSelectionAndUserRole(t *testing.T) {
	role := "write"
	selected := true
	userID := int64(55)
	var permissionCalls int
	g := GitHub{AppID: 7, HTTPClient: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/user/installations":
			return response(`{"installations":[{"id":1,"app_id":7,"permissions":{"contents":"write","pull_requests":"write"}}]}`), nil
		case "/user/installations/1/repositories":
			if !selected {
				return response(`{"repositories":[]}`), nil
			}
			return response(`{"repositories":[{"id":10,"full_name":"owner/repo","default_branch":"main","permissions":{"push":true}},{"id":11,"full_name":"owner/unrelated","default_branch":"main","permissions":{"push":true}}]}`), nil
		case "/repos/owner/repo/collaborators/developer/permission":
			permissionCalls++
			raw, _ := json.Marshal(map[string]any{"permission": role, "user": map[string]any{"id": userID, "login": "developer"}})
			return response(string(raw)), nil
		default:
			t.Fatal("queried unrelated repository", r.URL.Path)
			return nil, ErrDenied
		}
	})}}
	repo := Repository{ID: 10, InstallationID: 1, FullName: "owner/repo", DefaultBranch: "main"}
	user := Account{ID: 55, Login: "developer"}
	ctx := context.Background()
	if err := g.CheckRepository(ctx, "ghu_private", user, repo); err != nil || permissionCalls != 1 {
		t.Fatal("pinned check failed", err)
	}
	role = "read"
	if err := g.CheckRepository(ctx, "ghu_private", user, repo); !errors.Is(err, ErrDenied) {
		t.Fatal("read-only user accepted")
	}
	role = "write"
	userID = 999
	if err := g.CheckRepository(ctx, "ghu_private", user, repo); !errors.Is(err, ErrDenied) {
		t.Fatal("another user's role accepted")
	}
	userID = 55
	selected = false
	if err := g.CheckRepository(ctx, "ghu_private", user, repo); !errors.Is(err, ErrDenied) {
		t.Fatal("unselected repo accepted")
	}
	selected = true
	g.AppID = 8
	if err := g.CheckRepository(ctx, "ghu_private", user, repo); !errors.Is(err, ErrDenied) {
		t.Fatal("different App accepted")
	}
}

func TestGitHubOAuthPKCEAndErrorRedaction(t *testing.T) {
	g := GitHub{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://nudgy.example/github/oauth/callback"}
	u, err := url.Parse(g.AuthorizeURL("state", "verifier"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("code_challenge_method") != "S256" || len(u.Query().Get("code_challenge")) != 43 {
		t.Fatal("missing PKCE")
	}
	g.HTTPClient = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://github.com/login/oauth/access_token" || r.Method != "POST" {
			t.Fatal("wrong OAuth destination")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("client_secret") != "secret" || r.Form.Get("code_verifier") != "verifier" {
			t.Fatal("missing OAuth arguments")
		}
		return response(`{"access_token":"ghu_token","refresh_token":"ghr_refresh","expires_in":28800,"refresh_token_expires_in":15897600}`), nil
	})}
	c, err := g.Exchange(context.Background(), "code", "verifier")
	if err != nil || c.Expires.IsZero() || c.RefreshExpires.IsZero() {
		t.Fatal("token exchange failed")
	}
	g.HTTPClient = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("secret in URL") })}
	_, _, err = g.User(context.Background(), "ghu_token")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("upstream credential leaked")
	}
}

func TestGitHubDoesNotFollowRedirectsOrReturnPartialFailure(t *testing.T) {
	calls := 0
	g := GitHub{AppID: 7, HTTPClient: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		r := response(`{"private":"secret"}`)
		r.StatusCode = 302
		r.Header.Set("Location", "https://attacker.invalid")
		return r, nil
	})}}
	if _, err := g.Repositories(context.Background(), "ghu_secret"); err == nil || calls != 1 {
		t.Fatalf("followed redirect: %v %d", err, calls)
	}
	for _, status := range []int{401, 403, 404, 429, 500} {
		g.HTTPClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			r := response(`secret`)
			r.StatusCode = status
			return r, nil
		})
		if repos, err := g.Repositories(context.Background(), "ghu_secret"); err == nil || repos != nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("upstream failure exposed or treated as empty success")
		}
	}
}

func TestGitHubTraversesRepositoryPages(t *testing.T) {
	pages := 0
	g := GitHub{AppID: 7, HTTPClient: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/user" {
			return response(`{"id":55,"login":"developer"}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/collaborators/developer/permission") {
			return response(`{"permission":"write","user":{"id":55,"login":"developer"}}`), nil
		}
		if r.URL.Path == "/user/installations" {
			return response(`{"installations":[{"id":1,"app_id":7,"permissions":{"contents":"write","pull_requests":"write"}}]}`), nil
		}
		pages++
		if r.URL.Query().Get("page") == "2" {
			return response(`{"repositories":[{"id":101,"full_name":"owner/last","default_branch":"master","permissions":{"push":true}}]}`), nil
		}
		items := make([]map[string]any, 100)
		for i := range items {
			items[i] = map[string]any{"id": i + 1, "full_name": "owner/repo", "default_branch": "main", "permissions": map[string]bool{"push": true}}
		}
		raw, _ := json.Marshal(map[string]any{"repositories": items})
		return response(string(raw)), nil
	})}}
	repos, err := g.Repositories(context.Background(), "ghu_secret")
	if err != nil || len(repos) != 101 || pages != 2 {
		t.Fatalf("pagination: %d %d %v", len(repos), pages, err)
	}
}

func TestInstallationPushFlagDoesNotAuthorizeAnotherUsersRepository(t *testing.T) {
	for _, tc := range []struct {
		name, permission   string
		responseUser       int64
		status             int
		allowed, wantError bool
	}{
		{"write", "write", 55, 200, true, false},
		{"owner", "admin", 55, 200, true, false},
		{"read only despite push flag", "read", 55, 200, false, false},
		{"no collaborator grant", "none", 55, 200, false, false},
		{"missing role", "", 55, 200, false, false},
		{"another user role", "admin", 99, 200, false, false},
		{"forbidden", "", 0, 403, false, false},
		{"not a collaborator", "", 0, 404, false, false},
		{"revoked token", "", 0, 401, false, true},
		{"rate limited", "", 0, 429, false, true},
		{"upstream failure", "", 0, 500, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checked := false
			g := GitHub{AppID: 7, HTTPClient: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer ghu_requesting_user" {
					t.Fatal("used different credential")
				}
				switch r.URL.Path {
				case "/user":
					return response(`{"id":55,"login":"latifatcii"}`), nil
				case "/user/installations":
					return response(`{"installations":[{"id":1,"app_id":7,"permissions":{"contents":"write","pull_requests":"write"}}]}`), nil
				case "/user/installations/1/repositories":
					return response(`{"repositories":[{"id":10,"full_name":"BanuKarakaya/Beybi","default_branch":"main","permissions":{"push":true}}]}`), nil
				case "/repos/BanuKarakaya/Beybi/collaborators/latifatcii/permission":
					checked = true
					body, _ := json.Marshal(map[string]any{"permission": tc.permission, "user": map[string]any{"id": tc.responseUser}})
					r := response(string(body))
					r.StatusCode = tc.status
					return r, nil
				default:
					t.Fatalf("unexpected request: %s", r.URL.Path)
					return nil, ErrUnavailable
				}
			})}}
			repos, err := g.Repositories(context.Background(), "ghu_requesting_user")
			if !checked || (err != nil) != tc.wantError || (len(repos) == 1) != tc.allowed {
				t.Fatalf("repos=%v err=%v checked=%t", repos, err, checked)
			}
		})
	}
}
