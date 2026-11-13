package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"nudge/internal/workspace"
)

func retiredDashboard(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	writeDashboardJSON(w, http.StatusGone, map[string]string{"error": "Eski genel yönetim API’si kapatıldı. Çalışma alanına özel dashboard’u kullanın."})
}
func (h *WorkspaceHandler) WithAI(store workspace.AIStore, catalog workspace.Catalog) {
	h.ai = store
	h.catalog = catalog
}
func (h *WorkspaceHandler) manager(w http.ResponseWriter, r *http.Request) (workspace.Member, string, bool) {
	m, csrf, ok := h.session(w, r)
	if !ok {
		return m, csrf, false
	}
	if !m.Manager {
		h.page(w, 403, workspacePage{Member: m, Error: "Bu bölümü yalnızca çalışma alanı yöneticisi yönetebilir."})
		return m, csrf, false
	}
	return m, csrf, true
}
func (h *WorkspaceHandler) aiPage(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.manager(w, r)
	if !ok {
		return
	}
	if h.ai == nil || h.catalog == nil {
		h.page(w, 503, workspacePage{Member: m, Error: "AI ayarları şu an kullanılamıyor."})
		return
	}
	data := workspacePage{Member: m, CSRF: csrf, Section: "ai", Title: "AI ayarları", Description: "Kendi API anahtarını kullan, çalışma alanının modellerini sen seç."}
	data.RoutingTools = h.routingTools()
	var err error
	data.AI, err = h.ai.AISettings(r.Context(), m.UserID)
	if err != nil {
		h.page(w, 503, workspacePage{Member: m, Error: "AI ayarları okunamadı."})
		return
	}
	if data.AI.HasKey {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		creds, e := h.ai.AICredentials(ctx, m.UserID)
		if e == nil {
			data.Models, e = h.catalog.Models(ctx, creds.Provider, creds.APIKey)
		}
		if e != nil {
			data.Message = "Model listesi şu an alınamıyor. Mevcut ayarlar korunuyor; anahtarını, kotanı ve sağlayıcı erişimini kontrol et."
		}
	}
	if r.URL.Query().Get("done") == "saved" {
		data.Message = "AI ayarları kaydedildi. Kullanım ve ücretlendirme bu çalışma alanının sağlayıcı hesabına aittir."
	}
	h.page(w, 200, data)
}
func (h *WorkspaceHandler) saveAI(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.manager(w, r)
	if !ok {
		return
	}
	fail := func(code int, message string) {
		h.page(w, code, workspacePage{Member: m, CSRF: csrf, Section: "ai", Error: message})
	}
	if h.ai == nil || h.catalog == nil {
		fail(503, "AI ayarları şu an kullanılamıyor.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	c, err := h.ai.AICredentials(ctx, m.UserID)
	if errors.Is(err, workspace.ErrAINotConfigured) {
		c = workspace.AICredentials{}
	} else if err != nil {
		fail(503, "Mevcut AI ayarları okunamadı.")
		return
	}
	revision := r.PostForm.Get("revision")
	if c.Revision != revision {
		fail(409, "Ayarlar başka bir oturumda değişti. Sayfayı yenile.")
		return
	}
	switch r.PostForm.Get("action") {
	case "credentials":
		provider := r.PostForm.Get("provider")
		key := strings.TrimSpace(r.PostForm.Get("api_key"))
		models, e := h.catalog.Models(ctx, provider, key)
		if e != nil {
			fail(400, e.Error())
			return
		}
		if len(models) == 0 {
			fail(400, "Bu hesapta seçilebilecek bir metin modeli bulunamadı.")
			return
		}
		c.Provider = provider
		c.APIKey = key
		c.Model = ""
		c.ComplexModel = ""
		c.ToolModels = nil
	case "models":
		if c.APIKey == "" {
			fail(400, "Önce API anahtarını ekle.")
			return
		}
		models, e := h.catalog.Models(ctx, c.Provider, c.APIKey)
		if e != nil {
			fail(400, e.Error())
			return
		}
		allowed := map[string]bool{}
		for _, model := range models {
			allowed[model.ID] = true
		}
		model, complex := r.PostForm.Get("model"), r.PostForm.Get("complex_model")
		if !allowed[model] || (complex != "" && !allowed[complex]) {
			fail(400, "Sağlayıcı listesinden geçerli bir model seç.")
			return
		}
		c.Model = model
		c.ComplexModel = complex
	case "route":
		tool, model := r.PostForm.Get("tool"), r.PostForm.Get("route_model")
		known := false
		for _, name := range h.routingTools() {
			if name == tool {
				known = true
				break
			}
		}
		if !known {
			fail(400, "Listeden geçerli bir araç seç.")
			return
		}
		if model != "" {
			models, e := h.catalog.Models(ctx, c.Provider, c.APIKey)
			if e != nil {
				fail(400, "Model listesi alınamadı.")
				return
			}
			allowed := false
			for _, m := range models {
				if m.ID == model {
					allowed = true
					break
				}
			}
			if !allowed {
				fail(400, "Listeden geçerli bir model seç.")
				return
			}
		}
		if c.ToolModels == nil {
			c.ToolModels = map[string]string{}
		}
		if model == "" {
			delete(c.ToolModels, tool)
		} else {
			c.ToolModels[tool] = model
		}
	case "remove":
		c.Provider = ""
		c.APIKey = ""
		c.Model = ""
		c.ToolModels = nil
		c.ComplexModel = "" // Search settings are separately removable.
	case "search":
		key, model := strings.TrimSpace(r.PostForm.Get("search_api_key")), strings.TrimSpace(r.PostForm.Get("search_model"))
		if err := h.catalog.VerifySearch(ctx, key, model); err != nil {
			fail(400, err.Error())
			return
		}
		c.SearchAPIKey = key
		c.SearchModel = model
	case "remove_search":
		c.SearchAPIKey = ""
		c.SearchModel = ""
	default:
		fail(400, "Geçersiz ayar işlemi.")
		return
	}
	if err := h.ai.SaveAI(ctx, m.UserID, c, revision); err != nil {
		if errors.Is(err, workspace.ErrAIConflict) {
			fail(409, "Ayarlar değişti. Sayfayı yenileyip tekrar dene.")
		} else {
			fail(503, "AI ayarları kaydedilemedi.")
		}
		return
	}
	http.Redirect(w, r, "/dashboard/ai?done=saved", http.StatusSeeOther)
}
func (h *WorkspaceHandler) teamPage(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.manager(w, r)
	if !ok {
		return
	}
	data := workspacePage{Member: m, CSRF: csrf, Section: "team", Title: "Çalışma alanı", Description: "Slack çalışma alanın ve Nudgy’yi kullanan üyelerin."}
	store, ok := h.store.(interface {
		Members(context.Context, int64) ([]workspace.Member, error)
	})
	if !ok {
		h.page(w, 503, workspacePage{Member: m, Error: "Üyeler şu an listelenemiyor."})
		return
	}
	members, err := store.Members(r.Context(), m.UserID)
	if err != nil {
		h.page(w, 503, workspacePage{Member: m, Error: "Üyeler yüklenemedi."})
		return
	}
	data.Members = members
	h.page(w, 200, data)
}
