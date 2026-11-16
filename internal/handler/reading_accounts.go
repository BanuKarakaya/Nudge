package handler

import "context"

// Personal reading services have no shared repository/project scope. Only
// configured services are registered; their callbacks stay bound to the session.
func (h *WorkspaceHandler) RegisterReadingAccount(kind, name string, connected func(context.Context, int64) (bool, error), disconnect func(context.Context, int64) error) {
	if (kind != "feedbin" && kind != "raindrop") || connected == nil || disconnect == nil {
		return
	}
	h.providers = append(h.providers, workspaceProvider{Kind: kind, Name: name, Personal: true, Connected: connected, Disconnect: disconnect})
}
