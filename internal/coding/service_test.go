package coding

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	Store
	mu         sync.Mutex
	connection Connection
	states     map[string]OAuthState
	tasks      map[string]Task
}

func (s *memoryStore) SlackTeam(_ context.Context, id int64) (string, error) {
	if id == 1 || id == 2 {
		return "T", nil
	}
	return "", ErrDenied
}

func (s *memoryStore) SaveConnection(_ context.Context, c Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connection = c
	return nil
}
func (s *memoryStore) WithConnection(_ context.Context, id int64, fn func(*Connection) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connection.UserID != id {
		return ErrConnect
	}
	return fn(&s.connection)
}
func (s *memoryStore) PutOAuthState(_ context.Context, hash string, state OAuthState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = make(map[string]OAuthState)
	}
	s.states[hash] = state
	return nil
}
func (s *memoryStore) ConsumeOAuthState(_ context.Context, hash string) (OAuthState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[hash]
	delete(s.states, hash)
	if !ok {
		return OAuthState{}, ErrConnect
	}
	return state, nil
}
func (s *memoryStore) CreateDraft(_ context.Context, task Task) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tasks == nil {
		s.tasks = make(map[string]Task)
	}
	key := fmt.Sprintf("%d:%s:%s:%s", task.Identity.UserID, task.Identity.Team, task.Identity.Channel, task.Identity.Event)
	if existing, ok := s.tasks[key]; ok {
		return existing, nil
	}
	s.tasks[key] = task
	return task, nil
}
func (s *memoryStore) Task(_ context.Context, i Identity, id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tasks {
		if t.ID == id && t.Identity.UserID == i.UserID && t.Identity.Team == i.Team && t.Identity.Channel == i.Channel && t.Identity.Thread == i.Thread {
			return t, nil
		}
	}
	return Task{}, ErrNotFound
}

type fakeProvider struct {
	Provider
	repos     []Repository
	err       error
	mu        sync.Mutex
	refreshes int
}

func (p *fakeProvider) Repositories(context.Context, string) ([]Repository, error) {
	return p.repos, p.err
}
func (p *fakeProvider) Refresh(context.Context, string) (Credentials, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshes++
	return Credentials{Access: "ghu_new", Refresh: "ghr_new", Expires: time.Now().Add(time.Hour), RefreshExpires: time.Now().Add(24 * time.Hour)}, nil
}
func (*fakeProvider) AuthorizeURL(state, verifier string) string {
	return "https://github.com/login/oauth/authorize?state=" + state
}
func (*fakeProvider) Exchange(context.Context, string, string) (Credentials, error) {
	return Credentials{Access: "ghu_new"}, nil
}
func (*fakeProvider) User(context.Context, string) (int64, string, error) {
	return 55, "developer", nil
}

func fixture(t *testing.T) (*Service, *memoryStore, *fakeProvider, Identity) {
	t.Helper()
	store := &memoryStore{connection: Connection{UserID: 1, Team: "T", Credentials: Credentials{Access: "ghu_secret"}}}
	p := &fakeProvider{repos: []Repository{{ID: 10, FullName: "owner/repo", DefaultBranch: "master", InstallationID: 2}, {ID: 11, FullName: "owner/another", DefaultBranch: "main", InstallationID: 2}}}
	s, err := New(store, p)
	if err != nil {
		t.Fatal(err)
	}
	return s, store, p, Identity{UserID: 1, Team: "T", Channel: "C", Thread: "1.0", Event: "E", Request: "x dosyasını düzelt, test et ve PR aç"}
}

func TestDraftIsBoundToOriginalMessageAndDeduplicated(t *testing.T) {
	s, store, _, i := fixture(t)
	a, err := s.Prepare(context.Background(), i, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	i.Request = "changed model content"
	b, err := s.Prepare(context.Background(), i, "OWNER/REPO")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || b.Instruction == i.Request || b.Status != "draft" || len(store.tasks) != 1 {
		t.Fatalf("draft changed on retry: %+v", b)
	}
	if b.Repository.DefaultBranch != "master" {
		t.Fatal("guessed base branch")
	}
}

func TestPermissionAndTaskOwnershipBoundaries(t *testing.T) {
	s, _, p, i := fixture(t)
	repos, err := s.Repositories(context.Background(), i)
	if err != nil || len(repos) != 2 {
		t.Fatalf("GitHub permissions: %v %v", repos, err)
	}
	for _, name := range []string{"owner/not-allowed", "https://github.com/owner/repo", "owner/../../other", "repo"} {
		if _, err := s.Prepare(context.Background(), i, name); err == nil {
			t.Fatal("accepted " + name)
		}
	}
	task, err := s.Prepare(context.Background(), i, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
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
		if _, err := s.Task(context.Background(), other, task.ID, false); err == nil {
			t.Fatalf("leaked task across %s", field)
		}
	}
	p.repos = nil
	i.Event = "new"
	if _, err := s.Prepare(context.Background(), i, "owner/repo"); !errors.Is(err, ErrDenied) {
		t.Fatalf("cached revoked access: %v", err)
	}
	p.err = ErrUnavailable
	if _, err := s.Repositories(context.Background(), i); !errors.Is(err, ErrUnavailable) {
		t.Fatal("failure treated as empty inventory")
	}
}

func TestRefreshIsSerializedAndNeverUsesAnotherUsersToken(t *testing.T) {
	s, store, p, i := fixture(t)
	store.connection.Credentials = Credentials{Access: "old", Refresh: "refresh", Expires: time.Now().Add(-time.Hour), RefreshExpires: time.Now().Add(time.Hour)}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Repositories(context.Background(), i); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if p.refreshes != 1 {
		t.Fatalf("refreshes=%d", p.refreshes)
	}
	i.UserID = 2
	if _, err := s.Repositories(context.Background(), i); !errors.Is(err, ErrConnect) {
		t.Fatal("used another user's connection")
	}
}

func TestOAuthStateIsSingleUseHashedAndExpires(t *testing.T) {
	s, store, _, _ := fixture(t)
	state, _, err := s.BeginConnect(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.states[state]; ok {
		t.Fatal("raw bearer state stored")
	}
	if len(store.states[stateHash(state)].Verifier) < 43 {
		t.Fatal("weak PKCE verifier")
	}
	if err := s.CompleteConnect(context.Background(), state, "code"); err != nil {
		t.Fatal(err)
	}
	if store.connection.GitHubUserID != 55 {
		t.Fatal("identity not read from GitHub")
	}
	if store.connection.Team != "T" {
		t.Fatal("OAuth did not bind GitHub to persisted Slack workspace")
	}
	if err := s.CompleteConnect(context.Background(), state, "code"); err == nil {
		t.Fatal("OAuth replay accepted")
	}
	state, _, _ = s.BeginConnect(context.Background(), 1)
	v := store.states[stateHash(state)]
	v.Expires = time.Now().Add(-time.Minute)
	store.states[stateHash(state)] = v
	if err := s.CompleteConnect(context.Background(), state, "code"); err == nil {
		t.Fatal("expired OAuth accepted")
	}
}

func TestGitHubRepositoryChangesApplyWithoutServerConfiguration(t *testing.T) {
	s, _, p, i := fixture(t)
	ctx := context.Background()
	// This repository was never in any server list. GitHub's grant is enough.
	if _, err := s.Prepare(ctx, i, "owner/another"); err != nil {
		t.Fatal(err)
	}
	p.repos = append(p.repos, Repository{ID: 12, FullName: "other-org/new-repo", DefaultBranch: "main", InstallationID: 3})
	i.Event = "new-event"
	if _, err := s.Prepare(ctx, i, "other-org/new-repo"); err != nil {
		t.Fatalf("new GitHub grant not visible: %v", err)
	}
	p.repos = p.repos[:2]
	i.Event = "after-removal"
	if _, err := s.Prepare(ctx, i, "other-org/new-repo"); !errors.Is(err, ErrDenied) {
		t.Fatalf("removed GitHub grant retained: %v", err)
	}
}

func TestWorkspaceMismatchCannotRefreshOrUseConnection(t *testing.T) {
	s, store, p, i := fixture(t)
	store.connection.Credentials.Expires = time.Now().Add(-time.Hour)
	store.connection.Credentials.Refresh = "ghr_secret"
	store.connection.Credentials.RefreshExpires = time.Now().Add(time.Hour)
	i.Team = "OTHER"
	if _, err := s.Repositories(context.Background(), i); !errors.Is(err, ErrDenied) {
		t.Fatalf("cross-workspace listing: %v", err)
	}
	if p.refreshes != 0 {
		t.Fatal("foreign workspace refreshed credentials")
	}
	i.Team = "T"
	store.connection.Team = "OTHER"
	if _, err := s.Repositories(context.Background(), i); !errors.Is(err, ErrDenied) {
		t.Fatalf("mismatched stored connection used: %v", err)
	}
	if p.refreshes != 0 {
		t.Fatal("mismatched stored connection refreshed")
	}
	if _, _, err := s.BeginConnect(context.Background(), 99); err == nil {
		t.Fatal("unregistered Slack user started OAuth")
	}
}
