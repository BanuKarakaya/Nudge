package coding

import (
	"strings"
	"testing"
	"time"
)

func reviewFixture() Review {
	id := strings.Repeat("a", 32)
	return Review{
		TaskID: id, Repository: Repository{ID: 10, InstallationID: 2, FullName: "owner/repo", DefaultBranch: "main"},
		BaseCommit: strings.Repeat("1", 40), BaseTree: strings.Repeat("2", 40), Branch: "codex/nudgy-" + id,
		Summary: "Reject blank titles", CreatedAt: time.Unix(1700000000, 0).UTC(),
		Changes: []FileChange{{RepositoryFile: RepositoryFile{Path: "src/tasks.go", Mode: "100644", Content: []byte("package tasks\n")}}},
		Tests:   "not_run",
	}
}

func TestReviewBindsApprovalToExactCodeAndRepository(t *testing.T) {
	baseline, err := reviewFixture().Digest()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"code", "base", "repository", "mode", "deletion"} {
		t.Run(scenario, func(t *testing.T) {
			r := reviewFixture()
			switch scenario {
			case "code":
				r.Changes[0].Content = []byte("modified after approval")
			case "base":
				r.BaseCommit = strings.Repeat("3", 40)
			case "repository":
				r.Repository.ID++
			case "mode":
				r.Changes[0].Mode = "100755"
			case "deletion":
				r.Changes[0].Delete = true
				r.Changes[0].Content = nil
			}
			got, err := r.Digest()
			if err != nil || got == baseline {
				t.Fatalf("changed proposal retained approval: digest=%s err=%v", got, err)
			}
		})
	}
}

func TestReviewRejectsUnsafePathsAndFabricatedTests(t *testing.T) {
	for _, name := range []string{"../secret", "/etc/passwd", ".git/config", "dir/.GIT/config", "a/../b", "a\\b", "a\nb", ".", "", string([]byte{255})} {
		r := reviewFixture()
		r.Changes[0].Path = name
		if _, err := r.Digest(); err == nil {
			t.Fatalf("unsafe path accepted: %q", name)
		}
	}
	for _, scenario := range []string{"fabricated tests", "main branch", "symlink", "duplicate", "directory collision", "oversized", "invalid base"} {
		t.Run(scenario, func(t *testing.T) {
			r := reviewFixture()
			switch scenario {
			case "fabricated tests":
				r.Tests = "passed"
			case "main branch":
				r.Branch = "main"
			case "symlink":
				r.Changes[0].Mode = "120000"
			case "duplicate":
				r.Changes = append(r.Changes, r.Changes[0])
			case "directory collision":
				r.Changes = append(r.Changes, FileChange{RepositoryFile: RepositoryFile{Path: "src", Mode: "100644"}})
			case "oversized":
				r.Changes[0].Content = make([]byte, MaxChangeBytes+1)
			case "invalid base":
				r.BaseCommit = "main"
			}
			if _, err := r.Digest(); err == nil {
				t.Fatal("unsafe/unverified review accepted")
			}
		})
	}
}

func TestReviewDigestIgnoresFileOrderWithoutMutatingPayload(t *testing.T) {
	r := reviewFixture()
	r.Changes = append(r.Changes, FileChange{RepositoryFile: RepositoryFile{Path: "README.md", Mode: "100644", Content: []byte("readme")}})
	a, err := r.Digest()
	if err != nil || r.Changes[0].Path != "src/tasks.go" {
		t.Fatal("digest changed review file order")
	}
	r.Changes[0], r.Changes[1] = r.Changes[1], r.Changes[0]
	b, _ := r.Digest()
	if a != b {
		t.Fatal("ordering changed approval digest")
	}
}
