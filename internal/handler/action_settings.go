package handler

import (
	"context"
	"net/http"
	"sort"
	"time"

	"nudge/internal/coding"
	"nudge/internal/service"
	"nudge/internal/tracker"
	"nudge/internal/workspace"
)

func (h *WorkspaceHandler) WithActions(store actionStore, events *SlackEventHandler) {
	h.actions = store
	h.actionEvents = events
}
func (h *WorkspaceHandler) availableTools(ctx context.Context, m workspace.Member) service.AgentTools {
	if h.actionEvents == nil {
		return nil
	}
	base := service.NewAgentTools(nil, nil, nil, 0, time.Now(), time.UTC)
	if h.actionEvents.coding != nil {
		base = h.actionEvents.coding.Tools(base, coding.Identity{})
	}
	if h.actionEvents.gitlab != nil {
		base = h.actionEvents.gitlab.Tools(base, coding.Identity{})
	}
	for _, integration := range h.actionEvents.trackers {
		base = integration.Tools(base, tracker.Identity{})
	}
	var err error
	base, err = h.actionEvents.moduleTools(ctx, base, coding.Identity{UserID: m.UserID, Team: m.Team})
	if err != nil {
		return nil
	}
	return base
}
func (h *WorkspaceHandler) actionTools(ctx context.Context, m workspace.Member) []string {
	base := h.availableTools(ctx, m)
	if base == nil {
		return nil
	}
	var names []string
	for _, d := range base.Definitions() {
		if service.ToolMutates(base, d.Name) {
			names = append(names, d.Name)
		}
	}
	sort.Strings(names)
	return names
}
func (h *WorkspaceHandler) actionPage(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.session(w, r)
	if !ok {
		return
	}
	data := workspacePage{Member: m, CSRF: csrf, Section: "actions", Title: "Araç izinleri", Description: "Nudgy’nin bağlı uygulamalarda işlem yaparken ne zaman onay isteyeceğini belirle."}
	if !m.Manager || h.actions == nil {
		data.Error = "Araç izinleri için yönetici erişimi gerekiyor."
		h.page(w, http.StatusForbidden, data)
		return
	}
	names := h.actionTools(r.Context(), m)
	if r.Method == http.MethodPost {
		tool := r.PostForm.Get("tool")
		found := false
		for _, n := range names {
			if n == tool {
				found = true
			}
		}
		if !found || h.actions.SetActionMode(r.Context(), m.UserID, tool, r.PostForm.Get("mode")) != nil {
			data.Error = "Araç izni kaydedilemedi. Geçerli bir araç ve izin seçerek tekrar dene."
			h.page(w, http.StatusBadRequest, data)
			return
		}
		http.Redirect(w, r, "/dashboard/actions?saved=1", http.StatusSeeOther)
		return
	}
	descriptions := map[string]string{}
	if tools := h.availableTools(r.Context(), m); tools != nil {
		for _, d := range tools.Definitions() {
			descriptions[d.Name] = d.Description
		}
	}
	for _, name := range names {
		mode, err := h.actions.ActionMode(r.Context(), m.UserID, name)
		if err != nil {
			data.Error = "Araç izinleri şu an okunamıyor."
			h.page(w, http.StatusServiceUnavailable, data)
			return
		}
		data.ActionRows = append(data.ActionRows, workspaceActionRow{name, mode, descriptions[name]})
	}
	if r.URL.Query().Get("saved") == "1" {
		data.Message = "Araç izni kaydedildi."
	}
	h.page(w, http.StatusOK, data)
}

func (h *WorkspaceHandler) routingTools(ctx context.Context, m workspace.Member) []string {
	base := h.availableTools(ctx, m)
	if base == nil {
		return nil
	}
	names := []string{}
	for _, d := range base.Definitions() {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	return names
}
