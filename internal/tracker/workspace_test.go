package tracker

import (
	"context"
	"errors"
	"nudge/internal/workspace"
	"testing"
)

type testWorkspacePolicy struct {
	scope workspace.Scope
	err   error
}

func (p *testWorkspacePolicy) Scope(context.Context, int64, string) (workspace.Scope, error) {
	return p.scope, p.err
}
func TestWorkspaceVerificationNeedsNoPersonalResourceSelection(t *testing.T) {
	for _, kind := range []string{"jira", "trello"} {
		t.Run(kind, func(t *testing.T) {
			s, store, p := connectFixture(t, kind)
			ctx := context.Background()
			id := Identity{1, "T"}
			policy := &testWorkspacePolicy{scope: workspace.Scope{Enabled: true, Resources: []workspace.Resource{{ID: "selected"}, {ID: "not-accessible"}}}}
			s.WithWorkspacePolicy(policy)
			state, _, err := s.Begin(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			next, a, err := s.Selection(ctx, state, "code")
			if err != nil || next != "" || a.Account.ID != "account" {
				t.Fatalf("verification: %s %+v %v", next, a, err)
			}
			if len(store.connections[kind].Selected) != 0 {
				t.Fatal("employee selected resources")
			}
			a, err = s.Access(ctx, id)
			if err != nil || len(a.Resources) != 1 || a.Resources[0].ID != "selected" {
				t.Fatalf("intersection: %+v %v", a, err)
			}
			policy.scope.Resources = []workspace.Resource{{ID: "other"}}
			a, err = s.Access(ctx, id)
			if err != nil || len(a.Resources) != 1 || a.Resources[0].ID != "other" {
				t.Fatalf("live workspace change: %+v %v", a, err)
			}
			if _, err = s.Items(ctx, id, "selected", ""); !errors.Is(err, ErrDenied) {
				t.Fatalf("removed resource: %v", err)
			}
			if p.reads != 0 {
				t.Fatal("read outside workspace scope")
			}
			policy.scope.Enabled = false
			if _, err = s.Items(ctx, id, "other", ""); !errors.Is(err, ErrDenied) {
				t.Fatalf("disabled: %v", err)
			}
			if _, _, err = s.Begin(ctx, 1); !errors.Is(err, ErrDenied) {
				t.Fatalf("member bypassed setup: %v", err)
			}
			policy.scope.Manager = true
			if _, _, err = s.Begin(ctx, 1); err != nil {
				t.Fatal("manager cannot configure", err)
			}
			if _, err = s.SetupAccess(ctx, id); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestWorkspaceTrackerRechecksDisabledDuringOAuth(t *testing.T) {
	s, store, _ := connectFixture(t, "trello")
	ctx := context.Background()
	prior := store.connections["trello"]
	p := &testWorkspacePolicy{scope: workspace.Scope{Enabled: true}}
	s.WithWorkspacePolicy(p)
	state, _, err := s.Begin(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	p.scope.Enabled = false
	if _, _, err = s.Selection(ctx, state, "code"); !errors.Is(err, ErrDenied) {
		t.Fatalf("completed disabled OAuth: %v", err)
	}
	if store.connections["trello"].Credentials.Access != prior.Credentials.Access {
		t.Fatal("failed OAuth changed credentials")
	}
}
