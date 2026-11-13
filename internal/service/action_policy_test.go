package service

import (
	"context"
	"encoding/json"
	"errors"
	"nudge/internal/coding"
	"testing"
)

type policyTestStore struct {
	mode  string
	count int
}

func (s *policyTestStore) ActionMode(context.Context, int64, string) (string, error) {
	return s.mode, nil
}
func (s *policyTestStore) ProposeAction(_ context.Context, p ActionProposal) (ActionProposal, error) {
	s.count++
	return p, nil
}

type policyTestTools struct{ calls int }

func (*policyTestTools) Definitions() []ToolDefinition {
	return []ToolDefinition{{Name: "trello_card_create"}, {Name: "trello_cards_list"}}
}
func (t *policyTestTools) Call(context.Context, string, json.RawMessage) (string, error) {
	t.calls++
	return "ok", nil
}
func TestActionPolicyCannotExecuteUnapprovedWrites(t *testing.T) {
	for _, mode := range []string{"approval", "disabled", "auto"} {
		t.Run(mode, func(t *testing.T) {
			base := &policyTestTools{}
			store := &policyTestStore{mode: mode}
			notified := 0
			tools := WithActionPolicy(base, store, coding.Identity{UserID: 1}, func(context.Context, ActionProposal) error { notified++; return nil })
			_, err := tools.Call(context.Background(), "trello_card_create", json.RawMessage(`{}`))
			if mode == "disabled" && !errors.Is(err, ErrActionDisabled) {
				t.Fatal(err)
			}
			want := 0
			if mode == "auto" {
				want = 1
			}
			if base.calls != want {
				t.Fatal("policy bypass")
			}
			if mode == "approval" && (store.count != 1 || notified != 1) {
				t.Fatal("approval not delivered")
			}
		})
	}
}
func TestMutatingCatalogCoversManagementWrites(t *testing.T) {
	for _, kind := range []string{"jira", "trello"} {
		for _, d := range trackerManagementDefinitions(kind) {
			if d.Name == kind+"_members_list" || d.Name == "trello_labels_list" || d.Name == "trello_card_checklists" || d.Name == "jira_issue_edit_fields" || d.Name == kind+"_card_comments" || d.Name == kind+"_issue_comments" {
				continue
			}
			if !MutatingTool(d.Name) {
				t.Fatalf("write omitted from policy: %s", d.Name)
			}
		}
	}
}
