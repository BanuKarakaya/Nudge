package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"nudge/internal/ai"
	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/service"
	"nudge/internal/slack"
)

type SlackEventHandler struct {
	signingSecret     string
	installations     repository.InstallationRepository
	bookmarks         service.BookmarkService
	feedbinDigest     *service.FeedbinDigestService
	ai                service.TextSummarizer
	recommender       service.RSSRecommender
	dialogue          service.ConversationAssistant
	web               service.WebAssistant
	history           service.ConversationStore
	conversationLocks [64]sync.Mutex
	client            slackThreadMessageClient
	timezone          *time.Location
}

type slackThreadMessageClient interface {
	PostMessageInThread(ctx context.Context, token, channel, threadTS, text string) error
}

func NewSlackEventHandler(signingSecret string, installations repository.InstallationRepository, bookmarks service.BookmarkService, feedbinDigest *service.FeedbinDigestService, ai service.TextSummarizer, recommender service.RSSRecommender, dialogue service.ConversationAssistant, web service.WebAssistant, history service.ConversationStore, client slackThreadMessageClient, timezone *time.Location) *SlackEventHandler {
	return &SlackEventHandler{signingSecret: signingSecret, installations: installations, bookmarks: bookmarks, feedbinDigest: feedbinDigest, ai: ai, recommender: recommender, dialogue: dialogue, web: web, history: history, client: client, timezone: timezone}
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
	// Acknowledge immediately; Slack requires event responses within three seconds.
	w.WriteHeader(http.StatusOK)
	go h.replyWithSummary(envelope)
}

func (h *SlackEventHandler) replyWithSummary(event slackEventEnvelope) {
	threadTS := event.Event.ThreadTS
	if threadTS == "" {
		threadTS = event.Event.TS
	}
	key := service.ConversationKey{Team: event.TeamID, Channel: event.Event.Channel, Thread: threadTS, User: event.Event.User}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(key.Team + "\x00" + key.Channel + "\x00" + key.Thread + "\x00" + key.User))
	lock := &h.conversationLocks[hash.Sum32()%uint32(len(h.conversationLocks))]
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
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
	state, err := h.history.Load(ctx, key)
	message := "Konuşma geçmişine şu anda erişemedim. Biraz sonra tekrar deneyebilir misin?"
	action := ""
	if err == nil {
		message, action, err = h.conversationReply(ctx, state, event.Event.Text, userID, now)
	}
	success := err == nil
	if err != nil {
		log.Printf("Slack conversation response failed: %v", err)
	}
	// Generation timeout must not prevent a clear error response being delivered.
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer sendCancel()
	if err := h.client.PostMessageInThread(sendCtx, installation.BotToken, event.Event.Channel, threadTS, message); err != nil {
		log.Printf("send Slack thread summary: %v", err)
		return
	}
	if success {
		state.Remember(event.Event.Text, message, action)
		if err := h.history.Save(sendCtx, key, state); err != nil {
			log.Printf("save Slack conversation: %v", err)
		}
	}
}

func (h *SlackEventHandler) conversationReply(ctx context.Context, state service.ConversationState, request string, userID int64, now time.Time) (string, string, error) {
	failure := "Yanıtını şu anda tamamlayamadım. Biraz sonra tekrar deneyebilir misin?"
	if h.dialogue == nil {
		return failure, "", fmt.Errorf("conversation assistant unavailable")
	}
	request = strings.TrimSpace(slackUserMention.ReplaceAllString(request, ""))
	decision, err := h.dialogue.Converse(ctx, state, request)
	if err != nil {
		var providerErr *ai.ProviderError
		if errors.As(err, &providerErr) && providerErr.Status == http.StatusTooManyRequests {
			failure = "AI hizmetinin kullanım sınırına takıldım. Biraz sonra tekrar deneyebilir misin?"
		}
		return failure, "", fmt.Errorf("conversation decision: %w", err)
	}
	if err = decision.Validate(state); err != nil {
		return failure, "", err
	}
	var message string
	switch decision.Action {
	case service.ConversationWebSearch:
		if h.web == nil {
			err = fmt.Errorf("web search unavailable")
		} else {
			message, err = h.web.SearchWeb(ctx, decision.SearchRequest)
		}
		if err != nil {
			return "İnternet aramasını şu anda tamamlayamadım. Biraz sonra tekrar deneyebilir misin? RSS listene dönmedim; web kaynağını doğrulamadan bağlantı önermeyeceğim.", decision.Action, err
		}
	case service.ConversationReply:
		message = decision.Reply
	case service.ConversationSelect:
		message, err = state.RenderSelection(decision)
	case service.ConversationRSSSummary:
		message, err = h.feedbinDigest.BuildSummary(ctx, userID, lastFeedbinStart(now, h.timezone), h.ai)
	case service.ConversationRSSTitles:
		message, err = h.feedbinDigest.BuildDigest(ctx, userID, lastFeedbinStart(now, h.timezone))
	case service.ConversationRSSStats:
		message, err = h.feedbinDigest.BuildUnreadStats(ctx, userID)
	case service.ConversationRSSTodayStats:
		message, err = h.feedbinDigest.BuildTodayStats(ctx, userID, lastFeedbinStart(now, h.timezone), now)
	case service.ConversationRSSCategories:
		message, err = h.feedbinDigest.BuildCategoryStats(ctx, userID)
	case service.ConversationRSSRecommend:
		message, err = h.feedbinDigest.BuildRecommendation(ctx, userID, lastFeedbinStart(now, h.timezone), h.recommender, request)
	case service.ConversationBookmarks:
		bookmarks, listErr := h.bookmarks.ListByDate(ctx, userID, lastDailyStart(now, h.timezone).UTC(), now.UTC())
		err = listErr
		if err == nil {
			message = formatThreadSummary(bookmarks)
		}
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) && strings.HasPrefix(decision.Action, "RSS_") {
			failure = "Feedbin hesabın henüz bağlı görünmüyor. Nudge'dan gelen Feedbin bağlantı mesajıyla hesabını bağladıktan sonra tekrar deneyebilirsin."
		}
		return failure, decision.Action, fmt.Errorf("execute %s: %w", decision.Action, err)
	}
	return message, decision.Action, nil
}

var slackUserMention = regexp.MustCompile(`<@[A-Z0-9]+>`)

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
		return "📚 *Bookmarkların Beybi*\n\nDün 22:00’den beri kaydettiğin bir bookmark yok."
	}
	var builder strings.Builder
	builder.WriteString("📚 *Bookmarkların Beybi*\n\n")
	for _, bookmark := range bookmarks {
		builder.WriteString("• *")
		builder.WriteString(strings.ReplaceAll(bookmark.Title, "*", ""))
		builder.WriteString("*\n  🔗 <")
		builder.WriteString(bookmark.URL)
		builder.WriteString("|Bookmark’ı aç>\n")
	}
	return builder.String()
}
