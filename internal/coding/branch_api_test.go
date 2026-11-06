package coding

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestBranchProvidersEncodeExactNamesAndPaginate(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			r := Repository{ID: 10, InstallationID: 2, FullName: "owner/repo"}
			host, path, key := "api.github.com", "/repos/owner/repo/branches", "sha"
			if kind == "gitlab" {
				r.Provider = kind
				r.InstallationID = 0
				host, path, key = "gitlab.com", "/api/v4/projects/10/repository/branches", "id"
			}
			name := "feature/ödeme-v2"
			sha := strings.Repeat("a", 40)
			client := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.Host != host || req.Header.Get("Authorization") != "Bearer private" {
					t.Fatal("wrong request destination")
				}
				raw := fmt.Sprintf(`{"name":%q,"commit":{%q:%q}}`, name, key, sha)
				if req.URL.Path == path {
					if req.URL.Query().Get("page") != "2" || req.URL.Query().Get("per_page") != "100" {
						t.Fatal("lost pagination")
					}
					return response("[" + raw + "]"), nil
				}
				if req.URL.EscapedPath() != path+"/"+url.PathEscape(name) {
					t.Fatal("unescaped branch", req.URL.EscapedPath())
				}
				return response(raw), nil
			})}
			var p BranchProvider = GitHub{HTTPClient: client}
			if kind == "gitlab" {
				p = GitLab{HTTPClient: client}
			}
			branch, err := p.GetBranch(context.Background(), "private", r, name)
			if err != nil || branch.Name != name || branch.SHA != sha {
				t.Fatal(branch, err)
			}
			list, err := p.ListBranches(context.Background(), "private", r, 2)
			if err != nil || len(list) != 1 || list[0] != branch {
				t.Fatal(list, err)
			}
			for _, invalid := range []string{"", "HEAD", "refs/heads/main", "../main", "x//y", "main~1", "x.lock", "a b", "x@{1}"} {
				if _, err := p.GetBranch(context.Background(), "private", r, invalid); err == nil {
					t.Fatal("invalid ref accepted", invalid)
				}
			}
		})
	}
}

func TestGitLabBranchMergeUsesSelectedProjectAndChosenTarget(t *testing.T) {
	s, store, _, _, _, _ := gitLabFixture(t)
	connectGitLabFixture(t, s, store)
	s.store = &pinStore{memoryStore: store}
	g := s.provider.(GitLab)
	original := g.HTTPClient.Transport
	created, merged := false, false
	sha := strings.Repeat("a", 40)
	source, target := "existing/feature", "development"
	g.HTTPClient = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/repository/branches/") {
			name := strings.TrimPrefix(req.URL.Path, "/api/v4/projects/10/repository/branches/")
			if name != source && name != target {
				t.Fatal("guessed branch", name)
			}
			return response(fmt.Sprintf(`{"name":%q,"commit":{"id":%q}}`, name, sha)), nil
		}
		if !strings.Contains(req.URL.Path, "/merge_requests") {
			return original.RoundTrip(req)
		}
		state := "opened"
		if merged {
			state = "merged"
		}
		raw := strings.ReplaceAll(strings.ReplaceAll(gitlabChangeJSON(state, sha), "codex/nudgy-task", source), `"main"`, `"development"`)
		switch req.Method {
		case "POST":
			created = true
			return response(raw), nil
		case "PUT":
			merged = true
			return response(strings.Replace(raw, `"opened"`, `"merged"`, 1)), nil
		default:
			if strings.HasSuffix(req.URL.Path, "/merge_requests") {
				if created {
					return response("[" + raw + "]"), nil
				}
				return response("[]"), nil
			}
			return response(raw), nil
		}
	})}
	s.provider = g
	i := Identity{UserID: 1, Team: "T", Channel: "C", Thread: "1", Event: "merge-branches", Request: "gitlab team/subgroup/app existing/feature -> development merge et"}
	got, err := s.MergeBranches(context.Background(), i, "team/subgroup/app", source, target)
	if err != nil || got.State != "merged" || got.TargetBranch != target || !created || !merged {
		t.Fatal(got, err)
	}
	if _, err := s.MergeBranches(context.Background(), i, "team/other", source, target); err == nil {
		t.Fatal("model changed authorized repository")
	}
}
