package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"nudge/internal/service"
	"nudge/internal/slack"
)

type SlackInteractionHandler struct {
	signingSecret string
	bookmarks     service.BookmarkService
}

func NewSlackInteractionHandler(signingSecret string, bookmarks service.BookmarkService) *SlackInteractionHandler {
	return &SlackInteractionHandler{signingSecret: signingSecret, bookmarks: bookmarks}
}

func (h *SlackInteractionHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /slack/interactions", h.handle)
}

type slackInteractionPayload struct {
	Type    string `json:"type"`
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	} `json:"actions"`
}

type bookmarkActionValue struct {
	UserID     int64 `json:"user_id"`
	BookmarkID int64 `json:"bookmark_id"`
}

func (h *SlackInteractionHandler) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil || !slack.VerifySignature(
		h.signingSecret,
		r.Header.Get("X-Slack-Request-Timestamp"),
		r.Header.Get("X-Slack-Signature"),
		body,
	) {
		writeError(w, http.StatusUnauthorized, "invalid Slack signature")
		return
	}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid Slack interaction payload")
		return
	}
	var payload slackInteractionPayload
	if err := json.Unmarshal([]byte(values.Get("payload")), &payload); err != nil || len(payload.Actions) == 0 {
		writeError(w, http.StatusBadRequest, "invalid Slack interaction payload")
		return
	}

	var action bookmarkActionValue
	if err := json.Unmarshal([]byte(payload.Actions[0].Value), &action); err != nil {
		writeError(w, http.StatusBadRequest, "invalid bookmark action")
		return
	}

	switch payload.Actions[0].ActionID {
	case "bookmark_read":
		err = h.bookmarks.MarkRead(r.Context(), action.UserID, action.BookmarkID)
	case "bookmark_unread":
		err = h.bookmarks.MarkUnread(r.Context(), action.UserID, action.BookmarkID)
	default:
		writeError(w, http.StatusBadRequest, "unknown Slack action")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not update bookmark status")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"text": "Bookmark durumu güncellendi."})
}
