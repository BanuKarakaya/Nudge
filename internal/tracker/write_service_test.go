package tracker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type writeFake struct {
	*fakeProvider
	creates atomic.Int32
	moves   atomic.Int32
	failure error
	started chan struct{}
	proceed chan struct{}
}

func (p *writeFake) CreateOptions(context.Context, string, Resource, string) (CreateOptions, error) {
	return CreateOptions{}, nil
}
func (p *writeFake) MoveOptions(context.Context, string, Resource, string) ([]Option, error) {
	return nil, nil
}
func (p *writeFake) Create(context.Context, string, Resource, CreateInput) (MutationResult, error) {
	p.creates.Add(1)
	if p.started != nil {
		close(p.started)
		<-p.proceed
	}
	return MutationResult{Status: "created", Item: Item{ID: "new-item"}}, p.failure
}
func (p *writeFake) Move(context.Context, string, Resource, MoveInput) (MutationResult, error) {
	p.moves.Add(1)
	return MutationResult{Status: "moved", Item: Item{ID: "item"}}, p.failure
}
func writeFixture(t *testing.T) (*Service, *memoryStore, *writeFake) {
	s, store, base := connectFixture(t, "trello")
	p := &writeFake{fakeProvider: base}
	s.provider = p
	conn := store.connections["trello"]
	conn.Credentials.Scopes = "read:board:trello write:board:trello"
	store.connections["trello"] = conn
	return s, store, p
}
func TestWriteAuthorizationAndLegacyRefresh(t *testing.T) {
	for _, scenario := range []string{"other-user", "other-team", "unselected", "readonly", "empty-event", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			s, store, p := writeFixture(t)
			id, event, resource := Identity{1, "T"}, "event", "selected"
			if scenario == "other-user" {
				id.UserID = 2
			}
			if scenario == "other-team" {
				id.Team = "other"
			}
			if scenario == "unselected" {
				resource = "other"
			}
			if scenario == "empty-event" {
				event = ""
			}
			if scenario == "revoked" {
				p.resources = nil
			}
			if scenario == "readonly" {
				conn := store.connections["trello"]
				conn.Credentials.Scopes = ""
				conn.Credentials.Expires = time.Now().Add(-time.Minute)
				store.connections["trello"] = conn
				if _, err := s.Items(context.Background(), id, resource, ""); err != nil {
					t.Fatal("old read connection broken", err)
				}
			}
			if _, err := s.Create(context.Background(), id, event, resource, CreateInput{Title: "Task"}); err == nil {
				t.Fatal("unauthorized write accepted")
			}
			if p.creates.Load() != 0 {
				t.Fatal("provider called")
			}
		})
	}
}
func TestMutationReplayAndUnknownOutcome(t *testing.T) {
	for _, failure := range []error{nil, ErrOutcomeUnknown, ErrRejected} {
		s, store, p := writeFixture(t)
		p.failure = failure
		input := CreateInput{Title: "Task", Destination: "list"}
		first, err := s.Create(context.Background(), Identity{1, "T"}, "event", "selected", input)
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
		// A new service instance exercises persisted receipts, not process memory.
		restarted := New(store, p)
		again, err := restarted.Create(context.Background(), Identity{1, "T"}, "event", "selected", input)
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if failure == nil && (again.Item.ID != first.Item.ID || again.Status != "created") {
			t.Fatal(again)
		}
		want := int32(1)
		if failure == ErrRejected {
			want = 2
		}
		if p.creates.Load() != want {
			t.Fatal("unsafe replay", p.creates.Load())
		}
	}
}
func TestConcurrentMutationReservation(t *testing.T) {
	s, _, p := writeFixture(t)
	p.started = make(chan struct{})
	p.proceed = make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := s.Create(context.Background(), Identity{1, "T"}, "same-event", "selected", CreateInput{Title: "Task"}); err != nil {
			t.Error(err)
		}
	}()
	<-p.started
	_, err := s.Create(context.Background(), Identity{1, "T"}, "same-event", "selected", CreateInput{Title: "Task"})
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatal(err)
	}
	close(p.proceed)
	wg.Wait()
	if p.creates.Load() != 1 {
		t.Fatal("duplicate write")
	}
}
func TestMoveReplayAndScopeSurvivesRefresh(t *testing.T) {
	s, store, p := writeFixture(t)
	c := store.connections["trello"]
	c.Credentials.Expires = time.Now().Add(-time.Hour)
	store.connections["trello"] = c
	for range 2 {
		result, err := s.Move(context.Background(), Identity{1, "T"}, "event", "selected", MoveInput{Item: "item", Destination: "testing", ExpectedStatus: "doing"})
		if err != nil || result.Status != "moved" {
			t.Fatal(result, err)
		}
	}
	if p.moves.Load() != 1 || p.refreshes != 1 {
		t.Fatal("replay or lost scope")
	}
}
