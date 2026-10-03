package handler

import (
	"context"
	"net/http"
	"time"

	"nudge/internal/service"
)

type RaindropSyncHandler struct {
	service *service.RaindropSyncService
}

func NewRaindropSyncHandler(syncService *service.RaindropSyncService) *RaindropSyncHandler {
	return &RaindropSyncHandler{service: syncService}
}

func (h *RaindropSyncHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/raindrop/sync", h.sync)
}

func (h *RaindropSyncHandler) sync(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromHeader(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	synced, err := h.service.SyncLatest(ctx, userID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not sync Raindrop bookmarks")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"synced": synced})
}
