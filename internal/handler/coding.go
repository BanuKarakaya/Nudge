package handler

import (
	"context"
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
	admin.HandleFunc("GET /api/v1/coding/github/connections", func(w http.ResponseWriter, r *http.Request) {
		privatePageHeaders(w)
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		bindings, err := h.service.ConnectionBindings(ctx)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "connections unavailable")
			return
		}
		writeJSON(w, http.StatusOK, bindings)
	})
	admin.HandleFunc("GET /api/v1/coding/github/connection", func(w http.ResponseWriter, r *http.Request) {
		privatePageHeaders(w)
		userID, err := userIDFromHeader(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid user")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		installation, err := h.installations.GetByUserID(ctx, userID)
		if err != nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		access, err := h.service.RepositoryAccess(ctx, coding.Identity{UserID: userID, Team: installation.TeamID})
		if err != nil {
			writeError(w, http.StatusForbidden, "GitHub access could not be verified")
			return
		}
		writeJSON(w, http.StatusOK, access)
	})
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
		renderGitHubConnectionPage(w, http.StatusUnauthorized, githubPageData{Title: "Yeni bir bağlantı alalım.", Description: "Bu bağlantı geçersiz veya süresi dolmuş. Slack’te Nudgy’den GitHub hesabını bağlamak için yeni bir bağlantı iste."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	state, redirect, err := h.service.BeginConnect(ctx, userID)
	if err != nil {
		renderGitHubConnectionPage(w, http.StatusServiceUnavailable, githubPageData{Title: "Küçük bir aksilik oldu.", Description: "GitHub bağlantısını şu an başlatamıyoruz. Biraz sonra tekrar deneyebilirsin."})
		return
	}
	setOAuthCookie(w, r, "nudgy_github_oauth", state, "/github/oauth/callback")
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (h *CodingHandler) callback(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	state := r.URL.Query().Get("state")
	if !consumeOAuthCookie(w, r, "nudgy_github_oauth", state, "/github/oauth/callback") {
		renderGitHubConnectionPage(w, http.StatusBadRequest, githubPageData{Title: "Bağlantıyı yeniden başlatalım.", Description: "Bu tarayıcıdaki bağlantı oturumunu doğrulayamadık. Slack’te Nudgy’den yeni bir bağlantı iste ve işlemi aynı tarayıcıda tamamla."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if r.URL.Query().Get("error") != "" {
		renderGitHubConnectionPage(w, http.StatusBadRequest, githubPageData{Title: "Bağlantı tamamlanmadı.", Description: "GitHub izni verilmedi. Hazır olduğunda Slack’te Nudgy’den yeni bir bağlantı isteyebilirsin."})
		return
	}
	account, err := h.service.CompleteConnectAccount(ctx, state, r.URL.Query().Get("code"))
	if err != nil {
		renderGitHubConnectionPage(w, http.StatusBadRequest, githubPageData{Title: "Bağlantıyı tamamlayamadık.", Description: "Slack’te Nudgy’den yeni bir GitHub bağlantısı isteyip tekrar deneyebilirsin."})
		return
	}
	renderGitHubConnectionPage(w, http.StatusOK, githubPageData{Success: true, Title: "GitHub’la tanıştık.", Description: "Projelerin için küçük bir yardımcı, artık Slack’te yanında.", Login: account.Login})
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
