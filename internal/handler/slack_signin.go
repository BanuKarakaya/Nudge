package handler

import (
	"context"
	"net/http"

	"nudge/internal/slack"
)

type slackLoginRepository interface {
	SignInUser(context.Context, string, string) (int64, error)
}

func (h *SlackHandler) signInReady() bool {
	_, ok := h.installRepo.(slackLoginRepository)
	return ok && h.workspace != nil && h.oauth.ClientID != "" && h.oauth.ClientSecret != "" && h.oauth.RedirectURL != ""
}

func (h *SlackHandler) signIn(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	if !h.signInReady() {
		h.signInError(w, 503, "Slack ile giriş şu an kullanılamıyor. Lütfen daha sonra tekrar dene.")
		return
	}
	raw, err := newOAuthState()
	if err != nil {
		h.signInError(w, 503, "Giriş başlatılamadı. Yeniden dene.")
		return
	}
	state := "signin." + raw
	if err := h.saveState(r.Context(), state); err != nil {
		writeError(w, 503, "OAuth state unavailable")
		return
	}
	setOAuthCookie(w, r, "nudgy_slack_signin", state, "/slack/oauth/callback")
	http.Redirect(w, r, h.oauth.SignInURL(state, workspaceHash("slack-signin:"+state)), http.StatusFound)
}

func (h *SlackHandler) signInCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if !consumeOAuthCookie(w, r, "nudgy_slack_signin", state, "/slack/oauth/callback") || !h.consumeState(r.Context(), state) {
		h.signInError(w, 400, "Giriş isteğin geçersiz veya süresi dolmuş. Slack ile yeniden giriş yap.")
		return
	}
	if !h.signInReady() {
		h.signInError(w, 503, "Slack ile giriş şu an kullanılamıyor.")
		return
	}
	if r.URL.Query().Get("error") != "" {
		h.signInError(w, 400, "Slack girişini iptal ettin. Hazır olduğunda yeniden deneyebilirsin.")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		h.signInError(w, 400, "Slack giriş bilgisi eksik. Yeniden giriş yap.")
		return
	}
	identity, err := h.exchangeSignIn(r.Context(), code, workspaceHash("slack-signin:"+state))
	if err != nil {
		h.signInError(w, 502, "Slack hesabın doğrulanamadı. Yeniden giriş yap.")
		return
	}
	// This inserts only into an already installed workspace and never changes
	// its installer/manager, bot token, or integration permissions.
	user, err := h.installRepo.(slackLoginRepository).SignInUser(r.Context(), identity.Team, identity.User)
	if err != nil {
		h.signInError(w, 403, "Bu çalışma alanına giriş açılamadı. Doğru Slack çalışma alanını seçtiğinden ve Nudgy’nin kurulmuş olduğundan emin ol. Yeni kurulum için aşağıdaki bağlantıyı kullan.")
		return
	}
	m, err := h.workspace.store.Member(r.Context(), user)
	if err != nil || m.Team != identity.Team || m.SlackUser != identity.User {
		h.signInError(w, 403, "Çalışma alanı üyeliğin doğrulanamadı.")
		return
	}
	if err := h.workspace.startSession(w, r, user, ""); err != nil {
		h.signInError(w, 503, "Panel oturumu açılamadı. Yeniden dene.")
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (h *SlackHandler) signInError(w http.ResponseWriter, status int, message string) {
	if h.workspace == nil {
		writeError(w, status, message)
		return
	}
	h.workspace.page(w, status, workspacePage{SignIn: true, Title: "Giriş", Error: message})
}

// Kept as a function dependency so handler tests can exercise state and tenant
// checks separately from the cryptographic/token exchange tests.
type slackSignInExchange func(context.Context, string, string) (slack.SignInIdentity, error)
