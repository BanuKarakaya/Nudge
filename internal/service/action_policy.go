package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"nudge/internal/coding"
)

var ErrApprovalUnavailable = errors.New("onay sona ermiş veya daha önce kullanılmış")

var ErrActionDisabled = errors.New("bu araç workspace yöneticisi tarafından kapatılmış")

type ActionProposal struct {
	ID        string
	Identity  coding.Identity
	Tool      string
	Arguments json.RawMessage
	Revision  string
	Status    string
}
type ActionStore interface {
	ActionMode(context.Context, int64, string) (string, error)
	ProposeAction(context.Context, ActionProposal) (ActionProposal, error)
}

// MutatingTool is the host-owned catalog of external side effects. Unknown
// tracker operations fail closed; adding a read tool requires explicit review.
func MutatingTool(name string) bool {
	if strings.HasPrefix(name, "ext_") {
		return true
	}
	n := strings.TrimPrefix(name, "gitlab_")
	switch n {
	case "repository_edit_finish", "repository_push", "repository_change_request_create", "repository_change_request_merge", "repository_branch_change_request_create", "repository_branches_merge":
		return true
	}
	if strings.HasPrefix(name, "jira_") || strings.HasPrefix(name, "trello_") {
		for _, suffix := range []string{"_connect", "_connection_status", "_list", "_read", "_options", "_transitions", "_edit_fields", "_checklists", "_comments"} {
			if strings.HasSuffix(name, suffix) {
				return false
			}
		}
		return true
	}
	return false
}

type policyTools struct {
	AgentTools
	store    ActionStore
	identity coding.Identity
	notify   func(context.Context, ActionProposal) error
	prepare  func(context.Context, ActionProposal) (ActionProposal, error)
}

func WithActionPolicy(base AgentTools, store ActionStore, identity coding.Identity, notify func(context.Context, ActionProposal) error, prepare ...func(context.Context, ActionProposal) (ActionProposal, error)) AgentTools {
	t := &policyTools{AgentTools: base, store: store, identity: identity, notify: notify}
	if len(prepare) > 0 {
		t.prepare = prepare[0]
	}
	return t
}
func (t *policyTools) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if !ToolMutates(t.AgentTools, name) {
		return t.AgentTools.Call(ctx, name, args)
	}
	// Reject unknown names before creating a user-visible authorization request.
	known := false
	for _, d := range t.Definitions() {
		if d.Name == name {
			known = true
			break
		}
	}
	if !known {
		return "", errAgentArguments
	}
	mode, err := t.store.ActionMode(ctx, t.identity.UserID, name)
	if err != nil {
		return "", err
	}
	if mode == "disabled" {
		return "", ErrActionDisabled
	}
	if mode == "auto" {
		return t.AgentTools.Call(ctx, name, args)
	}
	candidate := ActionProposal{Identity: t.identity, Tool: name, Arguments: args}
	if t.prepare != nil {
		candidate, err = t.prepare(ctx, candidate)
		if err != nil {
			return "", err
		}
	}
	proposal, err := t.store.ProposeAction(ctx, candidate)
	if err != nil {
		return "", err
	}
	if err = t.notify(ctx, proposal); err != nil {
		return "", err
	}
	return `{"status":"awaiting_user_approval","executed":false,"message":"Slack onay kartı gönderildi. İşlem henüz yapılmadı; başarı iddia etme veya tekrar çağırma."}`, nil
}
