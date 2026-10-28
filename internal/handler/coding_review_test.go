package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nudge/internal/coding"
)

type reviewStore struct {
	coding.EditStore
	edit  coding.Edit
	calls int
}

func (s *reviewStore) GetEdit(_ context.Context, userID int64, id string) (coding.Edit, error) {
	s.calls++
	if userID != s.edit.Task.Identity.UserID || id != s.edit.Task.ID {
		return coding.Edit{}, coding.ErrNotFound
	}
	return s.edit, nil
}

type reviewGit struct{ coding.GitWorkspace }

func reviewHandlerFixture(t *testing.T) (*CodingHandler, *reviewStore, string) {
	t.Helper()
	h, connections, links, _ := codingHandlerFixture(t)
	connections.saved = coding.Connection{UserID: 1, Team: "T", GitHubUserID: 42, Login: "developer", Credentials: coding.Credentials{Access: "ghu_never_expose"}}
	identity := coding.Identity{UserID: 1, Team: "T", Channel: "C", Thread: "thread", Event: "request"}
	repo := coding.Repository{ID: 1, InstallationID: 2, FullName: "developer/private", DefaultBranch: "main"}
	id := strings.Repeat("a", 32)
	r := coding.Review{TaskID: id, Repository: repo, BaseCommit: strings.Repeat("1", 40), BaseTree: strings.Repeat("2", 40), Branch: "codex/nudgy-" + id, Tests: "not_run", Summary: "Fix <script>alert('xss')</script>", CreatedAt: time.Now().UTC(), Changes: []coding.FileChange{{RepositoryFile: coding.RepositoryFile{Path: "index.html", Mode: "100644", Content: []byte("<script>source</script>")}}, {RepositoryFile: coding.RepositoryFile{Path: "removed.txt", Mode: "100644"}, Delete: true}}}
	digest, err := r.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &reviewStore{edit: coding.Edit{Task: coding.Task{ID: id, Identity: identity, Repository: repo, Status: "awaiting_user_test"}, AccountID: 42, Review: r, Digest: digest, Snapshot: coding.Snapshot{Repository: repo, Commit: r.BaseCommit, Tree: r.BaseTree, Files: []coding.RepositoryFile{{Path: "index.html", Mode: "100644", Content: []byte("before")}, {Path: "removed.txt", Mode: "100644", Content: []byte("delete")}, {Path: "keep.bin", Mode: "100755", Content: []byte{0, 1, 2, 255}}}}}}
	if err := h.service.EnableEditing(store, reviewGit{}); err != nil {
		t.Fatal(err)
	}
	token, err := links.Token(1, reviewPurpose(id, digest), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"user_id": {"1"}, "task_id": {id}, "digest": {digest}, "token": {token}}
	return h, store, "/github/review?" + q.Encode()
}

func TestPrivateReviewEscapesSourceAndNeverPushes(t *testing.T) {
	h, _, link := reviewHandlerFixture(t)
	response := httptest.NewRecorder()
	h.review(response, httptest.NewRequest(http.MethodGet, link, nil))
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, bad := range []string{"<script>alert", "<script>source", "ghu_never_expose"} {
		if strings.Contains(body, bad) {
			t.Fatal("unsafe review", bad)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "Otomatik test çalıştırılmadı", "aynı Slack konuşmasına", "Pushla", "workspace"} {
		if !strings.Contains(body, want) {
			t.Fatal("missing review context", want)
		}
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(response.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatal("private page missing headers")
	}
}

func TestPrivateReviewRejectsForeignAndStaleCapabilities(t *testing.T) {
	h, store, link := reviewHandlerFixture(t)
	for _, field := range []string{"user_id", "task_id", "digest", "token"} {
		u, _ := url.Parse(link)
		q := u.Query()
		q.Set(field, "different")
		u.RawQuery = q.Encode()
		response := httptest.NewRecorder()
		h.review(response, httptest.NewRequest(http.MethodGet, u.String(), nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatal("unbound capability", field, response.Code)
		}
	}
	if store.calls != 0 {
		t.Fatal("invalid capability reached private storage")
	}
	store.edit.Task.Status = "editing"
	response := httptest.NewRecorder()
	h.review(response, httptest.NewRequest(http.MethodGet, link, nil))
	if response.Code != http.StatusForbidden {
		t.Fatal("changed review still downloadable")
	}
}

func TestReviewZIPContainsExactWorkingCopy(t *testing.T) {
	h, store, link := reviewHandlerFixture(t)
	response := httptest.NewRecorder()
	h.review(response, httptest.NewRequest(http.MethodGet, link+"&download=zip", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/zip" {
		t.Fatal("download failed")
	}
	r, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range r.File {
		handle, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(handle)
		handle.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = data
		if f.Name == "workspace/keep.bin" && f.Mode().Perm() != 0755 {
			t.Fatal("executable bit lost")
		}
	}
	if string(files["workspace/index.html"]) != "<script>source</script>" || !bytes.Equal(files["workspace/keep.bin"], []byte{0, 1, 2, 255}) {
		t.Fatal("working copy differs from approved content")
	}
	if _, ok := files["workspace/removed.txt"]; ok {
		t.Fatal("deleted file still present")
	}
	if !strings.Contains(string(files["NUDGY-REVIEW.txt"]), store.edit.Digest) {
		t.Fatal("archive does not identify reviewed revision")
	}
}

func TestReviewExplainsHowToRunXcodeCopyWithoutPushing(t *testing.T) {
	h, store, link := reviewHandlerFixture(t)
	store.edit.Snapshot.Files = append(store.edit.Snapshot.Files, coding.RepositoryFile{Path: "toDoList.xcodeproj/project.pbxproj", Mode: "100644", Content: []byte("project")})
	w := httptest.NewRecorder()
	h.review(w, httptest.NewRequest(http.MethodGet, link, nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, want := range []string{"workspace/toDoList.xcodeproj", "iPhone simülatörünü", "Run (⌘R)", "Push yapmadan test"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatal("missing simulator instruction", want)
		}
	}
	if strings.Contains(w.Body.String(), "pushla ") {
		t.Fatal("copy/paste approval still shown")
	}
}
