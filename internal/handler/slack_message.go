package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"nudge/internal/service"
)

type SlackMessageHandler struct {
	service *service.SlackMessagingService
}

func NewSlackMessageHandler(messageService *service.SlackMessagingService) *SlackMessageHandler {
	return &SlackMessageHandler{service: messageService}
}

func (h *SlackMessageHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/slack/test-message", h.sendTestMessage)
}

type sendTestMessageRequest struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
}

func (h *SlackMessageHandler) sendTestMessage(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromHeader(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var request sendTestMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	request.Channel = strings.TrimSpace(request.Channel)
	request.Text = strings.TrimSpace(request.Text)

	if err := h.service.SendMessage(r.Context(), userID, request.Channel, request.Text); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Slack message sent"})
}
