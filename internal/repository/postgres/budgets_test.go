package postgres

import (
	"context"
	"nudge/internal/domain"
	"testing"
)

func TestRunAdmissionIgnoresHistoricalTokenBudgets(t *testing.T) {
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
	t.Setenv("NUDGY_RUN_TOKEN_LIMIT", "1")
	t.Setenv("NUDGY_USER_DAILY_TOKENS", "1")
	t.Setenv("NUDGY_WORKSPACE_DAILY_TOKENS", "1")
	t.Setenv("NUDGY_WORKSPACE_CONCURRENCY", "1")
	// Completed old budgets and expired leases are not daily spending gates.
	if _, err = db.Exec(ctx, `INSERT INTO nudge_run_budgets(id,user_id,team_id,tokens,running,lease_until) VALUES('historical',$1,'T',9000000,FALSE,NOW()),('expired',$1,'T',9000000,TRUE,NOW()-INTERVAL '1 hour')`, user); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		id, err := ai.BeginRun(ctx, user)
		if err != nil {
			t.Fatalf("legacy budget blocked request %d: %v", i, err)
		}
		if err = ai.FinishRun(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	var tokens int64
	if err = db.QueryRow(ctx, `SELECT sum(tokens) FROM nudge_run_budgets WHERE team_id='T' AND id NOT IN ('historical','expired')`).Scan(&tokens); err != nil || tokens != 0 {
		t.Fatalf("new token reservations: %d %v", tokens, err)
	}
	if _, err = db.Exec(ctx, `UPDATE slack_installations SET bot_token='' WHERE team_id='T'`); err != nil {
		t.Fatal(err)
	}
	if _, err = ai.BeginRun(ctx, user); err == nil {
		t.Fatal("revoked workspace admitted")
	}
}
