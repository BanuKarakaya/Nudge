package handler

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"sync"
	"time"

	"nudge/internal/raindrop"
	"nudge/internal/service"
)

type RaindropHandler struct {
	oauth   raindrop.OAuthClient
	service *service.RaindropService
	states  *raindropStateStore
}

func NewRaindropHandler(oauthClient raindrop.OAuthClient, raindropService *service.RaindropService) *RaindropHandler {
	return &RaindropHandler{
		oauth:   oauthClient,
		service: raindropService,
		states:  &raindropStateStore{values: make(map[string]raindropState)},
	}
}

func (h *RaindropHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /raindrop/install", h.install)
	mux.HandleFunc("GET /raindrop/oauth/callback", h.callback)
}

func (h *RaindropHandler) install(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "user_id must be a positive integer")
		return
	}
	if h.oauth.ClientID == "" || h.oauth.RedirectURL == "" {
		writeError(w, http.StatusServiceUnavailable, "Raindrop OAuth is not configured")
		return
	}

	state, err := randomState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create OAuth state")
		return
	}
	h.states.add(state, userID, 10*time.Minute)
	http.Redirect(w, r, h.oauth.InstallURL(state), http.StatusFound)
}

func (h *RaindropHandler) callback(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.states.consume(r.URL.Query().Get("state"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid or expired OAuth state")
		return
	}
	if r.URL.Query().Get("error") != "" {
		writeError(w, http.StatusBadRequest, "Raindrop authorization was declined")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing OAuth code")
		return
	}
	if err := h.service.Connect(r.Context(), userID, code); err != nil {
		writeError(w, http.StatusBadGateway, "could not save Raindrop connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "Raindrop account connected",
		"user_id": userID,
	})
}

func randomState() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

type raindropState struct {
	userID    int64
	expiresAt time.Time
}

type raindropStateStore struct {
	mu     sync.Mutex
	values map[string]raindropState
}

func (s *raindropStateStore) add(state string, userID int64, lifetime time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[state] = raindropState{userID: userID, expiresAt: time.Now().Add(lifetime)}
}

func (s *raindropStateStore) consume(state string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[state]
	delete(s.values, state)
	return value.userID, ok && state != "" && time.Now().Before(value.expiresAt)
}
