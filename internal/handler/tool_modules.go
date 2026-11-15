package handler

import (
	"context"
	"nudge/internal/coding"
	"nudge/internal/service"
)

// WithToolModule registers a trusted, compiled-in extension before serving HTTP.
// Its callbacks are recreated for each signed request and for approved actions.
func (h *SlackEventHandler) WithToolModule(module service.ToolModule) {
	h.toolModules = append(h.toolModules, module)
}
func (h *SlackEventHandler) moduleTools(ctx context.Context, base service.AgentTools, i coding.Identity) (service.AgentTools, error) {
	registry := service.NewToolRegistry(base)
	for _, module := range h.toolModules {
		for _, tool := range module(i) {
			if err := registry.Register(tool); err != nil {
				return nil, err
			}
		}
	}
	if h.connectors != nil {
		tools, err := h.connectors.Tools(ctx, i)
		if err != nil {
			return nil, err
		}
		for _, tool := range tools {
			if err := registry.Register(tool); err != nil {
				return nil, err
			}
		}
	}
	return registry, nil
}
