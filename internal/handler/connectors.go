package handler

import (
	"context"
	_ "embed"
	"errors"
	"net/http"
	"time"

	"nudge/internal/connector"
)

//go:embed connectors.html
var connectorHTML string

func (h *WorkspaceHandler) WithConnectors(s *connector.Service, events *SlackEventHandler) {
	h.connectors = s
	events.connectors = s
}
func (h *WorkspaceHandler) connectorPage(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.manager(w, r)
	if !ok {
		return
	}
	data := workspacePage{Member: m, CSRF: csrf, Section: "connectors", Title: "Özel bağlantılar"}
	if h.connectors == nil {
		data.Error = "Özel bağlantılar bu kurulumda kullanılamıyor."
		h.page(w, 503, data)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	if r.Method == http.MethodPost {
		id, revision := r.PostForm.Get("id"), r.PostForm.Get("revision")
		var err error
		switch r.PostForm.Get("action") {
		case "discover":
			err = h.connectors.Discover(ctx, m.UserID, id, revision, r.PostForm.Get("name"), r.PostForm.Get("endpoint"), r.PostForm.Get("token"))
		case "select":
			enabled := r.PostForm.Get("enabled") == "true"
			if enabled && r.PostForm.Get("shared_account") != "true" {
				err = errors.New("Ortak servis hesabının ekiple paylaşılmasını doğrulayın.")
			} else {
				err = h.connectors.Select(ctx, m.UserID, id, revision, enabled, r.PostForm["tool"])
			}
		case "delete":
			err = h.connectors.Delete(ctx, m.UserID, id, revision)
		default:
			err = errors.New("Geçersiz işlem.")
		}
		if err != nil {
			data.Error = "Bağlantı kaydedilemedi. Adresi, erişim anahtarını, seçili araçları ve sunucu erişimini kontrol et. Değişiklik başka bir sekmede yapıldıysa sayfayı yenile. HTTPS ve Bearer anahtarı destekleyen bir MCP sunucusu gerekli."
			h.page(w, 400, data)
			return
		}
		http.Redirect(w, r, "/dashboard/connectors?done=saved", http.StatusSeeOther)
		return
	}
	var err error
	data.Connections, err = h.connectors.List(ctx, m.UserID)
	if err != nil {
		data.Error = "Bağlantılar okunamadı."
		h.page(w, 503, data)
		return
	}
	if r.URL.Query().Get("done") == "saved" {
		data.Message = "Kaydedildi. Bağlantı değiştiğinde eski işlem onayları ve otomatik izinler geçersizleşir."
	}
	h.page(w, 200, data)
}
