package handler

import (
	"crypto/rand"
	"encoding/hex"
	"html/template"
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
	writeRaindropSuccess(w)
}

func writeRaindropSuccess(w http.ResponseWriter) {
	const page = `<!doctype html>
<html lang="tr">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Nudge’ye hoş geldin</title>
  <style>
    :root { color-scheme: light; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #f7f7fb; color: #1f1f2e; }
    .card { width: min(420px, calc(100% - 40px)); box-sizing: border-box; padding: 40px 32px; text-align: center; background: white; border: 1px solid #e8e8f0; border-radius: 20px; box-shadow: 0 16px 40px rgba(31, 31, 46, .08); }
    .icon { width: 64px; height: 64px; margin: 0 auto 20px; display: grid; place-items: center; border-radius: 50%; background: #e8f8f0; color: #168653; font-size: 32px; }
    h1 { margin: 0 0 12px; font-size: 25px; }
    p { margin: 0; color: #686879; line-height: 1.55; }
    .hint { margin-top: 22px; font-size: 13px; }
  </style>
</head>
<body>
  <main class="card">
    <div class="icon" aria-hidden="true">✓</div>
    <h1>Raindrop başarıyla bağlandı</h1>
    <p>Nudge artık kaydettiğin bookmark’ları takip edebilir ve sana hatırlatmalar gönderebilir.</p>
    <p class="hint">Bu pencereyi kapatabilirsin.</p>
  </main>
</body>
</html>`
	// Parse the HTML as a template so future user-facing copy remains safely escaped.
	tmpl := template.Must(template.New("raindrop-success").Parse(page))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = tmpl.Execute(w, nil)
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
