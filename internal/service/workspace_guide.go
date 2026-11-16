package service

import (
	"context"
	"encoding/json"
	"errors"
)

var ErrWorkspaceGuide = errors.New("workspace guide unavailable")

// WorkspaceGuide contains host-owned product facts, never credentials, account
// contents or untrusted remote descriptions. Identity is bound by the caller.
type WorkspaceGuide struct {
	Manager     bool               `json:"is_workspace_manager"`
	Features    []WorkspaceFeature `json:"features"`
	Limitations []string           `json:"limitations"`
}

type WorkspaceFeature struct {
	Topic        string `json:"topic"`
	Section      string `json:"dashboard_section"`
	Available    bool   `json:"available_in_this_installation"`
	ManagerOnly  bool   `json:"manager_only"`
	Instructions string `json:"instructions"`
}

type workspaceGuideTools struct {
	AgentTools
	read func(context.Context) (WorkspaceGuide, error)
}

func WithWorkspaceGuide(base AgentTools, read func(context.Context) (WorkspaceGuide, error)) AgentTools {
	if read == nil {
		return base
	}
	return &workspaceGuideTools{AgentTools: base, read: read}
}

func (t *workspaceGuideTools) Definitions() []ToolDefinition {
	return append(append([]ToolDefinition(nil), t.AgentTools.Definitions()...), ToolDefinition{
		Name:        "workspace_guide",
		Description: "Read Nudgy's actual feature guide and current user's manager/member role. Use for 'neler yapabiliyorsun', connecting/reconnecting Feedbin or Raindrop, reminders/timezone, model/API settings, logs/usage, data export/deletion, disconnecting accounts, permissions, and adding company/internal apps via MCP. Explains supported actions, dashboard sections, and limitations; does NOT change settings, send a DM, connect an account, create a reminder, read live metrics or delete data. For an explicit request to access a panel or connect an account, also call workspace_connect; for a how-to or capability question just explain. Supplied operational tools, including ext_mcp_*, remain authoritative for executable actions.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}, "additionalProperties": false},
	})
}

func (t *workspaceGuideTools) Mutates(name string) bool {
	if name == "workspace_guide" {
		return false
	}
	return ToolMutates(t.AgentTools, name)
}

func (t *workspaceGuideTools) Call(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	if name != "workspace_guide" {
		return t.AgentTools.Call(ctx, name, raw)
	}
	var args struct{}
	defs := t.Definitions()
	if err := decodeAgentArguments(raw, defs[len(defs)-1].InputSchema, &args); err != nil {
		return "", err
	}
	guide, err := t.read(ctx)
	if err != nil {
		return "", ErrWorkspaceGuide
	}
	result, err := json.Marshal(guide)
	return string(result), err
}
