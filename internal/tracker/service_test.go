package tracker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu          sync.Mutex
	states      map[string]State
	connections map[string]Connection
}

func newMemoryStore() *memoryStore {
	return &memoryStore{states: map[string]State{}, connections: map[string]Connection{}}
}
func (*memoryStore) SlackTeam(_ context.Context, id int64) (string, error) {
	if id == 1 {
		return "T", nil
	}
	return "", ErrDenied
}
func (m *memoryStore) PutState(_ context.Context, p, h string, s State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[p+h] = s
	return nil
}
func (m *memoryStore) ConsumeState(_ context.Context, p, h string) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.states[p+h]
	delete(m.states, p+h)
	if !ok {
		return s, ErrConnect
	}
	return s, nil
}
func (m *memoryStore) Save(_ context.Context, c Connection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connections[c.Provider] = c
	return nil
}
func (m *memoryStore) WithConnection(_ context.Context, p string, id int64, fn func(*Connection) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.connections[p]
	if !ok || c.UserID != id {
		return ErrConnect
	}
	if err := fn(&c); err != nil {
		return err
	}
	m.connections[p] = c
	return nil
}
func (m *memoryStore) Disconnect(_ context.Context, p string, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.connections, p)
	for key, state := range m.states {
		if state.UserID == id && len(key) >= len(p) && key[:len(p)] == p {
			delete(m.states, key)
		}
	}
	return nil
}

type fakeProvider struct {
	kind             string
	account          Account
	resources        []Resource
	refreshes, reads int
	accountError     error
}

func (p *fakeProvider) Kind() string { return p.kind }
func (*fakeProvider) AuthorizeURL(s, v string) string {
	return "https://auth.atlassian.com/authorize?state=" + s
}
func (*fakeProvider) Exchange(context.Context, string, string) (Credentials, error) {
	return Credentials{"access-secret", "refresh-secret", time.Now().Add(time.Hour)}, nil
}
func (p *fakeProvider) Refresh(context.Context, string) (Credentials, error) {
	p.refreshes++
	return Credentials{"rotated-access", "rotated-refresh", time.Now().Add(time.Hour)}, nil
}
func (p *fakeProvider) Account(context.Context, string) (Account, error) {
	return p.account, p.accountError
}
func (p *fakeProvider) Resources(context.Context, string) ([]Resource, error) {
	return p.resources, nil
}
func (p *fakeProvider) Items(context.Context, string, Resource, string) (Page, error) {
	p.reads++
	return Page{Items: []Item{{ID: "item"}}}, nil
}
func (p *fakeProvider) Item(context.Context, string, Resource, string) (Item, error) {
	p.reads++
	return Item{ID: "item"}, nil
}
func connectFixture(t *testing.T, kind string) (*Service, *memoryStore, *fakeProvider) {
	t.Helper()
	store := newMemoryStore()
	p := &fakeProvider{kind: kind, account: Account{"account", "Name"}, resources: []Resource{{ID: "selected"}, {ID: "other"}}}
	s := New(store, p)
	state, _, err := s.Begin(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	selection, _, err := s.Selection(context.Background(), state, "code")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Complete(context.Background(), selection, []string{"selected"}); err != nil {
		t.Fatal(err)
	}
	return s, store, p
}
func TestSelectionReplayExpiryAndFailedReconnect(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"jira", "trello"} {
		t.Run(kind, func(t *testing.T) {
			s, store, _ := connectFixture(t, kind)
			state, _, _ := s.Begin(ctx, 1)
			selection, _, err := s.Selection(ctx, state, "code")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.Selection(ctx, state, "code"); !errors.Is(err, ErrConnect) {
				t.Fatal("OAuth replay", err)
			}
			if _, err = s.Complete(ctx, selection, []string{"unselected"}); !errors.Is(err, ErrDenied) {
				t.Fatal("forged selection", err)
			}
			if _, err = s.Complete(ctx, selection, []string{"selected"}); !errors.Is(err, ErrConnect) {
				t.Fatal("selection replay", err)
			}
			if got := store.connections[kind].Selected; len(got) != 1 || got[0] != "selected" {
				t.Fatal("failed reconnect changed saved connection")
			}
			state, _, _ = s.Begin(ctx, 1)
			saved := store.states[kind+stateHash(state)]
			saved.Expires = time.Now().Add(-time.Minute)
			store.states[kind+stateHash(state)] = saved
			if _, _, err = s.Selection(ctx, state, "code"); !errors.Is(err, ErrConnect) {
				t.Fatal("expired state", err)
			}
		})
	}
}
func TestSelectedResourcesIdentityAndRevocation(t *testing.T) {
	ctx := context.Background()
	s, _, p := connectFixture(t, "jira")
	id := Identity{1, "T"}
	access, err := s.Access(ctx, id)
	if err != nil || len(access.Resources) != 1 {
		t.Fatal(access, err)
	}
	for _, bad := range []Identity{{1, "OTHER"}, {2, "T"}, {0, ""}} {
		if _, err = s.Items(ctx, bad, "selected", ""); err == nil {
			t.Fatal("identity accepted")
		}
	}
	if _, err = s.Items(ctx, id, "other", ""); !errors.Is(err, ErrDenied) {
		t.Fatal("unselected resource read", err)
	}
	p.resources = []Resource{{ID: "other"}}
	if _, err = s.Item(ctx, id, "selected", "item"); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked resource read", err)
	}
	p.account.ID = "different-account"
	if _, err = s.Access(ctx, id); !errors.Is(err, ErrConnect) {
		t.Fatal("changed account", err)
	}
	if p.reads != 0 {
		t.Fatal("unauthorized reads", p.reads)
	}
}
func TestRefreshPersistsBeforeDownstreamFailureAndSerializes(t *testing.T) {
	ctx := context.Background()
	s, store, p := connectFixture(t, "trello")
	c := store.connections["trello"]
	c.Credentials.Expires = time.Now().Add(-time.Hour)
	store.connections["trello"] = c
	p.accountError = ErrUnavailable
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.Access(ctx, Identity{1, "T"}) }()
	}
	wg.Wait()
	if p.refreshes != 1 || store.connections["trello"].Credentials.Refresh != "rotated-refresh" {
		t.Fatal("rotating refresh token lost or reused", p.refreshes)
	}
	if store.connections["trello"].Selected[0] != "selected" {
		t.Fatal("refresh lost selection")
	}
}
func TestProviderIsolationAndDisconnect(t *testing.T) {
	ctx := context.Background()
	s, store, p := connectFixture(t, "jira")
	other := *p
	other.kind = "trello"
	tService := New(store, &other)
	state, _, _ := s.Begin(ctx, 1)
	if _, _, err := tService.Selection(ctx, state, "code"); !errors.Is(err, ErrConnect) {
		t.Fatal("cross-provider state accepted")
	}
	if _, _, err := s.Selection(ctx, state, "code"); err != nil {
		t.Fatal("other provider consumed state", err)
	}
	c := store.connections["jira"]
	c.Provider = "trello"
	store.connections["trello"] = c
	state, _, _ = s.Begin(ctx, 1)
	if err := s.Disconnect(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Selection(ctx, state, "code"); !errors.Is(err, ErrConnect) {
		t.Fatal("disconnect retained pending state")
	}
	if connected, _ := tService.Connected(ctx, 1); !connected {
		t.Fatal("disconnect changed other provider")
	}
}
