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
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"nudge/internal/analytics"
	"nudge/internal/coding"
	"nudge/internal/repository"
	"nudge/internal/service"
	"nudge/internal/slack"
)

type SlackEventHandler struct {
	signingSecret     string
	installations     repository.InstallationRepository
	bookmarks         service.BookmarkService
	feedbinDigest     *service.FeedbinDigestService
	runtime           service.AgentRuntime
	web               service.WebAssistant
	history           service.ConversationStore
	conversationLocks conversationLocks
	events            eventDeduplicator
	client            slackThreadMessageClient
	timezone          *time.Location
	analytics         repository.AnalyticsRepository
	coding            *CodingHandler
}

func (h *SlackEventHandler) WithCoding(handler *CodingHandler) *SlackEventHandler {
	h.coding = handler
	return h
}

type slackThreadMessageClient interface {
	PostMessageInThread(ctx context.Context, token, channel, threadTS, text string) error
}

type slackReactionClient interface {
	AddReaction(ctx context.Context, token, channel, timestamp, name string) error
	RemoveReaction(ctx context.Context, token, channel, timestamp, name string) error
}

const processingReaction = "nudgythinking"

func NewSlackEventHandler(signingSecret string, installations repository.InstallationRepository, bookmarks service.BookmarkService, feedbinDigest *service.FeedbinDigestService, runtime service.AgentRuntime, web service.WebAssistant, history service.ConversationStore, client slackThreadMessageClient, timezone *time.Location, analytics ...repository.AnalyticsRepository) *SlackEventHandler {
	var analyticsRepository repository.AnalyticsRepository
	if len(analytics) > 0 {
		analyticsRepository = analytics[0]
	}
	return &SlackEventHandler{signingSecret: signingSecret, installations: installations, bookmarks: bookmarks, feedbinDigest: feedbinDigest, runtime: runtime, web: web, history: history, client: client, timezone: timezone, analytics: analyticsRepository}
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
	if !slack.VerifySignature(h.signingSecret, r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), body) {
		writeError(w, http.StatusUnauthorized, "invalid Slack signature")
		return
	}
	if envelope.Type == "url_verification" {
		writeJSON(w, http.StatusOK, map[string]string{"challenge": envelope.Challenge})
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
	tracker := analytics.NewUsageTracker()
	operations := &analytics.OperationTracker{}
	analyticsRecord := repository.InteractionRecord{
		RequestID: requestID, TeamID: event.TeamID, ChannelID: event.Event.Channel,
		ThreadTS: threadTS, SlackUserID: event.Event.User,
		RequestText: strings.TrimSpace(slackUserMention.ReplaceAllString(event.Event.Text, "")),
		Status:      "processing", StartedAt: started,
	}
	defer func() {
		if h.analytics == nil {
			return
		}
		analyticsRecord.LatencyMS = time.Since(started).Milliseconds()
		analyticsRecord.Usage = tracker.Snapshot()
		analyticsRecord.Operations = operations.Snapshot()
		if analyticsRecord.Status == "processing" {
			analyticsRecord.Status = "failed"
		}
		persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.analytics.RecordInteraction(persistCtx, analyticsRecord); err != nil {
			log.Printf("slack_request=%s stage=analytics_save_failed error=%q", requestID, err)
		}
	}()
	key := service.ConversationKey{Team: event.TeamID, Channel: event.Event.Channel, Thread: threadTS, User: event.Event.User}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	ctx = analytics.WithUsageTracker(ctx, tracker)
	ctx = analytics.WithOperationTracker(ctx, operations)
	userID, err := h.installations.GetUserIDBySlackUserID(ctx, event.TeamID, event.Event.User)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
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
	analyticsRecord.UserID = userID
	log.Printf("slack_request=%s stage=user_resolved user_id=%d slack_user_id=%q team_id=%q elapsed_ms=%d", requestID, userID, event.Event.User, event.TeamID, time.Since(started).Milliseconds())
	installation, err := h.installations.GetByUserID(ctx, userID)
	if err != nil {
		log.Printf("slack_request=%s stage=installation_lookup_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
		return
	}
	log.Printf("slack_request=%s stage=installation_loaded elapsed_ms=%d", requestID, time.Since(started).Milliseconds())
	var removeProcessingReaction func()
	if reactionClient, ok := h.client.(slackReactionClient); ok && event.Event.TS != "" {
		reactionCtx, reactionCancel := context.WithTimeout(ctx, 3*time.Second)
		err := reactionClient.AddReaction(reactionCtx, installation.BotToken, event.Event.Channel, event.Event.TS, processingReaction)
		reactionCancel()
		if err != nil {
			log.Printf("slack_request=%s stage=processing_reaction_failed error=%q", requestID, err)
		} else {
			log.Printf("slack_request=%s stage=processing_reaction_added", requestID)
			removeProcessingReaction = func() {
				removeCtx, removeCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer removeCancel()
				if err := reactionClient.RemoveReaction(removeCtx, installation.BotToken, event.Event.Channel, event.Event.TS, processingReaction); err != nil {
					log.Printf("slack_request=%s stage=processing_reaction_remove_failed error=%q", requestID, err)
				} else {
					log.Printf("slack_request=%s stage=processing_reaction_removed", requestID)
				}
			}
			defer func() {
				if removeProcessingReaction != nil {
					removeProcessingReaction()
				}
			}()
		}
	}
	// Resolve credentials and show activity before waiting for this conversation.
	lockStarted := time.Now()
	lockCtx, lockCancel := context.WithTimeout(ctx, 15*time.Second)
	unlock, lockErr := h.conversationLocks.lock(lockCtx, key)
	lockCancel()
	var state service.ConversationState
	if lockErr != nil {
		err = lockErr
		log.Printf("slack_request=%s stage=thread_lock_failed wait_ms=%d error=%q", requestID, time.Since(lockStarted).Milliseconds(), err)
	} else {
		defer unlock()
		log.Printf("slack_request=%s stage=thread_lock_acquired wait_ms=%d", requestID, time.Since(lockStarted).Milliseconds())
		err = fmt.Errorf("conversation history unavailable")
		if h.history != nil {
			state, err = h.history.Load(ctx, key)
		}
	}
	historyLoaded := err == nil
	var result service.AgentResult
	localReply := localSocialReply(analyticsRecord.RequestText)
	if historyLoaded {
		log.Printf("slack_request=%s stage=history_loaded turns=%d prior_articles=%d elapsed_ms=%d", requestID, len(state.Turns), len(state.Articles), time.Since(started).Milliseconds())
		if localReply != "" {
			result.Text = localReply
			log.Printf("slack_request=%s stage=local_social_reply model_calls=0", requestID)
		} else if h.runtime == nil {
			err = fmt.Errorf("agent runtime unavailable")
		} else {
			// Dynamic tools belong to a native thread at creation. Re-bootstrap
			// delivered history once when coding tools are enabled or disabled.
			toolVersion := ""
			if h.coding != nil {
				toolVersion = "coding-connect-v4"
			}
			if state.AgentToolVersion != toolVersion {
				state.AgentThreadID = ""
				state.AgentTokenUsage = nil
				state.AgentToolVersion = toolVersion
			}
			// Detach mutable history so a failed run cannot change delivered state.
			history := state
			if state.AgentTokenUsage != nil {
				usage := *state.AgentTokenUsage
				history.AgentTokenUsage = &usage
			}
			history.Turns = append([]service.ConversationExchange(nil), state.Turns...)
			history.Articles = append([]service.ConversationArticle(nil), state.Articles...)
			if state.RSS != nil {
				progress := *state.RSS
				progress.ShownKeys = append([]string(nil), state.RSS.ShownKeys...)
				history.RSS = &progress
			}
			location := h.timezone
			if location == nil {
				location = time.UTC
			}
			now := time.Now().In(location)
			input := fmt.Sprintf("Current time: %s\nTimezone: %s\n\nSlack message:\n%s", now.Format(time.RFC3339), location.String(), analyticsRecord.RequestText)
			tools := service.NewAgentTools(h.feedbinDigest, h.bookmarks, h.web, userID, now, location)
			if h.coding != nil {
				eventID := event.EventID
				if eventID == "" {
					eventID = event.Event.TS
				}
				tools = h.coding.Tools(tools, coding.Identity{UserID: userID, Team: event.TeamID, Channel: event.Event.Channel, Thread: threadTS, Event: eventID, Request: analyticsRecord.RequestText})
			}
			log.Printf("slack_request=%s stage=agent_started", requestID)
			result, err = h.runtime.Run(ctx, service.AgentRequest{
				Key: key, ThreadID: state.AgentThreadID, Input: input, History: history,
				Tools: tools,
			})
			if err == nil && (strings.TrimSpace(result.Text) == "" || utf8.RuneCountInString(result.Text) > 35000) {
				err = fmt.Errorf("agent returned empty or oversized output")
			}
		}
	} else {
		log.Printf("slack_request=%s stage=history_load_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
	}
	success := err == nil
	message := agentFailureMessage
	if success {
		message = result.Text
	}
	analyticsRecord.ResponseText = message
	analyticsRecord.Action = "AGENT"
	if localReply != "" {
		analyticsRecord.Action = "SOCIAL_REPLY"
	}
	if err != nil {
		log.Printf("slack_request=%s stage=agent_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
		if historyLoaded {
			state.AgentThreadID = ""
			state.AgentTokenUsage = nil
			h.saveConversation(key, state, requestID)
		}
	} else {
		log.Printf("slack_request=%s stage=response_ready chars=%d elapsed_ms=%d", requestID, utf8.RuneCountInString(message), time.Since(started).Milliseconds())
	}
	// Generation timeout must not prevent a clear error response being delivered.
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer sendCancel()
	log.Printf("slack_request=%s stage=slack_send_started elapsed_ms=%d", requestID, time.Since(started).Milliseconds())
	if err := h.client.PostMessageInThread(sendCtx, installation.BotToken, event.Event.Channel, threadTS, message); err != nil {
		analyticsRecord.Status = "failed"
		log.Printf("slack_request=%s stage=slack_send_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
		if success && localReply == "" {
			state.AgentThreadID = ""
			state.AgentTokenUsage = nil
			h.saveConversation(key, state, requestID)
		}
		return
	}
	if success {
		analyticsRecord.Status = "success"
	} else {
		analyticsRecord.Status = "degraded"
	}
	log.Printf("slack_request=%s stage=slack_send_complete elapsed_ms=%d", requestID, time.Since(started).Milliseconds())
	if removeProcessingReaction != nil {
		removeProcessingReaction()
		removeProcessingReaction = nil
	}
	// Courtesy exchanges are recorded in analytics, not model-bound history.
	// Preserve the native thread and its cumulative usage counters unchanged.
	if success && localReply == "" {
		state.AgentThreadID = result.ThreadID
		state.AgentTokenUsage = result.TokenUsage
		state.Remember(event.Event.Text, message, "AGENT")
		h.saveConversation(key, state, requestID)
	}
	log.Printf("slack_request=%s stage=done success=%t total_ms=%d", requestID, success, time.Since(started).Milliseconds())
}

const agentFailureMessage = "Yanıtını şu anda tamamlayamadım. Biraz sonra tekrar deneyebilir misin?"

// Saving delivered state or invalidating a runtime thread must survive a run/send
// timeout: Codex may already contain output that Slack never showed.
func (h *SlackEventHandler) saveConversation(key service.ConversationKey, state service.ConversationState, requestID string) {
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.history.Save(persistCtx, key, state); err != nil {
		log.Printf("slack_request=%s stage=history_save_failed error=%q", requestID, err)
	} else {
		log.Printf("slack_request=%s stage=history_saved", requestID)
	}
}

func slackRequestID(event slackEventEnvelope) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(event.TeamID + "\x00" + event.Event.Channel + "\x00" + event.Event.ThreadTS + "\x00" + event.Event.User + "\x00" + event.Event.TS))
	return fmt.Sprintf("%08x", hash.Sum32())
}

var slackUserMention = regexp.MustCompile(`<@[A-Z0-9]+>`)
