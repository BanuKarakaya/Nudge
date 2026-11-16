package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

var ErrWorkspaceLinkDelivery = errors.New("private workspace link could not be delivered")

type workspaceTools struct {
	AgentTools
	send func(context.Context) error
	mu   sync.Mutex
	sent bool
}

// send is bound by the host to the authenticated Slack requester. The model
// cannot choose a recipient, workspace, URL or session, and never receives a link.
func WithWorkspaceTools(base AgentTools, send func(context.Context) error) AgentTools {
	if send == nil {
		return base
	}
	return &workspaceTools{AgentTools: base, send: send}
}
func (t *workspaceTools) Definitions() []ToolDefinition {
	return append(append([]ToolDefinition(nil), t.AgentTools.Definitions()...), ToolDefinition{
		Name:        "workspace_connect",
		Description: "Send the current Slack user their own Nudgy dashboard/panel sign-in link by private DM. Use for natural requests such as 'dashboard linki verebilir misin', 'panelimi aç', 'send me my Nudgy dashboard link', or access to Nudgy logs, usage and settings. Also use for Feedbin/Raindrop connection or renewal requests; the secure Hesaplarım page supports these personal accounts. Read workspace_guide for supported setup paths and explain the relevant dashboard section after successful delivery. Sending a link does not apply any requested setting change. This is the general Nudgy dashboard, independent of GitHub/GitLab/Jira/Trello. Both members and managers may request their own link; an administrator is not needed. Never use a provider-specific connection tool as a substitute. No URL or credential is exposed to the model. Only claim sent after success; for a link-only request stop after sending. Do not send for negated, quoted or unrelated third-party dashboard requests.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
	})
}
func (t *workspaceTools) Mutates(name string) bool {
	if name == "workspace_connect" {
		return false
	}
	return ToolMutates(t.AgentTools, name)
}
func (t *workspaceTools) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if name != "workspace_connect" {
		return t.AgentTools.Call(ctx, name, args)
	}
	var values map[string]json.RawMessage
	if len(args) > 128 || json.Unmarshal(args, &values) != nil || values == nil || len(values) != 0 {
		return "", errAgentArguments
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !t.sent {
		sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := t.send(sendCtx); err != nil {
			return "", ErrWorkspaceLinkDelivery
		}
		t.sent = true
	}
	return `{"connection_link_sent_privately":true,"instruction":"Nudgy dashboard bağlantısı kullanıcının Nudgy DM sohbetine gönderildi. Bağlantı istendiyse burada dur; kullanıcıya DM mesajını açmasını söyle. Giriş yapıldığını veya herhangi bir sağlayıcının bağlandığını iddia etme."}`, nil
}
