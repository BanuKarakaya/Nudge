package handler

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/service"
	"nudge/internal/slack"
)

type SlackEventHandler struct {
	signingSecret string
	installations repository.InstallationRepository
	bookmarks     service.BookmarkService
	feedbinDigest *service.FeedbinDigestService
	ai            service.TextSummarizer
	client        slackThreadMessageClient
	timezone      *time.Location
}

type slackThreadMessageClient interface {
	PostMessageInThread(ctx context.Context, token, channel, threadTS, text string) error
}

func NewSlackEventHandler(signingSecret string, installations repository.InstallationRepository, bookmarks service.BookmarkService, feedbinDigest *service.FeedbinDigestService, ai service.TextSummarizer, client slackThreadMessageClient, timezone *time.Location) *SlackEventHandler {
	return &SlackEventHandler{signingSecret: signingSecret, installations: installations, bookmarks: bookmarks, feedbinDigest: feedbinDigest, ai: ai, client: client, timezone: timezone}
}

func (h *SlackEventHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /slack/events", h.handle)
}

type slackEventEnvelope struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	TeamID    string `json:"team_id"`
	Event     struct {
		Type     string `json:"type"`
		User     string `json:"user"`
		Text     string `json:"text"`
		Channel  string `json:"channel"`
		TS       string `json:"ts"`
		ThreadTS string `json:"thread_ts"`
	} `json:"event"`
}

func (h *SlackEventHandler) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read Slack event")
		return
	}
	var envelope slackEventEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeError(w, http.StatusBadRequest, "invalid Slack event payload")
		return
	}
	if envelope.Type == "url_verification" {
		writeJSON(w, http.StatusOK, map[string]string{"challenge": envelope.Challenge})
		return
	}
	if !slack.VerifySignature(h.signingSecret, r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), body) {
		writeError(w, http.StatusUnauthorized, "invalid Slack signature")
		return
	}
	if envelope.Event.Type != "app_mention" {
		w.WriteHeader(http.StatusOK)
		return
	}
	text := strings.ToLower(envelope.Event.Text)
	if !strings.Contains(text, "özetimi ver") && !strings.Contains(text, "rsslerimi ver") && !strings.Contains(text, "rss'lerimi ver") {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Acknowledge immediately; Slack requires event responses within three seconds.
	w.WriteHeader(http.StatusOK)
	go h.replyWithSummary(envelope)
}

func (h *SlackEventHandler) replyWithSummary(event slackEventEnvelope) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	userID, err := h.installations.GetUserIDBySlackUserID(ctx, event.TeamID, event.Event.User)
	if err != nil {
		if err == pgx.ErrNoRows {
			userID, err = h.installations.UpsertUser(ctx, event.TeamID, event.Event.User)
			if err != nil {
				log.Printf("save Slack mention user: %v", err)
				return
			}
		} else {
			log.Printf("find Slack mention user: %v", err)
			return
		}
	}
	installation, err := h.installations.GetByUserID(ctx, userID)
	if err != nil {
		log.Printf("get Slack installation for mention: %v", err)
		return
	}
	now := time.Now()
	from := lastDailyStart(now, h.timezone).UTC()
	threadTS := event.Event.ThreadTS
	if threadTS == "" {
		threadTS = event.Event.TS
	}
	var message string
	if strings.Contains(strings.ToLower(event.Event.Text), "rss") {
		if h.feedbinDigest == nil {
			return
		}
		message, err = h.feedbinDigest.BuildAnalysisInput(ctx, userID, lastFeedbinStart(now, h.timezone))
		if err == nil && h.ai != nil && !strings.Contains(message, "yeni RSS yok") {
			if analyzed, aiErr := h.ai.Summarize(ctx, message); aiErr == nil {
				message = analyzed
			} else {
				log.Printf("Feedbin AI summary failed for Slack mention: %v", aiErr)
			}
		}
	} else {
		bookmarks, listErr := h.bookmarks.ListByDate(ctx, userID, from, now.UTC())
		err = listErr
		if err == nil {
			message = formatThreadSummary(bookmarks)
		}
	}
	if err != nil {
		log.Printf("build Slack mention summary: %v", err)
		return
	}
	if err := h.client.PostMessageInThread(ctx, installation.BotToken, event.Event.Channel, threadTS, message); err != nil {
		log.Printf("send Slack thread summary: %v", err)
	}
}

func lastFeedbinStart(now time.Time, location *time.Location) time.Time {
	localNow := now.In(location)
	start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 0, 0, 0, location)
	if localNow.Before(start) {
		start = start.Add(-24 * time.Hour)
	}
	return start
}

func lastDailyStart(now time.Time, location *time.Location) time.Time {
	localNow := now.In(location)
	start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 22, 0, 0, 0, location)
	if localNow.Before(start) {
		start = start.Add(-24 * time.Hour)
	}
	return start
}

func formatThreadSummary(bookmarks []domain.Bookmark) string {
	if len(bookmarks) == 0 {
		return "📚 *Özetin*\n\nDün 22:00’den beri kaydettiğin bir bookmark yok."
	}
	var builder strings.Builder
	builder.WriteString("📚 *Özetin*\n\n")
	for _, bookmark := range bookmarks {
		builder.WriteString("• *")
		builder.WriteString(strings.ReplaceAll(bookmark.Title, "*", ""))
		builder.WriteString("*\n  🔗 <")
		builder.WriteString(bookmark.URL)
		builder.WriteString("|Bookmark’ı aç>\n")
	}
	return builder.String()
}
