package handler

import (
	"net/http"

	"nudge/internal/service"
)

type OnboardingHandler struct {
	service *service.OnboardingService
	token   string
}

func NewOnboardingHandler(onboardingService *service.OnboardingService, token string) *OnboardingHandler {
	return &OnboardingHandler{service: onboardingService, token: token}
}

func (h *OnboardingHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/reminders/test-onboarding", h.testOnboarding)
}

func (h *OnboardingHandler) testOnboarding(w http.ResponseWriter, r *http.Request) {
	if h.token == "" || r.Header.Get("X-Test-Token") != h.token {
		writeError(w, http.StatusUnauthorized, "invalid test token")
		return
	}
	userID, err := userIDFromHeader(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.service.Send(r.Context(), userID); err != nil {
		writeError(w, http.StatusBadGateway, "could not send onboarding message")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "onboarding message sent"})
}
