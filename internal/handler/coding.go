package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"nudge/internal/coding"
	"nudge/internal/repository"
	"nudge/internal/secure"
	"nudge/internal/service"
)

type codingMessenger interface {
	OpenDirectMessage(context.Context, string, string) (string, error)
	PostMessage(context.Context, string, string, string) error
}

type CodingHandler struct {
	service       *coding.Service
	installations repository.InstallationRepository
	client        codingMessenger
	links         secure.LinkSigner
	publicURL     string
}

func NewCodingHandler(s *coding.Service, installations repository.InstallationRepository, client codingMessenger, links secure.LinkSigner, publicURL string) *CodingHandler {
	return &CodingHandler{service: s, installations: installations, client: client, links: links, publicURL: publicURL}
}

func (h *CodingHandler) RegisterRoutes(public, admin *http.ServeMux) {
	public.HandleFunc("GET /github/install", h.install)
	public.HandleFunc("GET /github/oauth/callback", h.callback)
	// The admin router must already be wrapped by RequireAPIToken.
	admin.HandleFunc("DELETE /api/v1/coding/github/connection", func(w http.ResponseWriter, r *http.Request) {
		userID, err := userIDFromHeader(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid user")
			return
		}
		if err := h.service.Disconnect(r.Context(), userID); err != nil {
			writeError(w, http.StatusServiceUnavailable, "disconnect failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (h *CodingHandler) install(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || !h.links.Verify(r.URL.Query().Get("token"), userID, "github", time.Now()) {
		writeError(w, http.StatusUnauthorized, "invalid or expired connection link")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	state, redirect, err := h.service.BeginConnect(ctx, userID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "GitHub connection unavailable")
		return
	}
	setOAuthCookie(w, r, "nudgy_github_oauth", state, "/github/oauth/callback")
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (h *CodingHandler) callback(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	state := r.URL.Query().Get("state")
	if !consumeOAuthCookie(w, r, "nudgy_github_oauth", state, "/github/oauth/callback") {
		writeError(w, http.StatusBadRequest, "OAuth browser session mismatch")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if r.URL.Query().Get("error") != "" {
		writeError(w, http.StatusBadRequest, "GitHub authorization declined")
		return
	}
	if err := h.service.CompleteConnect(ctx, state, r.URL.Query().Get("code")); err != nil {
		writeError(w, http.StatusBadRequest, "GitHub bağlantısı tamamlanamadı. Slack'ten yeni bağlantı linki iste.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html lang="tr"><meta charset="utf-8"><title>Nudgy · GitHub</title><h1>GitHub hesabın bağlandı</h1><p>Slack'e dönüp Nudgy'den erişebildiğin repoları listelemesini isteyebilirsin.</p><p>Kod çalıştırma ve PR açma henüz etkin değil; şu an yalnızca görev taslağı hazırlanır.</p></html>`)
}

func (h *CodingHandler) Tools(base service.AgentTools, identity coding.Identity) service.AgentTools {
	return service.NewCodingTools(base, h.service, identity, func(ctx context.Context) error {
		installation, err := h.installations.GetByUserID(ctx, identity.UserID)
		if err != nil {
			return err
		}
		if installation.TeamID != identity.Team {
			return coding.ErrDenied
		}
		// Check the Slack mapping again before delivering a private bearer link.
		userID, err := h.installations.GetUserIDBySlackUserID(ctx, identity.Team, installation.SlackUserID)
		if err != nil || userID != identity.UserID {
			return coding.ErrDenied
		}
		link, err := h.links.URL(h.publicURL, identity.UserID, "github", time.Now())
		if err != nil {
			return err
		}
		channel, err := h.client.OpenDirectMessage(ctx, installation.BotToken, installation.SlackUserID)
		if err != nil {
			return err
		}
		return h.client.PostMessage(ctx, installation.BotToken, channel, "GitHub hesabını Nudgy'ye bağlamak için özel bağlantın:\n"+link+"\nBu linki paylaşma. Şirket yöneticisinin GitHub App'i izin verilen repolara kurmuş olması gerekir.")
	})
}
