package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"nudge/internal/service"
)

type SlackBookmarkHandler struct {
	service *service.SlackBookmarkMessagingService
}

func NewSlackBookmarkHandler(bookmarkService *service.SlackBookmarkMessagingService) *SlackBookmarkHandler {
	return &SlackBookmarkHandler{service: bookmarkService}
}

func (h *SlackBookmarkHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/slack/bookmark-message", h.send)
}

type sendBookmarkMessageRequest struct {
	Channel    string `json:"channel"`
	BookmarkID int64  `json:"bookmark_id"`
}

func (h *SlackBookmarkHandler) send(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromHeader(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var request sendBookmarkMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	request.Channel = strings.TrimSpace(request.Channel)
	if err := h.service.SendBookmark(r.Context(), userID, request.BookmarkID, request.Channel); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "bookmark message sent"})
}

func parseBookmarkID(value string) (int64, error) {
	return strconv.ParseInt(value, 10, 64)
}
