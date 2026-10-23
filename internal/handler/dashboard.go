package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"nudge/internal/dashboard"
	"nudge/internal/repository"
)

type DashboardHandler struct {
	repository repository.AnalyticsRepository
	token      string
	files      http.Handler
}

func NewDashboardHandler(analytics repository.AnalyticsRepository, token string) *DashboardHandler {
	return &DashboardHandler{
		repository: analytics,
		token:      token,
		files:      http.FileServer(http.FS(dashboard.Files())),
	}
}

func (h *DashboardHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /dashboard", h.index)
	mux.Handle("GET /dashboard/", http.StripPrefix("/dashboard/", h.files))
	mux.HandleFunc("GET /api/dashboard", h.api)
}

func (h *DashboardHandler) index(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	// The shell is intentionally public so the dashboard can render its login-
	// free empty state. Data access remains protected by the API token below.
	clone := r.Clone(r.Context())
	clone.URL.Path = "/"
	h.files.ServeHTTP(w, clone)
}

func (h *DashboardHandler) api(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	if strings.TrimSpace(h.token) == "" {
		writeDashboardJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "dashboard is disabled"})
		return
	}
	if !h.authorized(r) {
		writeDashboardJSON(w, http.StatusUnauthorized, map[string]string{"error": "dashboard token required"})
		return
	}

	if h.repository == nil {
		writeDashboardJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "analytics unavailable"})
		return
	}

	to := time.Now()
	from := to.Add(-7 * 24 * time.Hour)
	switch r.URL.Query().Get("range") {
	case "24h":
		from = to.Add(-24 * time.Hour)
	case "30d":
		from = to.Add(-30 * 24 * time.Hour)
	case "all":
		from = time.Unix(0, 0)
	}

	result, err := h.repository.Dashboard(r.Context(), repository.DashboardFilters{
		From: from, To: to, Model: strings.TrimSpace(r.URL.Query().Get("model")),
		Status: strings.TrimSpace(r.URL.Query().Get("status")), Limit: 24,
	})
	if err != nil {
		writeDashboardJSON(w, http.StatusInternalServerError, map[string]string{"error": "dashboard data unavailable"})
		return
	}
	writeDashboardJSON(w, http.StatusOK, result)
}

func (h *DashboardHandler) authorized(r *http.Request) bool {
	provided := r.Header.Get("X-Dashboard-Token")
	if provided == "" {
		provided, _ = strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	return tokenMatches(h.token, provided)
}

func writeDashboardJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
