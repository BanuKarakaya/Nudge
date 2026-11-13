package handler

import (
	"context"
	"html/template"
	"net/http"
)

type preferencesStore interface {
	Preferences(context.Context, int64) (string, bool, error)
	SavePreferences(context.Context, int64, string, bool) error
}

var preferencesTemplate = template.Must(template.New("preferences").Parse(`<!doctype html><html lang="tr"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Nudgy · Hatırlatıcılar</title><main style="max-width:700px;margin:40px auto;padding:24px;font-family:system-ui"><a href="/dashboard">← Panel</a><h1>Hatırlatıcılar ve saat dilimi</h1><p>Günlük bağlantılar 22:00’de, Feedbin başlıkları 23:00’te gönderilir. Yeniden başlatmadan sonra son tamamlanan günlük dönem telafi edilir.</p><form method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Saat dilimi <input name="timezone" value="{{.Zone}}" placeholder="Europe/Istanbul" required></label><p><label><input type="checkbox" name="enabled" value="true" {{if .Enabled}}checked{{end}}>Bu çalışma alanı için hatırlatıcıları aç</label></p><button>Kaydet</button></form></main></html>`))

func (h *WorkspaceHandler) preferences(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.session(w, r)
	if !ok {
		return
	}
	store, ok := h.store.(preferencesStore)
	if !ok || !m.Manager {
		writeError(w, 403, "manager required")
		return
	}
	if r.Method == http.MethodPost {
		if store.SavePreferences(r.Context(), m.UserID, r.PostForm.Get("timezone"), r.PostForm.Get("enabled") == "true") != nil {
			writeError(w, 400, "geçerli bir IANA saat dilimi gerekli")
			return
		}
		http.Redirect(w, r, "/dashboard/preferences", http.StatusSeeOther)
		return
	}
	zone, enabled, err := store.Preferences(r.Context(), m.UserID)
	if err != nil {
		writeError(w, 503, "preferences unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = preferencesTemplate.Execute(w, struct {
		CSRF, Zone string
		Enabled    bool
	}{csrf, zone, enabled})
}
