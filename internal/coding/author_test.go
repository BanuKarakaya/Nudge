package coding

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestCommitAuthorIsProviderVerifiedAndDigestBound(t *testing.T) {
	a, err := (GitHub{}).CommitAuthor(context.Background(), "", Account{ID: 55, Login: "developer"})
	if err != nil || a.Email != "55+developer@users.noreply.github.com" {
		t.Fatal(a, err)
	}
	r := reviewFixture()
	before, _ := r.Digest()
	r.Author = &a
	after, err := r.Digest()
	if err != nil || before == after {
		t.Fatal("author not bound to review")
	}
	r.Author.Email = "injected\nemail@example.com"
	if r.Validate() == nil {
		t.Fatal("invalid author accepted")
	}
	for _, subject := range []string{"55", "99"} {
		g := GitLab{HTTPClient: &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/api/v4/user" || req.Header.Get("Authorization") != "Bearer user-token" {
				t.Fatal("wrong identity source")
			}
			return response(`{"id":` + subject + `,"username":"developer","commit_email":"verified@example.com"}`), nil
		})}}
		a, err := g.CommitAuthor(context.Background(), "user-token", Account{ID: 55, Login: "developer"})
		if subject == "55" && (err != nil || a.Email != "verified@example.com") {
			t.Fatal(a, err)
		}
		if subject == "99" && err == nil {
			t.Fatal("swapped token author accepted")
		}
	}
}
func TestLocalGitAttributesCommitToVerifiedRequester(t *testing.T) {
	g, repo, remote, run := localGitFixture(t)
	ctx := context.Background()
	snapshot, err := g.Clone(ctx, "ghu_test", repo)
	if err != nil {
		t.Fatal(err)
	}
	r := reviewFixture()
	r.Repository = repo
	r.BaseCommit = snapshot.Commit
	r.BaseTree = snapshot.Tree
	r.Author = &CommitAuthor{AccountID: 55, Name: "developer", Email: "55+developer@users.noreply.github.com"}
	commit, err := g.Push(ctx, "ghu_test", r)
	if err != nil {
		t.Fatal(err)
	}
	actual := run("--git-dir="+remote, "show", "-s", "--format=%an <%ae>|%cn|%B", commit)
	if !strings.Contains(actual, "developer <55+developer@users.noreply.github.com>|Nudgy|") || !strings.Contains(actual, "Nudgy-Requested-By: github:55:developer") {
		t.Fatal("lost attribution", actual)
	}
	again, err := g.Push(ctx, "ghu_test", r)
	if err != nil || again != commit {
		t.Fatal("author broke retry stability", err)
	}
}
