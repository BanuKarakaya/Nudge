package coding

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

type memoryEdits struct {
	mu    sync.Mutex
	items map[string]Edit
}

func copyEdit(e Edit) Edit {
	raw, _ := json.Marshal(e)
	var result Edit
	_ = json.Unmarshal(raw, &result)
	result.Task.Identity = e.Task.Identity
	return result
}
func (m *memoryEdits) CreateEdit(_ context.Context, e Edit) (Edit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil {
		m.items = map[string]Edit{}
	}
	for _, saved := range m.items {
		if sameScope(saved.Task.Identity, e.Task.Identity) && saved.Task.Identity.Event == e.Task.Identity.Event {
			return copyEdit(saved), nil
		}
	}
	m.items[e.Task.ID] = copyEdit(e)
	return e, nil
}
func (m *memoryEdits) GetEdit(_ context.Context, user int64, id string) (Edit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.items[id]
	if !ok || e.Task.Identity.UserID != user {
		return Edit{}, ErrNotFound
	}
	return copyEdit(e), nil
}
func (m *memoryEdits) WithEdit(_ context.Context, user int64, id string, fn func(*Edit) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.items[id]
	if !ok || e.Task.Identity.UserID != user {
		return ErrNotFound
	}
	e = copyEdit(e)
	if err := fn(&e); err != nil {
		return err
	}
	m.items[id] = copyEdit(e)
	return nil
}

type recordingGit struct {
	clones, pushes int
	review         Review
	failPush       bool
}

func (g *recordingGit) Clone(_ context.Context, _ string, r Repository) (Snapshot, error) {
	g.clones++
	return Snapshot{Repository: r, Commit: strings.Repeat("1", 40), Tree: strings.Repeat("2", 40), Files: []RepositoryFile{{Path: "src/app.go", Mode: "100644", Content: []byte("before\n")}, {Path: "README.md", Mode: "100644", Content: []byte("hello\n")}}}, nil
}
func (g *recordingGit) Push(_ context.Context, _ string, r Review) (string, error) {
	g.pushes++
	g.review = r
	if g.failPush {
		return "", errors.New("lost response")
	}
	return strings.Repeat("3", 40), nil
}

func editFixture(t *testing.T) (*Service, *memoryEdits, *recordingGit, *fakeProvider, *memoryStore, Identity, EditInfo) {
	t.Helper()
	s, connections, p, i := fixture(t)
	edits := &memoryEdits{}
	git := &recordingGit{}
	if err := s.EnableEditing(edits, git); err != nil {
		t.Fatal(err)
	}
	info, err := s.BeginEdit(context.Background(), i, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	return s, edits, git, p, connections, i, info
}
func finishFixture(t *testing.T, s *Service, i Identity, id string) EditInfo {
	t.Helper()
	ctx := context.Background()
	if _, err := s.WriteEditFile(ctx, i, id, "src/app.go", "after\n", false); err != nil {
		t.Fatal(err)
	}
	info, err := s.FinishEdit(ctx, i, id, "Update app")
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestEditLifecycleDoesNotPushUntilExactLaterApproval(t *testing.T) {
	s, store, git, _, _, i, info := editFixture(t)
	ctx := context.Background()
	retry, err := s.BeginEdit(ctx, i, "owner/repo")
	if err != nil || retry.Task.ID != info.Task.ID || git.clones != 1 {
		t.Fatal("clone was not idempotent", err)
	}
	ready := finishFixture(t, s, i, info.Task.ID)
	if ready.Task.Status != "awaiting_user_test" || ready.Tests != "not_run" || git.pushes != 0 {
		t.Fatal("preparation executed/pushed code")
	}
	if got, err := s.ReadEditFile(ctx, i, info.Task.ID, "src/app.go", 1, 20); err != nil || got != "after\n" {
		t.Fatal("file was not changed", got, err)
	}
	for _, request := range []string{"ok", "pushla", "please " + ready.ApprovalCommand, ready.ApprovalCommand + " ama şimdi yapma"} {
		other := i
		other.Event = "APPROVAL"
		other.Request = request
		if _, err := s.PushEdit(ctx, other, info.Task.ID); err == nil {
			t.Fatal("accepted ambiguous approval", request)
		}
	}
	i.Request = ready.ApprovalCommand
	if _, err := s.PushEdit(ctx, i, info.Task.ID); err == nil {
		t.Fatal("same-event approval accepted")
	}
	i.Event = "APPROVAL"
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.PushEdit(ctx, i, info.Task.ID)
			if err != nil || got.Task.Status != "pushed" {
				t.Error("approved push failed", err)
			}
		}()
	}
	wg.Wait()
	if git.pushes != 1 || git.review.Changes[0].Path != "src/app.go" || string(git.review.Changes[0].Content) != "after\n" {
		t.Fatal("wrong or duplicate push")
	}
	stored, _ := store.GetEdit(ctx, i.UserID, info.Task.ID)
	if stored.Task.Instruction != info.Task.Instruction || stored.Digest != ready.Digest {
		t.Fatal("immutable request/review changed")
	}
	if _, err := s.WriteEditFile(ctx, i, info.Task.ID, "src/app.go", "third", false); err == nil {
		t.Fatal("edited published review")
	}
}

func TestEditsRejectOtherUsersThreadsAndRevokedPermissions(t *testing.T) {
	s, _, git, p, connections, i, info := editFixture(t)
	ctx := context.Background()
	ready := finishFixture(t, s, i, info.Task.ID)
	for _, field := range []string{"user", "team", "channel", "thread"} {
		other := i
		switch field {
		case "user":
			other.UserID = 2
		case "team":
			other.Team = "OTHER"
		case "channel":
			other.Channel = "OTHER"
		case "thread":
			other.Thread = "OTHER"
		}
		other.Event = "APPROVAL"
		other.Request = ready.ApprovalCommand
		if _, err := s.EditStatus(ctx, other, info.Task.ID); err == nil {
			t.Fatal("cross-scope read", field)
		}
		if _, err := s.WriteEditFile(ctx, other, info.Task.ID, "README.md", "bad", false); err == nil {
			t.Fatal("cross-scope write", field)
		}
		if _, err := s.PushEdit(ctx, other, info.Task.ID); err == nil {
			t.Fatal("cross-scope push", field)
		}
		if _, err := s.CancelEdit(ctx, other, info.Task.ID); err == nil {
			t.Fatal("cross-scope cancel", field)
		}
	}
	i.Event = "APPROVAL"
	i.Request = ready.ApprovalCommand
	repos := p.repos
	p.repos = nil
	if _, err := s.PushEdit(ctx, i, info.Task.ID); err == nil {
		t.Fatal("revoked access still pushed")
	}
	if _, err := s.ReviewEdit(ctx, i.UserID, info.Task.ID, ready.Digest); err == nil {
		t.Fatal("revoked access still downloaded")
	}
	p.repos = repos
	connections.connection.GitHubUserID = 999
	if _, err := s.PushEdit(ctx, i, info.Task.ID); err == nil {
		t.Fatal("swapped GitHub identity pushed")
	}
	if git.pushes != 0 {
		t.Fatal("denied operation reached Git")
	}
}

func TestNewFileChangeInvalidatesReviewAndOldApproval(t *testing.T) {
	s, _, git, _, _, i, info := editFixture(t)
	ctx := context.Background()
	old := finishFixture(t, s, i, info.Task.ID)
	i.Event = "REVISION"
	if _, err := s.WriteEditFile(ctx, i, info.Task.ID, "src/app.go", "third\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewEdit(ctx, i.UserID, info.Task.ID, old.Digest); err == nil {
		t.Fatal("old private review remained valid")
	}
	newReview, err := s.FinishEdit(ctx, i, info.Task.ID, "revision")
	if err != nil {
		t.Fatal(err)
	}
	i.Event = "APPROVAL"
	i.Request = old.ApprovalCommand
	if _, err := s.PushEdit(ctx, i, info.Task.ID); err == nil {
		t.Fatal("stale approval accepted")
	}
	if git.pushes != 0 || old.Digest == newReview.Digest {
		t.Fatal("review was not versioned")
	}
	i.Request = newReview.ApprovalCommand
	if _, err := s.PushEdit(ctx, i, info.Task.ID); err != nil {
		t.Fatal(err)
	}
}

func TestEditRejectsUnsafePathsAndCancelInvalidatesReview(t *testing.T) {
	s, _, git, _, _, i, info := editFixture(t)
	ctx := context.Background()
	for _, name := range []string{"../.env", "/etc/passwd", ".git/config", "src/app.go/child", ".github/workflows/steal.yml"} {
		if _, err := s.WriteEditFile(ctx, i, info.Task.ID, name, "bad", false); err == nil {
			t.Fatal("unsafe file write", name)
		}
	}
	if _, err := s.FinishEdit(ctx, i, info.Task.ID, "no actual changes"); err == nil {
		t.Fatal("finished without code")
	}
	ready := finishFixture(t, s, i, info.Task.ID)
	if _, err := s.CancelEdit(ctx, i, info.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewEdit(ctx, i.UserID, info.Task.ID, ready.Digest); err == nil {
		t.Fatal("cancelled review accessible")
	}
	i.Event = "APPROVAL"
	i.Request = ready.ApprovalCommand
	if _, err := s.PushEdit(ctx, i, info.Task.ID); err == nil {
		t.Fatal("cancelled job pushed")
	}
	if git.pushes != 0 {
		t.Fatal("unexpected push")
	}
}

func TestFailedPushRetainsFrozenReviewForRetry(t *testing.T) {
	s, store, git, _, _, i, info := editFixture(t)
	ctx := context.Background()
	ready := finishFixture(t, s, i, info.Task.ID)
	i.Event = "APPROVAL"
	i.Request = ready.ApprovalCommand
	git.failPush = true
	if _, err := s.PushEdit(ctx, i, info.Task.ID); err == nil {
		t.Fatal("failed push reported success")
	}
	e, _ := store.GetEdit(ctx, i.UserID, info.Task.ID)
	if e.Task.Status != "awaiting_user_test" || e.Digest != ready.Digest {
		t.Fatal("failed push destroyed review")
	}
	git.failPush = false
	if _, err := s.PushEdit(ctx, i, info.Task.ID); err != nil {
		t.Fatal(err)
	}
}
