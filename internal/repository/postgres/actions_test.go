package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"nudge/internal/coding"
	"nudge/internal/domain"
	"nudge/internal/service"
	"nudge/internal/workspace"
)

type actionExecutionProbe struct {
	name  string
	calls int
}

func (p *actionExecutionProbe) Definitions() []service.ToolDefinition {
	return []service.ToolDefinition{{Name: p.name}}
}
func (p *actionExecutionProbe) Call(context.Context, string, json.RawMessage) (string, error) {
	p.calls++
	return `{"title":"Nudgy","url":"https://trello.com/c/example","list_name":"Doing"}`, nil
}

func TestActionPolicyDefaultsAndWorkspaceOverrides(t *testing.T) {
	db := reviewDB(t, 2)
	ctx := context.Background()
	installs := NewInstallationRepository(db)
	owner, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "T", SlackUserID: "OWNER", BotToken: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := installs.SignInUser(ctx, "T", "MEMBER")
	if err != nil {
		t.Fatal(err)
	}
	other, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "OTHER", SlackUserID: "OWNER", BotToken: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewActionStore(db, reviewCipher(t))
	for _, name := range []string{"trello_card_create", "trello_card_assign", "jira_issue_create", "repository_push", "repository_change_request_merge", "gitlab_repository_change_request_merge", "ext_mcp_company_create"} {
		t.Run(name, func(t *testing.T) {
			base := &actionExecutionProbe{name: name}
			notified := 0
			tools := service.WithActionPolicy(base, store, coding.Identity{UserID: member, Team: "T", Event: name, Channel: "C", Thread: "1"}, func(context.Context, service.ActionProposal) error { notified++; return nil })
			result, err := tools.Call(ctx, name, json.RawMessage(`{}`))
			if err != nil || base.calls != 1 || notified != 0 || !strings.Contains(result, "https://trello.com/c/example") {
				t.Fatalf("default request did not execute exactly once: calls=%d notifications=%d result=%s err=%v", base.calls, notified, result, err)
			}
			if err := store.SetActionMode(ctx, member, name, "auto"); !errors.Is(err, workspace.ErrDenied) {
				t.Fatalf("member changed policy: %v", err)
			}
			if err := store.SetActionMode(ctx, owner, name, "approval"); err != nil {
				t.Fatal(err)
			}
			result, err = tools.Call(ctx, name, json.RawMessage(`{}`))
			if err != nil || base.calls != 1 || notified != 1 || !strings.Contains(result, "awaiting_user_approval") {
				t.Fatalf("explicit approval bypassed: %s %v", result, err)
			}
			if mode, err := store.ActionMode(ctx, other, name); err != nil || mode != "auto" {
				t.Fatalf("policy leaked across workspaces: %s %v", mode, err)
			}
			if err := store.SetActionMode(ctx, owner, name, "disabled"); err != nil {
				t.Fatal(err)
			}
			if _, err := tools.Call(ctx, name, json.RawMessage(`{}`)); !errors.Is(err, service.ErrActionDisabled) || base.calls != 1 || notified != 1 {
				t.Fatalf("disabled action executed: %v", err)
			}
			if err := store.SetActionMode(ctx, owner, name, "auto"); err != nil {
				t.Fatal(err)
			}
			if _, err := tools.Call(ctx, name, json.RawMessage(`{}`)); err != nil || base.calls != 2 || notified != 1 {
				t.Fatalf("explicit auto failed: %v", err)
			}
		})
	}
	if _, err := db.Exec(ctx, `UPDATE slack_installations SET bot_token='' WHERE team_id='T'`); err != nil {
		t.Fatal(err)
	}
	base := &actionExecutionProbe{name: "trello_card_create"}
	tools := service.WithActionPolicy(base, store, coding.Identity{UserID: member}, func(context.Context, service.ActionProposal) error {
		t.Fatal("revoked installation proposed action")
		return nil
	})
	if _, err := tools.Call(ctx, base.name, json.RawMessage(`{}`)); err == nil || base.calls != 0 {
		t.Fatal("revoked installation executed auto action")
	}
}
