package handler

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"nudge/internal/repository"
	"nudge/internal/service"
)

type FeedbinDigestSlackClient interface {
	OpenDirectMessage(ctx context.Context, token, slackUserID string) (string, error)
	PostMessage(ctx context.Context, token, channel, text string) error
}

type FeedbinDigestHandler struct {
	installations repository.InstallationRepository
	digest        *service.FeedbinDigestService
	ai            service.TextSummarizer
	slack         FeedbinDigestSlackClient
	timezone      *time.Location
	testToken     string
}

func NewFeedbinDigestHandler(installations repository.InstallationRepository, digest *service.FeedbinDigestService, ai service.TextSummarizer, slack FeedbinDigestSlackClient, timezone *time.Location, testToken string) *FeedbinDigestHandler {
	return &FeedbinDigestHandler{installations: installations, digest: digest, ai: ai, slack: slack, timezone: timezone, testToken: testToken}
}

func (h *FeedbinDigestHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/feedbin/test-digest", h.testDigest)
}

func (h *FeedbinDigestHandler) testDigest(w http.ResponseWriter, r *http.Request) {
	if h.testToken == "" || r.Header.Get("X-Test-Token") != h.testToken {
		writeError(w, http.StatusUnauthorized, "invalid test token")
		return
	}
	userID, err := userIDFromHeader(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	localNow := time.Now().In(h.timezone)
	since := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 0, 0, 0, h.timezone)
	if localNow.Before(since) {
		since = since.Add(-24 * time.Hour)
	}
	digest, err := h.digest.BuildAnalysisInput(r.Context(), userID, since)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not build Feedbin digest")
		return
	}
	if h.ai != nil && !strings.Contains(digest, "yeni RSS yok") {
		if analyzed, aiErr := h.ai.Summarize(r.Context(), digest); aiErr == nil {
			digest = analyzed
		} else {
			log.Printf("Feedbin AI summary failed for test endpoint: %v", aiErr)
		}
	}
	installation, err := h.installations.GetByUserID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load Slack installation")
		return
	}
	channel, err := h.slack.OpenDirectMessage(r.Context(), installation.BotToken, installation.SlackUserID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not open Slack direct message")
		return
	}
	if err := h.slack.PostMessage(r.Context(), installation.BotToken, channel, digest); err != nil {
		writeError(w, http.StatusBadGateway, "could not send Feedbin digest")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Feedbin digest sent"})
}
