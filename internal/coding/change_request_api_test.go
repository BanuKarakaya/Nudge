package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func githubChangeJSON(state, sha string) string {
	return fmt.Sprintf(`{"number":12,"state":%q,"merged":%t,"mergeable":true,"mergeable_state":"clean","merge_commit_sha":%q,"head":{"ref":"codex/nudgy-task","sha":%q,"repo":{"id":10}},"base":{"ref":"main","repo":{"id":10}}}`, state, state == "closed", strings.Repeat("c", 40), sha)
}
func gitlabChangeJSON(state, sha string) string {
	return fmt.Sprintf(`{"iid":12,"project_id":10,"source_project_id":10,"target_project_id":10,"state":%q,"detailed_merge_status":"mergeable","source_branch":"codex/nudgy-task","target_branch":"main","sha":%q,"merge_commit_sha":%q}`, state, sha, strings.Repeat("c", 40))
}

func TestChangeRequestProviderCreateFindAndMerge(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			sha := strings.Repeat("a", 40)
			repo := Repository{ID: 10, InstallationID: 2, FullName: "owner/repo", DefaultBranch: "main"}
			host := "api.github.com"
			endpoint := "/repos/owner/repo/pulls"
			token := "ghu_secret"
			opened := githubChangeJSON("open", sha)
			merged := githubChangeJSON("closed", sha)
			if kind == "gitlab" {
				repo = Repository{Provider: kind, ID: 10, FullName: "team/sub/repo", DefaultBranch: "main"}
				host = "gitlab.com"
				endpoint = "/api/v4/projects/10/merge_requests"
				token = glToken
				opened = gitlabChangeJSON("opened", sha)
				merged = gitlabChangeJSON("merged", sha)
			}
			writes := 0
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != host || r.Header.Get("Authorization") != "Bearer "+token {
					t.Fatal("wrong credential destination")
				}
				switch {
				case r.Method == "GET" && r.URL.Path == endpoint:
					if r.URL.Query().Get("state") != "all" || r.URL.Query().Get("per_page") != "100" {
						t.Fatal("retry lookup omits completed requests")
					}
					return response("[" + opened + "]"), nil
				case r.Method == "GET" && r.URL.Path == endpoint+"/12":
					return response(opened), nil
				case r.Method == "POST" && r.URL.Path == endpoint:
					writes++
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Fatal("invalid JSON")
					}
					if body["title"] != "title" {
						t.Fatal("missing title")
					}
					if kind == "github" && (body["head"] != "codex/nudgy-task" || body["base"] != "main") {
						t.Fatal("wrong branches")
					}
					if kind == "gitlab" && (body["source_branch"] != "codex/nudgy-task" || body["target_branch"] != "main" || body["remove_source_branch"] != false) {
						t.Fatal("wrong branches")
					}
					out := response(opened)
					out.StatusCode = 201
					return out, nil
				case r.Method == "PUT" && r.URL.Path == endpoint+"/12/merge":
					writes++
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Fatal("invalid JSON")
					}
					if body["sha"] != sha {
						t.Fatal("missing revision guard")
					}
					if kind == "github" {
						if len(body) != 2 || body["merge_method"] != "merge" {
							t.Fatal("unexpected merge options")
						}
						return response(`{"merged":true,"sha":"` + strings.Repeat("c", 40) + `"}`), nil
					}
					if len(body) != 3 || body["auto_merge"] != false || body["should_remove_source_branch"] != false {
						t.Fatal("unsafe merge options")
					}
					return response(merged), nil
				default:
					t.Fatal("unexpected API call", r.Method, r.URL.Path)
					return nil, ErrUnavailable
				}
			})}
			var p ChangeRequestProvider = GitHub{HTTPClient: client}
			if kind == "gitlab" {
				p = GitLab{HTTPClient: client}
			}
			ctx := context.Background()
			existing, err := p.FindChange(ctx, token, repo, "codex/nudgy-task", "main")
			if err != nil || existing == nil || existing.Number != 12 {
				t.Fatal(existing, err)
			}
			created, err := p.CreateChange(ctx, token, repo, "codex/nudgy-task", "main", "title", "body")
			if err != nil || created.Number != 12 || created.State != "open" {
				t.Fatal(created, err)
			}
			result, err := p.MergeChange(ctx, token, repo, created)
			if err != nil || result.State != "merged" || writes != 2 {
				t.Fatal(result, err, writes)
			}
		})
	}
}

func TestMergeProviderRejectsRevisionChangesAndUnreadyRequests(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		for _, mode := range []string{"changed head", "changed base", "blocked", "draft", "wrong repository", "unknown readiness", "false success", "conflict"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				sha := strings.Repeat("a", 40)
				raw := githubChangeJSON("open", sha)
				repo := Repository{ID: 10, InstallationID: 2, FullName: "owner/repo"}
				if kind == "gitlab" {
					raw = gitlabChangeJSON("opened", sha)
					repo = Repository{Provider: kind, ID: 10, FullName: "owner/repo"}
				}
				switch mode {
				case "changed head":
					raw = strings.ReplaceAll(raw, sha, strings.Repeat("b", 40))
				case "changed base":
					raw = strings.ReplaceAll(raw, `"main"`, `"production"`)
				case "blocked":
					raw = strings.ReplaceAll(raw, `"clean"`, `"blocked"`)
					raw = strings.ReplaceAll(raw, `"mergeable"`, `"not_approved"`)
				case "draft":
					raw = strings.Replace(raw, `{`, `{"draft":true,`, 1)
				case "wrong repository":
					raw = strings.ReplaceAll(raw, `"id":10`, `"id":11`)
					raw = strings.ReplaceAll(raw, `"target_project_id":10`, `"target_project_id":11`)
				case "unknown readiness":
					raw = strings.ReplaceAll(raw, `"mergeable":true`, `"mergeable":null`)
					raw = strings.ReplaceAll(raw, `"detailed_merge_status":"mergeable"`, `"detailed_merge_status":"checking"`)
				}
				writes := 0
				client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
					if r.Method == "PUT" {
						writes++
						if mode == "conflict" {
							out := response(`{"secret":"do not expose"}`)
							out.StatusCode = 409
							return out, nil
						}
						if kind == "github" {
							return response(`{"merged":false}`), nil
						}
						return response(raw), nil
					}
					return response(raw), nil
				})}
				var p ChangeRequestProvider = GitHub{HTTPClient: client}
				if kind == "gitlab" {
					p = GitLab{HTTPClient: client}
				}
				_, err := p.MergeChange(context.Background(), "token", repo, ChangeRequest{Number: 12, HeadSHA: sha, TargetBranch: "main"})
				if err == nil {
					t.Fatal("unsafe merge succeeded")
				}
				if mode != "false success" && mode != "conflict" && writes != 0 {
					t.Fatal("invalid merge reached write")
				}
			})
		}
	}
}

func TestChangeAPIRejectsRedirectsAndSanitizesErrors(t *testing.T) {
	for _, status := range []int{301, 302, 401, 403, 404, 409, 422, 500} {
		calls := 0
		client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			out := response(`{"message":"private credential"}`)
			out.StatusCode = status
			out.Header = make(http.Header)
			out.Header.Set("Location", "https://evil.example/steal")
			return out, nil
		})}
		_, err := (GitHub{HTTPClient: client}).GetChange(context.Background(), "ghu_private", Repository{ID: 10, InstallationID: 2, FullName: "owner/repo"}, 12)
		if err == nil || strings.Contains(err.Error(), "private") || calls != 1 {
			t.Fatal("unsafe error/redirect", err, calls)
		}
	}
}

func TestGitLabOAuthAPIUpgradeAndLegacyRefresh(t *testing.T) {
	for _, scope := range []string{"", "read_user read_api write_repository", "read_user read_api write_repository api"} {
		g := GitLab{HTTPClient: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return response(fmt.Sprintf(`{"access_token":%q,"refresh_token":"long-refresh-token","expires_in":7200,"token_type":"Bearer","scope":%q}`, glToken, scope)), nil
		})}}
		c, err := g.Exchange(context.Background(), "code", "verifier")
		if strings.Contains(scope, " api") {
			if err != nil || c.Scopes != scope {
				t.Fatal("scope not persisted", err)
			}
		} else if !errors.Is(err, ErrAPIScope) {
			t.Fatal("new authorization accepted without api", err)
		}
		if _, err := g.Refresh(context.Background(), "old-refresh"); err != nil {
			t.Fatal("legacy credential refresh broken", err)
		}
	}
}

func TestGitLabServiceCreatesMRThenRequiresSeparateExplicitMerge(t *testing.T) {
	s, store, _, _, _, _ := gitLabFixture(t)
	connectGitLabFixture(t, s, store)
	s.store = &pinStore{memoryStore: store}
	g := s.provider.(GitLab)
	original := g.HTTPClient.Transport
	created, merges := false, 0
	source := ""
	sha := strings.Repeat("3", 40)
	g.HTTPClient = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/merge_requests") {
			return original.RoundTrip(r)
		}
		state := "opened"
		if merges > 0 {
			state = "merged"
		}
		raw := strings.ReplaceAll(gitlabChangeJSON(state, sha), "codex/nudgy-task", source)
		switch r.Method {
		case "POST":
			created = true
			out := response(raw)
			out.StatusCode = 201
			return out, nil
		case "PUT":
			merges++
			return response(strings.Replace(raw, `"opened"`, `"merged"`, 1)), nil
		default:
			if strings.HasSuffix(r.URL.Path, "/merge_requests") {
				if created {
					return response("[" + raw + "]"), nil
				}
				return response(`[]`), nil
			}
			return response(raw), nil
		}
	})}
	s.provider = g
	i := Identity{UserID: 1, Team: "T", Channel: "C", Thread: "1", Event: "edit-E", Request: "kodu düzelt ve MR aç"}
	ctx := context.Background()
	edit, err := s.BeginEdit(ctx, i, "team/subgroup/app")
	if err != nil {
		t.Fatal(err)
	}
	source = edit.Branch
	if _, err := s.WriteEditFile(ctx, i, edit.Task.ID, "src/app.go", "after", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishAndPublish(ctx, i, edit.Task.ID, "change"); err != nil {
		t.Fatal(err)
	}
	mr, err := s.CreateChangeRequest(ctx, i, edit.Task.ID, "MR title", "description")
	if err != nil || mr.Number != 12 || !created {
		t.Fatal(mr, err)
	}
	if _, err := s.MergeChangeRequest(ctx, i, "team/subgroup/app", 12); !errors.Is(err, ErrMergeRequest) {
		t.Fatal("MR creation implicitly authorized merge", err)
	}
	i.Event = "merge-E"
	i.Request = "merge " + mr.URL
	merged, err := s.MergeChangeRequest(ctx, i, "team/subgroup/app", 12)
	if err != nil || merged.State != "merged" || merges != 1 {
		t.Fatal(merged, err, merges)
	}
	if _, err := s.MergeChangeRequest(ctx, i, "team/subgroup/app", 12); err != nil || merges != 1 {
		t.Fatal("merge retry duplicated write", err)
	}
	i.Event = "unselected-E"
	i.Request = "merge https://gitlab.com/team/other/-/merge_requests/12"
	if _, err := s.MergeChangeRequest(ctx, i, "team/other", 12); !errors.Is(err, ErrDenied) {
		t.Fatal("unselected project merged", err)
	}
}
