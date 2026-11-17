package handler

import (
	"net/http"
	"nudge/internal/slack"
)

func (h *WorkspaceHandler) slackManifest(w http.ResponseWriter, r *http.Request) {
	m, _, ok := h.session(w, r)
	if !ok {
		return
	}
	if !m.Manager {
		writeError(w, 403, "Yönetici erişimi gerekli.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="nudgy-slack-manifest.json"`)
	_, _ = w.Write([]byte(slack.Manifest(h.base)))
}
