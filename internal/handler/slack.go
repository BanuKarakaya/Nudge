package handler

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/slack"
)

type SlackHandler struct {
	oauth       slack.OAuthClient
	installRepo repository.InstallationRepository
	states      *oauthStateStore
}

func NewSlackHandler(oauthClient slack.OAuthClient, installRepo repository.InstallationRepository) *SlackHandler {
	return &SlackHandler{
		oauth:       oauthClient,
		installRepo: installRepo,
		states:      newOAuthStateStore(),
	}
}

func (h *SlackHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /slack/install", h.install)
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
	h.states.add(state, 10*time.Minute)
	http.Redirect(w, r, h.oauth.InstallURL(state), http.StatusFound)
}

func (h *SlackHandler) callback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if !h.states.consume(state) {
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
	s.values[state] = time.Now().Add(lifetime)
}

func (s *oauthStateStore) consume(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	expiresAt, ok := s.values[state]
	delete(s.values, state)
	return ok && time.Now().Before(expiresAt)
}
