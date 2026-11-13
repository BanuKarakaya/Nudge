package handler

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
)

type nativeDataCleaner interface {
	DeleteUserState(context.Context, int64) error
}

func (h *WorkspaceHandler) WithNativeData(cleaner nativeDataCleaner) { h.nativeData = cleaner }

type personalDataStore interface {
	ExportPersonalData(context.Context, int64) (json.RawMessage, error)
	DeletePersonalData(context.Context, int64) error
	AuditLog(context.Context, int64) (json.RawMessage, error)
}

var privacyTemplate = template.Must(template.New("privacy").Parse(`<!doctype html><html lang="tr"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Nudgy · Verilerim</title><main style="max-width:720px;margin:40px auto;font-family:system-ui;padding:24px"><a href="/dashboard">← Panel</a><h1>Verilerim</h1><p><a href="/dashboard/data/export">Kişisel verilerimi JSON olarak indir</a></p><h2>Geçmişimi ve bağlantılarımı sil</h2><p>Konuşmaların, görevlerin, kişisel sağlayıcı bağlantıların ve kayıtlı bağlantıların silinir; oturumun kapatılır. Slack üyelik kaydın ve çalışma alanına ait ortak yönetici ayarları korunur. Slack’te ve sağlayıcılarda önceden oluşturulan içerik, yedekler ve ücret/kota kayıtları bu silmeye dahil değildir. Bu işlem geri alınamaz.</p><form method="post" action="/dashboard/data/delete"><input type="hidden" name="csrf" value="{{.}}"><label>Onaylamak için SİL yaz: <input name="confirm" required pattern="SİL"></label><button>Kişisel verilerimi sil</button></form></main></html>`))

func (h *WorkspaceHandler) personalData(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.session(w, r)
	if !ok {
		return
	}
	store, ok := h.store.(personalDataStore)
	if !ok {
		writeError(w, 503, "data service unavailable")
		return
	}
	switch r.URL.Path {
	case "/dashboard/data/delete":
		if r.PostForm.Get("confirm") != "SİL" {
			writeError(w, 400, "silme onayı gerekli")
			return
		}
		if h.nativeData != nil {
			if err := h.nativeData.DeleteUserState(r.Context(), m.UserID); err != nil {
				writeError(w, 503, "AI geçmişi silinemedi; tekrar dene")
				return
			}
		}
		if store.DeletePersonalData(r.Context(), m.UserID) != nil {
			writeError(w, 503, "silme tamamlanamadı")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: workspaceCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
	case "/dashboard/data/export", "/dashboard/audit":
		var raw json.RawMessage
		var err error
		if r.URL.Path == "/dashboard/audit" {
			if !m.Manager {
				writeError(w, 403, "manager required")
				return
			}
			raw, err = store.AuditLog(r.Context(), m.UserID)
		} else {
			raw, err = store.ExportPersonalData(r.Context(), m.UserID)
			w.Header().Set("Content-Disposition", `attachment; filename="nudgy-personal-data.json"`)
		}
		if err != nil {
			writeError(w, 503, "export unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = privacyTemplate.Execute(w, csrf)
	}
}
