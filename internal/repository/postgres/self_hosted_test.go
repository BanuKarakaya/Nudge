package postgres

import (
	"context"
	"errors"
	"testing"

	"nudge/internal/domain"
	"nudge/internal/repository"
)

func TestSelfHostedRepositoryRestrictsExistingAndNewWorkspaces(t *testing.T) {
	db := reviewDB(t, 2)
	ctx := context.Background()
	unscoped := NewInstallationRepository(db, reviewCipher(t))
	ids := map[string]int64{}
	for _, team := range []string{"T", "OTHER"} {
		id, err := unscoped.Save(ctx, domain.SlackInstallation{TeamID: team, SlackUserID: "U", BotToken: "synthetic"})
		if err != nil {
			t.Fatal(err)
		}
		ids[team] = id
	}
	r := NewInstallationRepository(db, reviewCipher(t)).WithWorkspace("T")
	if _, err := r.GetByUserID(ctx, ids["T"]); err != nil {
		t.Fatal("owner lost bot access", err)
	}
	if got, err := r.GetByUserID(ctx, ids["OTHER"]); !errors.Is(err, repository.ErrWorkspaceNotAllowed) || got.BotToken != "" {
		t.Fatal("foreign token exposed", err)
	}
	if _, err := r.Save(ctx, domain.SlackInstallation{TeamID: "NEW", SlackUserID: "U", BotToken: "x"}); !errors.Is(err, repository.ErrWorkspaceNotAllowed) {
		t.Fatal("foreign install saved", err)
	}
	if _, err := r.SignInUser(ctx, "OTHER", "NEW-MEMBER"); !errors.Is(err, repository.ErrWorkspaceNotAllowed) {
		t.Fatal("foreign member saved", err)
	}
	if _, err := r.ListUsersByTeam(ctx, "OTHER"); !errors.Is(err, repository.ErrWorkspaceNotAllowed) {
		t.Fatal("foreign members listed", err)
	}
	if _, err := r.GetUserIDBySlackUserID(ctx, "OTHER", "U"); !errors.Is(err, repository.ErrWorkspaceNotAllowed) {
		t.Fatal("foreign lookup accepted", err)
	}
	seeds, err := r.ListWorkspaceSeeds(ctx)
	if err != nil || len(seeds) != 1 || seeds[0] != ids["T"] {
		t.Fatal("onboarding included foreign workspace", seeds, err)
	}
	var installs, members int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM slack_installations").Scan(&installs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&members); err != nil {
		t.Fatal(err)
	}
	if installs != 2 || members != 2 {
		t.Fatal("existing data deleted or new foreign data written", installs, members)
	}
	r.WithWorkspaces(" T, OTHER, ", "T")
	allowed, restricted := r.AllowedWorkspaces()
	if !restricted || len(allowed) != 2 || repository.WorkspaceAllowed(r, "THIRD") || repository.WorkspaceAllowed(r, "T-prefix") {
		t.Fatal("allowlist normalization or exact match failed", allowed)
	}
	for team, id := range ids {
		if got, err := r.GetByUserID(ctx, id); err != nil || got.TeamID != team || got.BotToken != "synthetic" {
			t.Fatal("approved workspace lost its independent installation", team, err)
		}
	}
	seeds, err = r.ListWorkspaceSeeds(ctx)
	if err != nil || len(seeds) != 2 {
		t.Fatal("second approved workspace excluded", seeds, err)
	}
	if _, err := r.Save(ctx, domain.SlackInstallation{TeamID: "THIRD", SlackUserID: "U", BotToken: "x"}); !errors.Is(err, repository.ErrWorkspaceNotAllowed) {
		t.Fatal("multiple approvals opened public signup", err)
	}
	r.WithWorkspaces(" , ")
	if got, err := r.GetByUserID(ctx, ids["T"]); !errors.Is(err, repository.ErrWorkspaceNotAllowed) || got.BotToken != "" {
		t.Fatal("unconfigured instance returned token")
	}
	seeds, err = r.ListWorkspaceSeeds(ctx)
	if err != nil || len(seeds) != 0 {
		t.Fatal("unconfigured instance scheduled onboarding", err)
	}
}
