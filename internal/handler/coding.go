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
	public.HandleFunc("GET /github/setup", h.setup)
	public.HandleFunc("POST /github/setup", h.setup)
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
	setOAuthCookie(w, r, "nudgy_github_install", state, "/github/setup")
	renderGitHubConnectionPage(w, http.StatusOK, githubPageData{Selecting: true, Title: "GitHub’ını bağlayalım.", Description: "Nudgy’nin erişeceği repoları GitHub’da seç, ardından hesabını doğrula.", InstallationURL: redirect, State: state})
}

func (h *CodingHandler) setup(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	// GitHub can omit state after an existing installation is updated. The
	// private, HttpOnly browser cookie still binds this one-use setup session;
	// PKCE OAuth and GitHub API verification follow before any account is saved.
	cookie, cookieErr := r.Cookie("nudgy_github_install")
	state := r.URL.Query().Get("state")
	manual := r.Method == http.MethodPost
	if manual {
		// Our no-referrer policy makes browsers send Origin: null for form
		// submissions. Opaque/absent origins still require the unpredictable
		// form state, matching HttpOnly cookie and single-use server state below.
		origin := r.Header.Get("Origin")
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" || (origin != "" && origin != "null" && origin != h.publicURL) {
			renderGitHubConnectionPage(w, http.StatusForbidden, githubPageData{Title: "Bağlantıyı yeniden başlatalım.", Description: "Bu isteğin bağlantı sayfasından geldiğini doğrulayamadık. Slack’te Nudgy’nin gönderdiği özel bağlantıyı açıp tekrar dene."})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid form")
			return
		}
		state = r.PostForm.Get("state")
	}
	if !manual && state == "" && cookieErr == nil {
		state = cookie.Value
	}
	if !consumeOAuthCookie(w, r, "nudgy_github_install", state, "/github/setup") {
		renderGitHubConnectionPage(w, http.StatusBadRequest, githubPageData{Title: "Bağlantıyı yeniden başlatalım.", Description: "Slack’te Nudgy’nin sana gönderdiği özel bağlantıyı açıp hesabını ve repolarını seç."})
		return
	}
	var installationID int64
	var err error
	if !manual {
		installationID, err = strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
	}
	action := r.URL.Query().Get("setup_action")
	if !manual && (err != nil || installationID <= 0 || (action != "install" && action != "update")) {
		renderGitHubConnectionPage(w, http.StatusBadRequest, githubPageData{Title: "Repo seçimi tamamlanmadı.", Description: "GitHub’da hesabını ve bağlamak istediğin repoları seçip kurulumu kaydet. Kuruluş izni gerekiyorsa önce yöneticinin onaylaması gerekir."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	oauthState, redirect, err := h.service.ContinueConnect(ctx, state, installationID)
	if err != nil {
		renderGitHubConnectionPage(w, http.StatusBadRequest, githubPageData{Title: "Bağlantıyı yeniden başlatalım.", Description: "Bağlantı oturumunun süresi dolmuş olabilir. Slack’te Nudgy’den yeni bir bağlantı iste."})
		return
	}
	setOAuthCookie(w, r, "nudgy_github_oauth", oauthState, "/github/oauth/callback")
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
		renderGitHubConnectionPage(w, http.StatusBadRequest, githubPageData{Title: "Bağlantıyı tamamlayamadık.", Description: "GitHub hesabını ve seçtiğin repoların erişimini doğrulayamadık. Repo seçimini ve yetkilendirmeyi aynı hesapla tamamlamak için Slack’te Nudgy’den yeni bir bağlantı iste."})
		return
	}
	renderGitHubConnectionPage(w, http.StatusOK, githubPageData{Success: true, Title: "GitHub’ın bağlandı.", Description: "Hesabın ve seçtiğin repolar için bağlantı tamamlandı. Slack’e dönebilirsin.", Login: account.Login})
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
		return h.client.PostMessage(ctx, installation.BotToken, channel, "GitHub hesabını Nudgy’ye bağlamak için özel bağlantın:\n"+link+"\nGitHub’da kendi hesabını ve bağlamak istediğin repoları seç, ardından Nudgy’ye izin ver. Bu bağlantı yalnızca hesabını ve repo izinlerini bağlar. Linki paylaşma.")
	})
}
