package coding

import (
	"context"
	"errors"
	"nudge/internal/workspace"
	"strings"
	"testing"
)

type testWorkspacePolicy struct {
	scope workspace.Scope
	err   error
}

func (p *testWorkspacePolicy) Scope(context.Context, int64, string) (workspace.Scope, error) {
	return p.scope, p.err
}
func TestWorkspaceMemberVerifiesExistingInstallation(t *testing.T) {
	s, store, _, _ := fixture(t)
	ctx := context.Background()
	p := &testWorkspacePolicy{scope: workspace.Scope{Enabled: true, Resources: []workspace.Resource{{ID: "10"}}}}
	s.WithWorkspacePolicy(p)
	state, redirect, err := s.BeginVerification(ctx, 1)
	if err != nil || !strings.Contains(redirect, "/login/oauth/authorize") || store.states[stateHash(state)].InstallationID != 0 {
		t.Fatalf("personal verification: %s %v", redirect, err)
	}
	// Fake VerifyInstallation rejects ID 0: members must instead use live repository access.
	if _, err = s.CompleteConnectAccount(ctx, state, "code"); err != nil {
		t.Fatal(err)
	}
	state, _, err = s.BeginVerification(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	p.scope.Enabled = false
	if _, err = s.CompleteConnectAccount(ctx, state, "code"); !errors.Is(err, ErrDenied) {
		t.Fatalf("disabled during OAuth: %v", err)
	}
	if _, _, err = s.BeginVerification(ctx, 1); !errors.Is(err, ErrDenied) {
		t.Fatalf("disabled connection: %v", err)
	}
}
func TestWorkspaceScopeIntersectsLiveUserAccess(t *testing.T) {
	s, _, provider, id := fixture(t)
	ctx := context.Background()
	p := &testWorkspacePolicy{scope: workspace.Scope{Enabled: true, Resources: []workspace.Resource{{ID: "10"}, {ID: "999"}}}}
	s.WithWorkspacePolicy(p)
	access, err := s.RepositoryAccess(ctx, id)
	if err != nil || len(access.Repositories) != 1 || access.Repositories[0].ID != 10 {
		t.Fatalf("intersection: %+v %v", access, err)
	}
	if _, err = s.Prepare(ctx, id, "owner/another"); !errors.Is(err, ErrDenied) {
		t.Fatalf("unselected repo: %v", err)
	}
	p.scope.Resources = []workspace.Resource{{ID: "11"}}
	access, err = s.RepositoryAccess(ctx, id)
	if err != nil || len(access.Repositories) != 1 || access.Repositories[0].ID != 11 {
		t.Fatalf("scope changes require no reconnect: %+v %v", access, err)
	}
	provider.repos = nil
	access, err = s.RepositoryAccess(ctx, id)
	if err != nil || len(access.Repositories) != 0 {
		t.Fatalf("revoked personal access: %+v %v", access, err)
	}
	p.err = errors.New("database offline")
	if _, err = s.RepositoryAccess(ctx, id); err == nil {
		t.Fatal("policy error failed open")
	}
}
func TestWorkspaceManagerSetupDoesNotEnableOperations(t *testing.T) {
	s, _, _, id := fixture(t)
	ctx := context.Background()
	p := &testWorkspacePolicy{scope: workspace.Scope{Manager: true}}
	s.WithWorkspacePolicy(p)
	access, err := s.SetupAccess(ctx, id)
	if err != nil || len(access.Repositories) != 2 {
		t.Fatalf("manager setup: %+v %v", access, err)
	}
	if _, err = s.RepositoryAccess(ctx, id); !errors.Is(err, ErrDenied) {
		t.Fatalf("setup bypassed disabled scope: %v", err)
	}
	p.scope.Manager = false
	if _, err = s.SetupAccess(ctx, id); !errors.Is(err, ErrDenied) {
		t.Fatal("member enumerated setup resources")
	}
}

func TestWorkspaceGitLabVerificationUsesSharedProjects(t *testing.T) {
	s, store, _, _, _, _ := gitLabFixture(t)
	ctx := context.Background()
	p := &testWorkspacePolicy{scope: workspace.Scope{Enabled: true, Resources: []workspace.Resource{{ID: "10"}}}}
	s.WithWorkspacePolicy(p)
	state, _, err := s.BeginGitLab(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	next, account, repos, err := s.GitLabSelection(ctx, state, "code")
	if err != nil || next != "" || account.ID != 55 || len(repos) != 0 {
		t.Fatalf("employee selection: %s %+v %v", next, account, err)
	}
	if store.connection.Credentials.SelectedProjects != "" {
		t.Fatal("personal project selection persisted")
	}
	a, err := s.RepositoryAccess(ctx, Identity{UserID: 1, Team: "T"})
	if err != nil || len(a.Repositories) != 1 || a.Repositories[0].ID != 10 {
		t.Fatal("shared projects not enforced", a, err)
	}
	p.scope.Enabled = false
	if _, err = s.RepositoryAccess(ctx, Identity{UserID: 1, Team: "T"}); !errors.Is(err, ErrDenied) {
		t.Fatal("disabled GitLab access", err)
	}
}

func TestWorkspaceManagerCanVerifyBeforeChoosingSharedScope(t *testing.T) {
	s, _, _, _ := fixture(t)
	s.WithWorkspacePolicy(&testWorkspacePolicy{scope: workspace.Scope{Manager: true}})
	state, _, err := s.BeginVerification(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteConnectAccount(context.Background(), state, "code"); err != nil {
		t.Fatal("manager cannot repair setup", err)
	}
}
