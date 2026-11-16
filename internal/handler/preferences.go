package handler

import (
	"context"
	"net/http"
)

type preferencesStore interface {
	Preferences(context.Context, int64) (string, bool, error)
	SavePreferences(context.Context, int64, string, bool) error
}

func (h *WorkspaceHandler) preferences(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.session(w, r)
	if !ok {
		return
	}
	data := workspacePage{Member: m, CSRF: csrf, Section: "preferences", Title: "Hatırlatıcılar", Description: "Günlük özetleri ve çalışma alanının saat dilimini yönet."}
	store, ok := h.store.(preferencesStore)
	if !ok || !m.Manager {
		data.Error = "Hatırlatıcı ayarlarını yalnızca çalışma alanı yöneticisi değiştirebilir."
		h.page(w, http.StatusForbidden, data)
		return
	}
	if r.Method == http.MethodPost {
		if store.SavePreferences(r.Context(), m.UserID, r.PostForm.Get("timezone"), r.PostForm.Get("enabled") == "true") != nil {
			data.Timezone = r.PostForm.Get("timezone")
			data.ReminderEnabled = r.PostForm.Get("enabled") == "true"
			data.FormError = "Ayarlar kaydedilemedi. Europe/Istanbul gibi geçerli bir saat dilimi girip yeniden dene."
			h.page(w, http.StatusBadRequest, data)
			return
		}
		http.Redirect(w, r, "/dashboard/preferences?saved=1", http.StatusSeeOther)
		return
	}
	zone, enabled, err := store.Preferences(r.Context(), m.UserID)
	if err != nil {
		data.Error = "Hatırlatıcı ayarları şu an okunamıyor. Lütfen tekrar dene."
		h.page(w, http.StatusServiceUnavailable, data)
		return
	}
	data.Timezone, data.ReminderEnabled = zone, enabled
	if r.URL.Query().Get("saved") == "1" {
		data.Message = "Hatırlatıcı ayarları kaydedildi."
	}
	h.page(w, http.StatusOK, data)
}
