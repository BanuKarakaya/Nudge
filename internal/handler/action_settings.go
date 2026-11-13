package handler

import (
	"html/template"
	"net/http"
	"sort"
	"time"

	"nudge/internal/coding"
	"nudge/internal/service"
	"nudge/internal/tracker"
)

var actionSettings = template.Must(template.New("actions").Parse(`<!doctype html><html lang="tr"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Nudgy · Araç izinleri</title><link rel="stylesheet" href="/dashboard/workspace.css"><main style="max-width:960px;margin:40px auto;padding:24px"><a href="/dashboard">← Panele dön</a><h1>Araç izinleri</h1><p>Varsayılan olarak dış sistemlerde değişiklik yapan her işlem Slack’te sahibinin onayını ister. Otomatik izin, aracı modelin doğrudan kullanmasına izin verir.</p><table><thead><tr><th>Araç</th><th>İzin</th></tr></thead><tbody>{{range .Rows}}<tr><td>{{.Name}}</td><td><form method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="tool" value="{{.Name}}"><select name="mode"><option value="approval" {{if eq .Mode "approval"}}selected{{end}}>Onay iste</option><option value="auto" {{if eq .Mode "auto"}}selected{{end}}>Otomatik</option><option value="disabled" {{if eq .Mode "disabled"}}selected{{end}}>Kapalı</option></select><button>Kaydet</button></form></td></tr>{{end}}</tbody></table></main></html>`))

func (h *WorkspaceHandler) WithActions(store actionStore, events *SlackEventHandler) {
	h.actions = store
	h.actionEvents = events
}
func (h *WorkspaceHandler) availableTools() service.AgentTools {
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
	base, err = h.actionEvents.moduleTools(base, coding.Identity{})
	if err != nil {
		return nil
	}
	return base
}
func (h *WorkspaceHandler) actionTools() []string {
	base := h.availableTools()
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
	if !m.Manager || h.actions == nil {
		writeError(w, 403, "manager required")
		return
	}
	names := h.actionTools()
	if r.Method == http.MethodPost {
		tool := r.PostForm.Get("tool")
		found := false
		for _, n := range names {
			if n == tool {
				found = true
			}
		}
		if !found || h.actions.SetActionMode(r.Context(), m.UserID, tool, r.PostForm.Get("mode")) != nil {
			writeError(w, 400, "invalid tool policy")
			return
		}
		http.Redirect(w, r, "/dashboard/actions", http.StatusSeeOther)
		return
	}
	type row struct{ Name, Mode string }
	data := struct {
		CSRF string
		Rows []row
	}{CSRF: csrf}
	for _, name := range names {
		mode, err := h.actions.ActionMode(r.Context(), m.UserID, name)
		if err != nil {
			writeError(w, 503, "policies unavailable")
			return
		}
		data.Rows = append(data.Rows, row{name, mode})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = actionSettings.Execute(w, data)
}

func (h *WorkspaceHandler) routingTools() []string {
	base := h.availableTools()
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
