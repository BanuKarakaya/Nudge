package coding

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type identityProvider struct {
	Provider
	users     map[string]Account
	mu        sync.Mutex
	repoCalls int
	userErr   error
}

func (p *identityProvider) User(_ context.Context, token string) (int64, string, error) {
	a, ok := p.users[token]
	if p.userErr != nil {
		return 0, "", p.userErr
	}
	if !ok {
		return 0, "", ErrConnect
	}
	return a.ID, a.Login, nil
}

func (p *identityProvider) Repositories(_ context.Context, token string) ([]Repository, error) {
	p.mu.Lock()
	p.repoCalls++
	p.mu.Unlock()
	a, ok := p.users[token]
	if !ok {
		return nil, ErrConnect
	}
	return []Repository{{ID: a.ID, FullName: a.Login + "/private", DefaultBranch: "main", InstallationID: 1}}, nil
}

func TestRepositoryAccessVerifiesTokenSubjectBeforeListing(t *testing.T) {
	for _, tc := range []struct {
		name      string
		storedID  int64
		lookupErr error
		refresh   bool
		want      error
	}{
		{"swapped token", 999, nil, false, ErrConnect},
		{"unknown stored identity", 0, nil, false, ErrConnect},
		{"revoked token", 55, ErrConnect, false, ErrConnect},
		{"GitHub unavailable", 55, ErrUnavailable, false, ErrUnavailable},
		{"refreshed token has wrong owner", 999, nil, true, ErrConnect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, base, i := fixture(t)
			store.connection.GitHubUserID = tc.storedID
			if tc.refresh {
				store.connection.Credentials.Expires = time.Now().Add(-time.Hour)
				store.connection.Credentials.Refresh = "ghr_old"
				store.connection.Credentials.RefreshExpires = time.Now().Add(time.Hour)
			}
			before := store.connection.Credentials
			p := &identityProvider{Provider: base, users: map[string]Account{"ghu_secret": {55, "developer"}, "ghu_new": {55, "developer"}}, userErr: tc.lookupErr}
			s.provider = p
			access, err := s.RepositoryAccess(context.Background(), i)
			if !errors.Is(err, tc.want) || len(access.Repositories) != 0 || access.Account.ID != 0 || p.repoCalls != 0 {
				t.Fatalf("identity failure requested/exposed repositories: access=%+v err=%v calls=%d", access, err, p.repoCalls)
			}
			if store.connection.Credentials != before {
				t.Fatal("committed unverified refreshed credentials")
			}
		})
	}
}

type identityStore struct {
	Store
	connections map[int64]Connection
}

func (s identityStore) WithConnection(_ context.Context, id int64, fn func(*Connection) error) error {
	c, ok := s.connections[id]
	if !ok {
		return ErrConnect
	}
	return fn(&c)
}

func TestTwoUsersInSameSlackThreadNeverShareGitHubAccess(t *testing.T) {
	_, base, _, identity := fixture(t)
	s, _ := New(identityStore{Store: base, connections: map[int64]Connection{
		1: {UserID: 1, Team: "T", GitHubUserID: 55, Login: "banu", Credentials: Credentials{Access: "ghu_banu"}},
		2: {UserID: 2, Team: "T", GitHubUserID: 66, Login: "latifatcii", Credentials: Credentials{Access: "ghu_latif"}},
	}}, &identityProvider{users: map[string]Account{"ghu_banu": {55, "banu"}, "ghu_latif": {66, "latifatcii"}}})
	var wg sync.WaitGroup
	for range 12 {
		for _, user := range []struct {
			id    int64
			login string
		}{{1, "banu"}, {2, "latifatcii"}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				i := identity
				i.UserID = user.id // workspace, channel and thread remain identical
				access, err := s.RepositoryAccess(context.Background(), i)
				if err != nil || access.Account.Login != user.login || len(access.Repositories) != 1 || access.Repositories[0].FullName != user.login+"/private" {
					t.Errorf("cross-user access: user=%d access=%+v err=%v", user.id, access, err)
				}
			}()
		}
	}
	wg.Wait()
}

func TestStoredSlackUserMismatchFailsClosed(t *testing.T) {
	s, store, base, i := fixture(t)
	wrong := store.connection
	wrong.UserID = 2
	s.store = identityStore{Store: store, connections: map[int64]Connection{1: wrong}}
	p := &identityProvider{Provider: base, users: map[string]Account{"ghu_secret": {55, "developer"}}}
	s.provider = p
	if _, err := s.Repositories(context.Background(), i); !errors.Is(err, ErrDenied) || p.repoCalls != 0 {
		t.Fatalf("used another Slack user's connection: %v", err)
	}
}

func TestGitHubRenameKeepsImmutableIdentity(t *testing.T) {
	s, _, base, i := fixture(t)
	s.provider = &identityProvider{Provider: base, users: map[string]Account{"ghu_secret": {55, "renamed"}}}
	a, err := s.Account(context.Background(), i)
	if err != nil || a.ID != 55 || a.Login != "renamed" {
		t.Fatalf("rename: %+v %v", a, err)
	}
}
