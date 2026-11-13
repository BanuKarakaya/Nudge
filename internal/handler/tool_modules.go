package handler

import (
	"nudge/internal/coding"
	"nudge/internal/service"
)

// WithToolModule registers a trusted, compiled-in extension before serving HTTP.
// Its callbacks are recreated for each signed request and for approved actions.
func (h *SlackEventHandler) WithToolModule(module service.ToolModule) {
	h.toolModules = append(h.toolModules, module)
}
func (h *SlackEventHandler) moduleTools(base service.AgentTools, i coding.Identity) (service.AgentTools, error) {
	registry := service.NewToolRegistry(base)
	for _, module := range h.toolModules {
		for _, tool := range module(i) {
			if err := registry.Register(tool); err != nil {
				return nil, err
			}
		}
	}
	return registry, nil
}
