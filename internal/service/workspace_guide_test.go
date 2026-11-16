package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceGuideCannotSelectIdentityOrSendLinks(t *testing.T) {
	reads, sends := 0, 0
	base := WithWorkspaceTools(NewAgentTools(nil, nil, nil, 1, time.Now(), time.UTC), func(context.Context) error { sends++; return nil })
	tools := WithWorkspaceGuide(base, func(context.Context) (WorkspaceGuide, error) { reads++; return WorkspaceGuide{Manager: false}, nil })
	for _, raw := range []string{`{"user_id":2}`, `{"team":"other"}`, `{"send":true}`, `null`, `[]`, `{} {}`} {
		if _, err := tools.Call(context.Background(), "workspace_guide", json.RawMessage(raw)); err == nil {
			t.Fatal("invalid guide argument", raw)
		}
	}
	out, err := tools.Call(context.Background(), "workspace_guide", json.RawMessage(`{}`))
	if err != nil || reads != 1 || sends != 0 || !strings.Contains(out, `"is_workspace_manager":false`) {
		t.Fatal("guide has unexpected effects", out, err, reads, sends)
	}
	if _, err = tools.Call(context.Background(), "workspace_connect", json.RawMessage(`{}`)); err != nil || sends != 1 {
		t.Fatal("guide broke delegated delivery", err)
	}
	failed := WithWorkspaceGuide(base, func(context.Context) (WorkspaceGuide, error) {
		return WorkspaceGuide{}, errors.New("PRIVATE_DATABASE_SECRET")
	})
	_, err = failed.Call(context.Background(), "workspace_guide", json.RawMessage(`{}`))
	code, msg := AgentToolFailure(err)
	if code != "workspace_guide_failed" || strings.Contains(msg+err.Error(), "PRIVATE") {
		t.Fatal("unsafe guide error", code)
	}
}

type catalogTestTools struct{ defs []ToolDefinition }

func (t catalogTestTools) Definitions() []ToolDefinition { return t.defs }
func (catalogTestTools) Call(context.Context, string, json.RawMessage) (string, error) {
	panic("fingerprinting must not execute tools")
}

func TestToolCatalogFingerprintIgnoresOrderButDetectsSchemaChanges(t *testing.T) {
	a := ToolDefinition{Name: "a", InputSchema: map[string]any{"type": "object"}}
	b := ToolDefinition{Name: "b", Description: "v1", InputSchema: map[string]any{"type": "object"}}
	one, _ := ToolCatalogFingerprint(catalogTestTools{[]ToolDefinition{a, b}})
	two, _ := ToolCatalogFingerprint(catalogTestTools{[]ToolDefinition{b, a}})
	if one != two || one == "" {
		t.Fatal("order made catalog unstable")
	}
	b.InputSchema = map[string]any{"type": "object", "required": []string{"new_argument"}}
	three, _ := ToolCatalogFingerprint(catalogTestTools{[]ToolDefinition{a, b}})
	if three == one {
		t.Fatal("changed schema reused native tools")
	}
}
