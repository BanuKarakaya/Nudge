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
	conversationLocks conversationLocks
	events            eventDeduplicator
	client            slackThreadMessageClient
	timezone          *time.Location
}

type slackThreadMessageClient interface {
	PostMessageInThread(ctx context.Context, token, channel, threadTS, text string) error
}

type slackReactionClient interface {
	AddReaction(ctx context.Context, token, channel, timestamp, name string) error
	RemoveReaction(ctx context.Context, token, channel, timestamp, name string) error
}

const processingReaction = "nudgethinking"

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
	EventID   string `json:"event_id"`
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
	log.Printf("slack_request=%s stage=received", slackRequestID(envelope))
	w.WriteHeader(http.StatusOK)
	log.Printf("slack_request=%s stage=acknowledged", slackRequestID(envelope))
	go h.replyWithSummary(envelope)
}

func (h *SlackEventHandler) replyWithSummary(event slackEventEnvelope) {
	requestID := slackRequestID(event)
	finish, fresh := h.events.begin(event, time.Now())
	if !fresh {
		log.Printf("slack_request=%s stage=duplicate_ignored", requestID)
		return
	}
	defer finish()
	started := time.Now()
	log.Printf("slack_request=%s stage=processing_started", requestID)
	threadTS := event.Event.ThreadTS
	if threadTS == "" {
		threadTS = event.Event.TS
	}
	key := service.ConversationKey{Team: event.TeamID, Channel: event.Event.Channel, Thread: threadTS, User: event.Event.User}
	lockStarted := time.Now()
	unlock := h.conversationLocks.lock(key)
	defer unlock()
	log.Printf("slack_request=%s stage=thread_lock_acquired wait_ms=%d", requestID, time.Since(lockStarted).Milliseconds())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	userID, err := h.installations.GetUserIDBySlackUserID(ctx, event.TeamID, event.Event.User)
	if err != nil {
		if err == pgx.ErrNoRows {
			userID, err = h.installations.UpsertUser(ctx, event.TeamID, event.Event.User)
			if err != nil {
				log.Printf("slack_request=%s stage=user_create_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
				return
			}
		} else {
			log.Printf("slack_request=%s stage=user_lookup_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
			return
		}
	}
	log.Printf("slack_request=%s stage=user_resolved elapsed_ms=%d", requestID, time.Since(started).Milliseconds())
	installation, err := h.installations.GetByUserID(ctx, userID)
	if err != nil {
		log.Printf("slack_request=%s stage=installation_lookup_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
		return
	}
	log.Printf("slack_request=%s stage=installation_loaded elapsed_ms=%d", requestID, time.Since(started).Milliseconds())
	var removeProcessing func()
	if reactionClient, ok := h.client.(slackReactionClient); ok && event.Event.TS != "" {
		reactionCtx, reactionCancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := reactionClient.AddReaction(reactionCtx, installation.BotToken, event.Event.Channel, event.Event.TS, processingReaction)
		reactionCancel()
		if err != nil {
			log.Printf("slack_request=%s stage=processing_reaction_failed error=%q", requestID, err)
		} else {
			log.Printf("slack_request=%s stage=processing_reaction_added", requestID)
			removeProcessing = func() {
				removeCtx, removeCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer removeCancel()
				if err := reactionClient.RemoveReaction(removeCtx, installation.BotToken, event.Event.Channel, event.Event.TS, processingReaction); err != nil {
					log.Printf("slack_request=%s stage=processing_reaction_remove_failed error=%q", requestID, err)
				} else {
					log.Printf("slack_request=%s stage=processing_reaction_removed", requestID)
				}
			}
			defer func() { removeProcessing() }()
		}
	}
	now := time.Now()
	state, err := h.history.Load(ctx, key)
	message := "Konuşma geçmişine şu anda erişemedim. Biraz sonra tekrar deneyebilir misin?"
	action := ""
	if err == nil {
		log.Printf("slack_request=%s stage=history_loaded turns=%d prior_articles=%d elapsed_ms=%d", requestID, len(state.Turns), len(state.Articles), time.Since(started).Milliseconds())
		message, action, err = h.conversationReplyWithState(ctx, &state, event.Event.Text, userID, now, requestID)
	} else {
		log.Printf("slack_request=%s stage=history_load_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
	}
	success := err == nil
	if err != nil {
		log.Printf("slack_request=%s stage=conversation_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
	} else {
		log.Printf("slack_request=%s stage=response_ready action=%s chars=%d elapsed_ms=%d", requestID, action, len(message), time.Since(started).Milliseconds())
	}
	// Generation timeout must not prevent a clear error response being delivered.
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer sendCancel()
	log.Printf("slack_request=%s stage=slack_send_started elapsed_ms=%d", requestID, time.Since(started).Milliseconds())
	if err := h.client.PostMessageInThread(sendCtx, installation.BotToken, event.Event.Channel, threadTS, message); err != nil {
		log.Printf("slack_request=%s stage=slack_send_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
		return
	}
	log.Printf("slack_request=%s stage=slack_send_complete elapsed_ms=%d", requestID, time.Since(started).Milliseconds())
	if success {
		state.Remember(event.Event.Text, message, action)
		if err := h.history.Save(sendCtx, key, state); err != nil {
			log.Printf("slack_request=%s stage=history_save_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
		} else {
			log.Printf("slack_request=%s stage=history_saved elapsed_ms=%d", requestID, time.Since(started).Milliseconds())
		}
	}
	log.Printf("slack_request=%s stage=done success=%t total_ms=%d", requestID, success, time.Since(started).Milliseconds())
}

func (h *SlackEventHandler) conversationReply(ctx context.Context, state service.ConversationState, request string, userID int64, now time.Time) (string, string, error) {
	return h.conversationReplyWithState(ctx, &state, request, userID, now, "local")
}

func (h *SlackEventHandler) conversationReplyWithState(ctx context.Context, state *service.ConversationState, request string, userID int64, now time.Time, requestID string) (string, string, error) {
	failure := "Yanıtını şu anda tamamlayamadım. Biraz sonra tekrar deneyebilir misin?"
	if h.dialogue == nil {
		return failure, "", fmt.Errorf("conversation assistant unavailable")
	}
	request = strings.TrimSpace(slackUserMention.ReplaceAllString(request, ""))
	modelStarted := time.Now()
	log.Printf("slack_request=%s stage=model_started", requestID)
	decision, err := h.dialogue.Converse(ctx, *state, request)
	if err != nil {
		log.Printf("slack_request=%s stage=model_failed duration_ms=%d error=%q", requestID, time.Since(modelStarted).Milliseconds(), err)
		var providerErr *ai.ProviderError
		if errors.As(err, &providerErr) && providerErr.Status == http.StatusTooManyRequests {
			failure = "AI hizmetinin kullanım sınırına takıldım. Biraz sonra tekrar deneyebilir misin?"
		}
		return failure, "", fmt.Errorf("conversation decision: %w", err)
	}
	log.Printf("slack_request=%s stage=model_complete duration_ms=%d action=%s", requestID, time.Since(modelStarted).Milliseconds(), decision.Action)
	if err = decision.Validate(*state); err != nil {
		return failure, "", err
	}
	var message string
	operationStarted := time.Now()
	if decision.Action != service.ConversationReply && decision.Action != service.ConversationSelect {
		log.Printf("slack_request=%s stage=operation_started action=%s", requestID, decision.Action)
	}
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
		progress := service.RSSSummaryProgress{Scope: "today", Since: lastFeedbinStart(now, h.timezone)}
		if decision.RSSContinue && state.RSS != nil {
			progress = *state.RSS
		}
		if decision.RSSScope != "" {
			progress.Scope = decision.RSSScope
			if state.RSS != nil && state.RSS.Scope != decision.RSSScope {
				// A new scope starts a fresh inventory, so entries from the prior
				// scope must not be silently skipped.
				progress.ShownKeys = nil
				progress.Total = 0
			}
		}
		if progress.Scope == "all_unread" {
			progress.Since = time.Time{}
		}
		if progress.Scope == "today" && (progress.Since.IsZero() || (state.RSS != nil && state.RSS.Scope != progress.Scope)) {
			progress.Since = lastFeedbinStart(now, h.timezone)
		}
		if decision.RSSContinue && state.RSS == nil {
			// Upgrade threads created before paginated summaries existed.
			for _, article := range state.Articles {
				if article.URL != "" {
					progress.ShownKeys = append(progress.ShownKeys, "url:"+article.URL)
				}
			}
		}
		var next service.RSSSummaryProgress
		message, next, err = h.feedbinDigest.BuildSummaryPage(ctx, userID, progress, selectedSummarizer(h.ai, decision.NeedsDeepSeek))
		if err == nil {
			state.RSS = &next
		}
	case service.ConversationRSSTitles:
		message, err = h.feedbinDigest.BuildDigest(ctx, userID, lastFeedbinStart(now, h.timezone))
	case service.ConversationRSSStats:
		message, err = h.feedbinDigest.BuildUnreadStats(ctx, userID)
	case service.ConversationRSSTodayStats:
		message, err = h.feedbinDigest.BuildTodayStats(ctx, userID, lastFeedbinStart(now, h.timezone), now)
	case service.ConversationRSSCategories:
		message, err = h.feedbinDigest.BuildCategoryStats(ctx, userID)
	case service.ConversationRSSRecommend:
		message, err = h.feedbinDigest.BuildRecommendation(ctx, userID, lastFeedbinStart(now, h.timezone), selectedRecommender(h.recommender, decision.NeedsDeepSeek), request)
	case service.ConversationBookmarks:
		bookmarks, listErr := h.bookmarks.ListByDate(ctx, userID, lastDailyStart(now, h.timezone).UTC(), now.UTC())
		err = listErr
		if err == nil {
			message = formatThreadSummary(bookmarks)
		}
	}
	if err != nil {
		if decision.Action != service.ConversationReply && decision.Action != service.ConversationSelect {
			log.Printf("slack_request=%s stage=operation_failed action=%s duration_ms=%d error=%q", requestID, decision.Action, time.Since(operationStarted).Milliseconds(), err)
		}
		if errors.Is(err, pgx.ErrNoRows) && strings.HasPrefix(decision.Action, "RSS_") {
			failure = "Feedbin hesabın henüz bağlı görünmüyor. Nudge'dan gelen Feedbin bağlantı mesajıyla hesabını bağladıktan sonra tekrar deneyebilirsin."
		}
		return failure, decision.Action, fmt.Errorf("execute %s: %w", decision.Action, err)
	}
	if decision.Action != service.ConversationReply && decision.Action != service.ConversationSelect {
		log.Printf("slack_request=%s stage=operation_complete action=%s duration_ms=%d", requestID, decision.Action, time.Since(operationStarted).Milliseconds())
	}
	return message, decision.Action, nil
}

type complexitySummarizerSelector interface {
	SummarizerFor(bool) service.TextSummarizer
}

type complexityRecommenderSelector interface {
	RecommenderFor(bool) service.RSSRecommender
}

func selectedSummarizer(summarizer service.TextSummarizer, complex bool) service.TextSummarizer {
	if selector, ok := summarizer.(complexitySummarizerSelector); ok {
		return selector.SummarizerFor(complex)
	}
	return summarizer
}

func selectedRecommender(recommender service.RSSRecommender, complex bool) service.RSSRecommender {
	if selector, ok := recommender.(complexityRecommenderSelector); ok {
		return selector.RecommenderFor(complex)
	}
	return recommender
}

func slackRequestID(event slackEventEnvelope) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(event.TeamID + "\x00" + event.Event.Channel + "\x00" + event.Event.ThreadTS + "\x00" + event.Event.User + "\x00" + event.Event.TS))
	return fmt.Sprintf("%08x", hash.Sum32())
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
