package service

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRegistryCannotShadowToolsAndDefaultsToApproval(t *testing.T) {
	r := NewToolRegistry(&policyTestTools{})
	calls := 0
	tool := RegisteredTool{Definition: ToolDefinition{Name: "ext_issue_close", InputSchema: map[string]any{"type": "object"}}, Run: func(context.Context, json.RawMessage) (string, error) { calls++; return "done", nil }}
	if err := r.Register(tool); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(tool); err == nil {
		t.Fatal("duplicate accepted")
	}
	tool.Definition.Name = "trello_card_create"
	if err := r.Register(tool); err == nil {
		t.Fatal("built-in shadow accepted")
	}
	if !ToolMutates(r, "ext_issue_close") {
		t.Fatal("write default missing")
	}
	if _, err := r.Call(context.Background(), "ext_unknown", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown extension accepted")
	}
	if calls != 0 {
		t.Fatal("unexpected external execution")
	}
}
