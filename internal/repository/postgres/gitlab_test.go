package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/coding"
	"nudge/internal/secure"
)

func testGitLabStoreIsolation(t *testing.T, ctx context.Context, db *pgxpool.Pool, cipher secure.Cipher, gh *CodingStore) {
	gl, err := NewGitLabStore(ctx, db, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewGitLabStore(ctx, db, cipher); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	c := coding.Connection{UserID: 1, Team: "T", GitHubUserID: 42, Login: "same-numeric-id", Credentials: coding.Credentials{Access: "gitlab-secret-only", SelectedProjects: "[10]"}}
	if err := gl.SaveConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	ghConnection := c
	ghConnection.Credentials.Access = "github-separate-secret"
	if err := gh.SaveConnection(ctx, ghConnection); err != nil {
		t.Fatal(err)
	}
	var encrypted string
	if err := db.QueryRow(ctx, `SELECT credentials FROM gitlab_connections WHERE user_id=1`).Scan(&encrypted); err != nil || strings.Contains(encrypted, c.Credentials.Access) {
		t.Fatal("GitLab token not encrypted", err)
	}
	state := coding.OAuthState{UserID: 1, Phase: "gitlab-select", Expires: time.Now().Add(time.Minute), Pending: &c}
	if err := gl.PutOAuthState(ctx, "same-state", state); err != nil {
		t.Fatal(err)
	}
	if err := gh.PutOAuthState(ctx, "same-state", coding.OAuthState{UserID: 1, Phase: "oauth", Verifier: "github-verifier", Expires: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT verifier FROM gitlab_oauth_states WHERE state_hash='same-state'`).Scan(&encrypted); err != nil || strings.Contains(encrypted, "gitlab-secret") {
		t.Fatal("pending token not encrypted", err)
	}
	restored, err := gl.ConsumeOAuthState(ctx, "same-state")
	if err != nil || restored.Pending == nil || *restored.Pending != c {
		t.Fatal("pending selection did not round-trip", err)
	}
	githubState, err := gh.ConsumeOAuthState(ctx, "same-state")
	if err != nil || githubState.Verifier != "github-verifier" {
		t.Fatal("provider OAuth states collided", err)
	}
	i := coding.Identity{UserID: 1, Team: "T", Channel: "C", Thread: "GL", Event: "same-provider-event"}
	e := coding.Edit{Task: coding.Task{ID: strings.Repeat("f", 32), Identity: i, Provider: "gitlab", Repository: coding.Repository{ID: 10, Provider: "gitlab", FullName: "team/subgroup/app", DefaultBranch: "main"}, Status: "editing"}, AccountID: 42}
	if _, err := gl.CreateEdit(ctx, e); err != nil {
		t.Fatal(err)
	}
	e.Task.Provider = "github"
	e.Task.Repository = coding.Repository{ID: 10, InstallationID: 2, FullName: "owner/repo", DefaultBranch: "main"}
	if _, err := gh.CreateEdit(ctx, e); err != nil {
		t.Fatal(err)
	}
	glRestart, err := NewGitLabStore(ctx, db, cipher)
	if err != nil {
		t.Fatal(err)
	}
	got, err := glRestart.GetEdit(ctx, 1, e.Task.ID)
	if err != nil || got.Task.Provider != "gitlab" {
		t.Fatal("provider edit isolation/restart failed", err)
	}
	if err := gl.Disconnect(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := gh.WithConnection(ctx, 1, func(c *coding.Connection) error {
		if c.Credentials.Access != "github-separate-secret" {
			t.Fatal("GitHub token altered")
		}
		return nil
	}); err != nil {
		t.Fatal("GitLab disconnect affected GitHub", err)
	}
	if got, err := gh.GetEdit(ctx, 1, e.Task.ID); err != nil || got.Task.Status != "editing" {
		t.Fatal("GitLab disconnect cancelled GitHub task", err)
	}
	if got, err := gl.GetEdit(ctx, 1, e.Task.ID); err != nil || got.Task.Status != "cancelled" {
		t.Fatal("GitLab disconnect retained task", err)
	}
}
