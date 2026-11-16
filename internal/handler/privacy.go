package handler

import (
	"context"
	"encoding/json"
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

func (h *WorkspaceHandler) personalData(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.session(w, r)
	if !ok {
		return
	}
	data := workspacePage{Member: m, CSRF: csrf, Section: "data", Title: "Verilerim", Description: "Kişisel verilerini indir veya Nudgy’de saklanan geçmişini ve bağlantılarını yönet."}
	if r.URL.Path == "/dashboard/audit" || r.URL.Path == "/dashboard/audit/export" {
		data.Section, data.Title, data.Description = "audit", "Denetim kaydı", "Çalışma alanındaki son 200 ayar ve güvenlik işlemi."
	}
	store, ok := h.store.(personalDataStore)
	if !ok {
		data.Error = "Veri hizmeti şu an kullanılamıyor."
		h.page(w, http.StatusServiceUnavailable, data)
		return
	}
	switch r.URL.Path {
	case "/dashboard/data/delete":
		if r.PostForm.Get("confirm") != "SİL" {
			data.FormError = "Silme işlemi yapılmadı. Onay alanına SİL yazmalısın."
			h.page(w, http.StatusBadRequest, data)
			return
		}
		if h.nativeData != nil {
			if err := h.nativeData.DeleteUserState(r.Context(), m.UserID); err != nil {
				data.FormError = "AI geçmişi silinemedi; lütfen tekrar dene."
				h.page(w, http.StatusServiceUnavailable, data)
				return
			}
		}
		if store.DeletePersonalData(r.Context(), m.UserID) != nil {
			data.FormError = "Silme tamamlanamadı; lütfen tekrar dene."
			h.page(w, http.StatusServiceUnavailable, data)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: workspaceCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
	case "/dashboard/data/export", "/dashboard/audit", "/dashboard/audit/export":
		var raw json.RawMessage
		var err error
		if data.Section == "audit" {
			if !m.Manager {
				data.Error = "Denetim kaydını yalnızca çalışma alanı yöneticisi görebilir."
				h.page(w, http.StatusForbidden, data)
				return
			}
			raw, err = store.AuditLog(r.Context(), m.UserID)
		} else {
			raw, err = store.ExportPersonalData(r.Context(), m.UserID)
		}
		if err != nil {
			data.Error = "Veriler şu an alınamıyor. Lütfen tekrar dene."
			h.page(w, http.StatusServiceUnavailable, data)
			return
		}
		if r.URL.Path == "/dashboard/audit" {
			if json.Unmarshal(raw, &data.AuditRows) != nil {
				data.Error = "Denetim kaydı görüntülenemiyor."
				h.page(w, http.StatusServiceUnavailable, data)
				return
			}
			h.page(w, http.StatusOK, data)
			return
		}
		filename := "nudgy-personal-data.json"
		if data.Section == "audit" {
			filename = "nudgy-audit-log.json"
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	default:
		h.page(w, http.StatusOK, data)
	}
}
