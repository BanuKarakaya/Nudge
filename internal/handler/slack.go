package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/slack"
)

type SlackHandler struct {
	workspace  *WorkspaceHandler
	onboarding interface {
		EnqueueWorkspace(context.Context, int64) error
	}
	oauth          slack.OAuthClient
	installRepo    repository.InstallationRepository
	states         *oauthStateStore
	exchangeSignIn slackSignInExchange
}

func (h *SlackHandler) WithWorkspacePortal(p *WorkspaceHandler) *SlackHandler {
	h.workspace = p
	p.slackSignIn = h.signInReady()
	return h
}

func (h *SlackHandler) WithOnboarding(s interface {
	EnqueueWorkspace(context.Context, int64) error
}) *SlackHandler {
	h.onboarding = s
	return h
}

func NewSlackHandler(oauthClient slack.OAuthClient, installRepo repository.InstallationRepository) *SlackHandler {
	return &SlackHandler{
		oauth:          oauthClient,
		installRepo:    installRepo,
		states:         newOAuthStateStore(),
		exchangeSignIn: oauthClient.ExchangeSignIn,
	}
}

func (h *SlackHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /slack/install", h.install)
	mux.HandleFunc("GET /slack/signin", h.signIn)
	mux.HandleFunc("GET /slack/oauth/callback", h.callback)
}

func (h *SlackHandler) install(w http.ResponseWriter, r *http.Request) {
	if h.oauth.ClientID == "" || h.oauth.RedirectURL == "" {
		writeError(w, http.StatusServiceUnavailable, "Slack OAuth is not configured")
		return
	}

	state, err := newOAuthState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create OAuth state")
		return
	}
	if err := h.saveState(r.Context(), state); err != nil {
		writeError(w, 503, "OAuth state unavailable")
		return
	}
	setOAuthCookie(w, r, "nudge_slack_oauth", state, "/slack/oauth/callback")
	http.Redirect(w, r, h.oauth.InstallURL(state), http.StatusFound)
}

func (h *SlackHandler) callback(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	if strings.HasPrefix(r.URL.Query().Get("state"), "signin.") {
		h.signInCallback(w, r)
		return
	}
	if !consumeOAuthCookie(w, r, "nudge_slack_oauth", r.URL.Query().Get("state"), "/slack/oauth/callback") {
		writeError(w, http.StatusBadRequest, "OAuth browser session mismatch")
		return
	}
	state := r.URL.Query().Get("state")
	if !h.consumeState(r.Context(), state) {
		writeError(w, http.StatusBadRequest, "invalid or expired OAuth state")
		return
	}

	if oauthError := r.URL.Query().Get("error"); oauthError != "" {
		writeError(w, http.StatusBadRequest, "Slack authorization was declined")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing OAuth code")
		return
	}

	result, err := h.oauth.ExchangeCode(r.Context(), code)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not complete Slack OAuth")
		return
	}

	if result.Team.ID == "" || result.AuthedUser.ID == "" || result.AccessToken == "" {
		writeError(w, 502, "Slack installation identity missing")
		return
	}
	userID, err := h.installRepo.Save(r.Context(), domain.SlackInstallation{
		TeamID:      result.Team.ID,
		TeamName:    result.Team.Name,
		BotToken:    result.AccessToken,
		SlackUserID: result.AuthedUser.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save Slack installation")
		return
	}

	if h.onboarding != nil {
		if err := h.onboarding.EnqueueWorkspace(r.Context(), userID); err != nil {
			log.Printf("onboarding stage=enqueue_failed user_id=%d", userID)
		}
	}
	if h.workspace != nil {
		if err := h.workspace.startSession(w, r, userID, ""); err != nil {
			writeError(w, 503, "workspace session unavailable")
			return
		}
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "Slack workspace connected",
		"user_id": userID,
	})
}

func newOAuthState() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

type oauthStateStore struct {
	mu     sync.Mutex
	values map[string]time.Time
}

func newOAuthStateStore() *oauthStateStore {
	return &oauthStateStore{values: make(map[string]time.Time)}
}

func (s *oauthStateStore) add(state string, lifetime time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, expires := range s.values {
		if time.Now().After(expires) {
			delete(s.values, key)
		}
	}
	s.values[state] = time.Now().Add(lifetime)
}

func (s *oauthStateStore) consume(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	expiresAt, ok := s.values[state]
	delete(s.values, state)
	return ok && time.Now().Before(expiresAt)
}
