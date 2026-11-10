package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const glToken = "gitlab-test-access-very-private"
const glProject = `{"id":10,"path_with_namespace":"team/subgroup/app","default_branch":"main","permissions":{"project_access":{"access_level":30}}}`

func gitLabFixture(t *testing.T) (*Service, *memoryStore, *memoryEdits, *recordingGit, *int64, *int) {
	t.Helper()
	subject := int64(55)
	role := 30
	g := GitLab{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://nudgy.example/gitlab/oauth/callback", HTTPClient: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "gitlab.com" {
			t.Fatal("credential sent to foreign host")
		}
		switch r.URL.Path {
		case "/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("client_secret") != "secret" || r.Form.Get("redirect_uri") != "https://nudgy.example/gitlab/oauth/callback" {
				t.Fatal("bad token exchange")
			}
			if r.Form.Get("grant_type") == "authorization_code" && len(r.Form.Get("code_verifier")) < 43 {
				t.Fatal("PKCE missing")
			}
			return response(`{"access_token":"` + glToken + `","refresh_token":"refresh-token-very-private","expires_in":7200,"token_type":"Bearer","scope":"read_user read_api write_repository api"}`), nil
		case "/api/v4/user":
			return response(fmt.Sprintf(`{"id":%d,"username":"developer","state":"active","commit_email":"developer@example.com"}`, subject)), nil
		case "/api/v4/projects":
			if r.Header.Get("Authorization") != "Bearer "+glToken || r.URL.Query().Get("min_access_level") != "30" {
				t.Fatal("missing user-scoped access")
			}
			return response("[" + glProject + `,{"id":11,"path_with_namespace":"team/other","default_branch":"main","permissions":{"group_access":{"access_level":40}}},{"id":12,"path_with_namespace":"team/readonly","default_branch":"main","permissions":{"project_access":{"access_level":20}}}]`), nil
		case "/api/v4/projects/10":
			return response(fmt.Sprintf(`{"id":10,"path_with_namespace":"team/subgroup/app","default_branch":"main","permissions":{"group_access":{"access_level":%d}}}`, role)), nil
		default:
			t.Fatal("unexpected path", r.URL.Path)
			return nil, ErrUnavailable
		}
	})}}
	store := &memoryStore{}
	s, err := NewGitLab(store, g)
	if err != nil {
		t.Fatal(err)
	}
	edits := &memoryEdits{}
	git := &recordingGit{}
	_ = s.EnableEditing(edits, git)
	return s, store, edits, git, &subject, &role
}
func connectGitLabFixture(t *testing.T, s *Service, store *memoryStore) {
	t.Helper()
	ctx := context.Background()
	state, redirect, err := s.BeginGitLab(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	if u.Host != "gitlab.com" || u.Query().Get("state") != state || u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("scope") != "read_user read_api write_repository api" {
		t.Fatal("invalid OAuth URL")
	}
	next, account, repos, err := s.GitLabSelection(ctx, state, "code")
	if err != nil || account.ID != 55 || len(repos) != 2 {
		t.Fatal("selection failed", err, repos)
	}
	if store.connection.UserID != 0 {
		t.Fatal("OAuth activated all projects before selection")
	}
	if _, _, _, err := s.GitLabSelection(ctx, state, "code"); err == nil {
		t.Fatal("OAuth replay accepted")
	}
	if _, err := s.CompleteGitLabSelection(ctx, next, []int64{10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteGitLabSelection(ctx, next, []int64{11}); err == nil {
		t.Fatal("selection replay accepted")
	}
}
func TestGitLabSelectionEditingAndPublishingIsolation(t *testing.T) {
	s, store, _, git, subject, role := gitLabFixture(t)
	connectGitLabFixture(t, s, store)
	s.WithBranchRules(&testBranchRules{template: "team/{repo}/{task_id}"})
	ctx := context.Background()
	i := Identity{UserID: 1, Team: "T", Channel: "C", Thread: "thread", Event: "E", Request: "change screen"}
	repos, err := s.Repositories(ctx, i)
	if err != nil || len(repos) != 1 || repos[0].FullName != "team/subgroup/app" {
		t.Fatal("unselected project leaked", repos, err)
	}
	if _, err := s.BeginEdit(ctx, i, "team/other"); err == nil || git.clones != 0 {
		t.Fatal("unselected project cloned")
	}
	info, err := s.BeginEdit(ctx, i, "team/subgroup/app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteEditFile(ctx, i, info.Task.ID, "screen.swift", "new screen", false); err != nil {
		t.Fatal(err)
	}
	*role = 20
	if _, err := s.FinishAndPublish(ctx, i, info.Task.ID, "screen"); err == nil || git.pushes != 0 {
		t.Fatal("revoked write access pushed")
	}
	*role = 30
	*subject = 99
	if _, err := s.EditStatus(ctx, i, info.Task.ID); err == nil {
		t.Fatal("swapped token subject accepted")
	}
	*subject = 55
	for _, other := range []Identity{{UserID: 2, Team: "T", Channel: "C", Thread: "thread"}, {UserID: 1, Team: "OTHER", Channel: "C", Thread: "thread"}} {
		if _, err := s.EditStatus(ctx, other, info.Task.ID); err == nil {
			t.Fatal("foreign Slack identity accepted")
		}
	}
	result, err := s.FinishAndPublish(ctx, i, info.Task.ID, "screen")
	if err != nil || result.Task.Status != "pushed" || git.pushes != 1 || result.Task.Provider != "gitlab" {
		t.Fatal("GitLab publish failed", err)
	}
	if git.review.Repository.Origin() != "https://gitlab.com" || !strings.Contains(git.review.Repository.BranchURL(result.Branch), "/-/tree/team/app/") {
		t.Fatal("wrong publishing destination")
	}
	// A GitHub service must not read even a numerically identical GitLab task.
	github, _ := New(store, &fakeProvider{})
	_ = github.EnableEditing(s.edits, git)
	if _, err := github.EditStatus(ctx, i, info.Task.ID); err == nil {
		t.Fatal("GitHub service read GitLab task")
	}
}
func TestGitLabRefreshPreservesSelectionAndFailedSelectionDoesNotReplaceConnection(t *testing.T) {
	s, store, _, _, _, _ := gitLabFixture(t)
	connectGitLabFixture(t, s, store)
	ctx := context.Background()
	store.connection.Credentials.Expires = time.Now().Add(-time.Hour)
	repos, err := s.Repositories(ctx, Identity{UserID: 1, Team: "T"})
	if err != nil || len(repos) != 1 || store.connection.Credentials.SelectedProjects != "[10]" {
		t.Fatal("refresh lost selection", err)
	}
	before := store.connection
	state, _, _ := s.BeginGitLab(ctx, 1)
	next, _, _, _ := s.GitLabSelection(ctx, state, "code")
	if _, err := s.CompleteGitLabSelection(ctx, next, []int64{999}); err == nil {
		t.Fatal("unknown project accepted")
	}
	if store.connection != before {
		t.Fatal("failed setup replaced working connection")
	}
}
func TestGitLabPaginationAndHTTPFailuresFailClosed(t *testing.T) {
	for _, status := range []int{302, 401, 403, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			g := GitLab{HTTPClient: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				resp := response(`[]`)
				resp.StatusCode = status
				resp.Header.Set("Location", "https://other.example")
				return resp, nil
			})}}
			if _, err := g.Repositories(context.Background(), glToken); err == nil || calls != 1 {
				t.Fatal("error or redirect treated as success")
			}
		})
	}
	pages := 0
	g := GitLab{HTTPClient: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		pages++
		items := []json.RawMessage{}
		if pages == 1 {
			for range 100 {
				items = append(items, json.RawMessage(glProject))
			}
		} else {
			items = append(items, json.RawMessage(`{"id":11,"path_with_namespace":"group/second","default_branch":"main","permissions":{"group_access":{"access_level":40}}}`))
		}
		raw, _ := json.Marshal(items)
		return response(string(raw)), nil
	})}}
	repos, err := g.Repositories(context.Background(), glToken)
	if err != nil || pages != 2 || len(repos) != 2 {
		t.Fatal("pagination incomplete", err)
	}
}
