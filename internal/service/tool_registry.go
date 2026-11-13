package service

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"

	"nudge/internal/coding"
)

// ToolModule is constructed per request with server-authenticated identity.
// Modules must check live provider access; a registry is not a credential broker.
type ToolModule func(coding.Identity) []RegisteredTool
type RegisteredTool struct {
	Definition ToolDefinition
	ReadOnly   bool // Defaults to a write requiring the host's approval policy.
	Run        func(context.Context, json.RawMessage) (string, error)
}
type ToolRegistry struct {
	base  AgentTools
	tools map[string]RegisteredTool
}

var extensionName = regexp.MustCompile(`^ext_[a-z][a-z0-9_]{0,90}$`)

func NewToolRegistry(base AgentTools) *ToolRegistry {
	return &ToolRegistry{base: base, tools: map[string]RegisteredTool{}}
}

// Register is startup/request-assembly only, before any model call. Extensions
// cannot shadow built-ins or another extension. No remote discovery or execution.
func (r *ToolRegistry) Register(tool RegisteredTool) error {
	if !extensionName.MatchString(tool.Definition.Name) || tool.Run == nil || tool.Definition.InputSchema == nil {
		return errors.New("invalid tool registration")
	}
	for _, d := range r.Definitions() {
		if d.Name == tool.Definition.Name {
			return errors.New("duplicate tool registration")
		}
	}
	raw, err := json.Marshal(tool.Definition)
	if err != nil {
		return err
	}
	var copied ToolDefinition
	if err = json.Unmarshal(raw, &copied); err != nil {
		return err
	}
	tool.Definition = copied
	r.tools[tool.Definition.Name] = tool
	return nil
}
func (r *ToolRegistry) Definitions() []ToolDefinition {
	defs := append([]ToolDefinition(nil), r.base.Definitions()...)
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		defs = append(defs, r.tools[name].Definition)
	}
	return defs
}
func (r *ToolRegistry) Mutates(name string) bool {
	if t, ok := r.tools[name]; ok {
		return !t.ReadOnly
	}
	return MutatingTool(name)
}
func (r *ToolRegistry) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if t, ok := r.tools[name]; ok {
		if !json.Valid(args) {
			return "", errAgentArguments
		}
		return t.Run(ctx, args)
	}
	if extensionName.MatchString(name) {
		return "", errAgentArguments
	}
	return r.base.Call(ctx, name, args)
}
func ToolMutates(tools AgentTools, name string) bool {
	if catalog, ok := tools.(interface{ Mutates(string) bool }); ok {
		return catalog.Mutates(name)
	}
	return MutatingTool(name)
}
