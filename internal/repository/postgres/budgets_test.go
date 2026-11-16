package postgres

import (
	"context"
	"errors"
	"nudge/internal/analytics"
	"nudge/internal/domain"
	"testing"
)

func TestDailyBudgetUsesRemainingUserAndWorkspaceAllowance(t *testing.T) {
	for _, scope := range []string{"user", "workspace"} {
		t.Run(scope, func(t *testing.T) {
			db := reviewDB(t, 2)
			ctx := context.Background()
			installs := NewInstallationRepository(db)
			user, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "T", SlackUserID: "U", BotToken: "synthetic"})
			if err != nil {
				t.Fatal(err)
			}
			ai, err := NewWorkspaceAIStore(ctx, db, reviewCipher(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("NUDGY_RUN_TOKEN_LIMIT", "100000")
			t.Setenv("NUDGY_USER_DAILY_TOKENS", "2000000")
			t.Setenv("NUDGY_WORKSPACE_DAILY_TOKENS", "2000000")
			if scope == "user" {
				t.Setenv("NUDGY_USER_DAILY_TOKENS", "500000")
			} else {
				t.Setenv("NUDGY_WORKSPACE_DAILY_TOKENS", "500000")
			}
			id, _, err := ai.BeginBudget(ctx, user)
			if err != nil {
				t.Fatal(err)
			}
			if err = ai.FinishBudget(ctx, id, 402498); err != nil {
				t.Fatal(err)
			}
			next, limit, err := ai.BeginBudget(ctx, user)
			if err != nil || limit != 97502 {
				t.Fatalf("remaining allowance rejected: %d %v", limit, err)
			}
			_, _, err = ai.BeginBudget(ctx, user)
			var coded *analytics.OperationalError
			if !errors.As(err, &coded) || coded.Code != "daily_quota" {
				t.Fatalf("reserved allowance reused: %v", err)
			}
			if err = ai.FinishBudget(ctx, next, 10000); err != nil {
				t.Fatal(err)
			}
			_, limit, err = ai.BeginBudget(ctx, user)
			if err != nil || limit != 87502 {
				t.Fatalf("unused reservation not released: %d %v", limit, err)
			}
		})
	}
}
