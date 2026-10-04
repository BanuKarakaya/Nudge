package handler

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

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
	Type        string `json:"type"`
	ResponseURL string `json:"response_url"`
	Actions     []struct {
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

	blocks := bookmarkStatusBlocks(payload.Actions[0].ActionID, payload.Actions[0].Value)
	if payload.ResponseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := (slack.APIClient{HTTPClient: &http.Client{Timeout: 2 * time.Second}}).UpdateInteraction(ctx, payload.ResponseURL, blocks); err != nil {
			log.Printf("update Slack interaction message: %v", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"text": "Bookmark durumu güncellendi."})
}

func bookmarkStatusBlocks(actionID, value string) []map[string]any {
	readText := "Okudum"
	unreadText := "Okumadım"
	if actionID == "bookmark_read" {
		readText = "✅ Okudum"
	} else {
		unreadText = "✅ Okumadım"
	}

	return []map[string]any{{
		"type": "actions",
		"elements": []map[string]any{
			{
				"type":      "button",
				"text":      map[string]string{"type": "plain_text", "text": readText},
				"action_id": "bookmark_read",
				"value":     value,
				"style":     "primary",
			},
			{
				"type":      "button",
				"text":      map[string]string{"type": "plain_text", "text": unreadText},
				"action_id": "bookmark_unread",
				"value":     value,
			},
		},
	}}
}
