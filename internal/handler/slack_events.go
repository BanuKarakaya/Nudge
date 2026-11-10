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
	worktracker "nudge/internal/tracker"
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
	jobs              repository.SlackJobRepository
	gitlab            *CodingHandler
	trackers          []*TrackerHandler
}

func (h *SlackEventHandler) WithCoding(handler *CodingHandler) *SlackEventHandler {
	h.coding = handler
	h.linkMergePeers()
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
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
	if envelope.TeamID == "" || envelope.Event.Channel == "" || envelope.Event.User == "" || envelope.Event.TS == "" {
		writeError(w, http.StatusBadRequest, "invalid Slack event identity")
		return
	}
	if h.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "Slack job queue unavailable")
		return
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid Slack event")
		return
	}
	// Persist before acknowledging; a failure must let Slack retry delivery.
	enqueueCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	err = h.jobs.Enqueue(enqueueCtx, repository.SlackJob{
		Key: eventConversationKey(envelope), EventKey: slackEventKey(envelope), Payload: payload,
	})
	if err != nil {
		log.Printf("slack_request=%s stage=enqueue_failed", slackRequestID(envelope))
		writeError(w, http.StatusServiceUnavailable, "could not persist Slack event")
		return
	}
	w.WriteHeader(http.StatusOK)
	log.Printf("slack_request=%s stage=queued", slackRequestID(envelope))
}

func (h *SlackEventHandler) replyWithSummary(event slackEventEnvelope) {
	requestID := slackRequestID(event)
	finish, fresh := h.events.begin(event, time.Now())
	if !fresh {
		log.Printf("slack_request=%s stage=duplicate_ignored", requestID)
		return
	}
	defer finish()
	_ = h.processSlackEvent(context.Background(), event, nil)
}

func (h *SlackEventHandler) processSlackEvent(parent context.Context, event slackEventEnvelope, job *repository.SlackJob) error {
	requestID := slackRequestID(event)
	if job != nil {
		requestID = fmt.Sprintf("slack-job-%d", job.ID)
	}
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
		if h.analytics == nil || job != nil {
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
	ctx, cancel := context.WithTimeout(parent, 150*time.Second)
	defer cancel()
	ctx = analytics.WithUsageTracker(ctx, tracker)
	ctx = analytics.WithOperationTracker(ctx, operations)
	userID, err := h.installations.GetUserIDBySlackUserID(ctx, event.TeamID, event.Event.User)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			userID, err = h.installations.UpsertUser(ctx, event.TeamID, event.Event.User)
			if err != nil {
				log.Printf("slack_request=%s stage=user_create_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
				return err
			}
		} else {
			log.Printf("slack_request=%s stage=user_lookup_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
			return err
		}
	}
	analyticsRecord.UserID = userID
	log.Printf("slack_request=%s stage=user_resolved user_id=%d slack_user_id=%q team_id=%q elapsed_ms=%d", requestID, userID, event.Event.User, event.TeamID, time.Since(started).Milliseconds())
	installation, err := h.installations.GetByUserID(ctx, userID)
	if err != nil {
		log.Printf("slack_request=%s stage=installation_lookup_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
		return err
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
	if job != nil && err != nil {
		return err
	}
	// A crashed attempt may have advanced its native thread without delivering
	// anything. Rebuild from delivered history and rediscover persisted edits.
	if job != nil && job.Attempts > 1 {
		state.AgentThreadID = ""
		state.AgentTokenUsage = nil
	}
	historyLoaded := err == nil
	var result service.AgentResult
	var codingReply *codingReply
	localReply := localSocialReply(analyticsRecord.RequestText)
	connectProvider := connectionProvider(analyticsRecord.RequestText)
	connectionOK := true
	if historyLoaded {
		log.Printf("slack_request=%s stage=history_loaded turns=%d prior_articles=%d elapsed_ms=%d", requestID, len(state.Turns), len(state.Articles), time.Since(started).Milliseconds())
		if connectProvider != "" {
			localReply, connectionOK = h.connectionReply(ctx, connectProvider, coding.Identity{UserID: userID, Team: event.TeamID, Channel: event.Event.Channel, Thread: threadTS, Event: slackEventKey(event), Request: analyticsRecord.RequestText}, job)
		}
		if localReply != "" {
			result.Text = localReply
			log.Printf("slack_request=%s stage=local_reply model_calls=0", requestID)
		} else if h.runtime == nil {
			err = fmt.Errorf("agent runtime unavailable")
		} else {
			// Dynamic tools belong to a native thread at creation. Re-bootstrap
			// delivered history once when coding tools are enabled or disabled.
			toolVersion := ""
			if h.coding != nil {
				toolVersion = "coding-semantic-merge-v7"
			}
			if h.gitlab != nil {
				toolVersion += "+gitlab-semantic-merge-v5"
			}
			for _, integration := range h.trackers {
				toolVersion += "+" + integration.service.Kind() + "-v1"
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
			threadContext := h.threadContext(ctx, installation.BotToken, event)
			input := fmt.Sprintf("Current time: %s\nTimezone: %s%s\n\nSlack message:\n%s", now.Format(time.RFC3339), location.String(), threadContext, analyticsRecord.RequestText)
			tools := service.NewAgentTools(h.feedbinDigest, h.bookmarks, h.web, userID, now, location)
			eventID := event.EventID
			if eventID == "" {
				eventID = event.Event.TS
			}
			identity := coding.Identity{UserID: userID, Team: event.TeamID, Channel: event.Event.Channel, Thread: threadTS, Event: eventID, Request: analyticsRecord.RequestText}
			if h.coding != nil {
				tools, codingReply = h.coding.ToolsForReply(tools, identity)
			}
			if h.gitlab != nil {
				tools, codingReply = h.gitlab.ToolsForReply(tools, identity, codingReply)
			}
			var runTools service.AgentTools = tools
			trackerRequestID := ""
			if eventID != "" {
				trackerRequestID = event.TeamID + ":" + event.Event.Channel + ":" + eventID
			}
			for _, integration := range h.trackers {
				runTools = integration.Tools(runTools, worktracker.Identity{UserID: userID, Team: event.TeamID}, trackerRequestID)
			}
			if resolver, ok := h.runtime.(service.MergeIntentResolver); ok {
				runTools = service.WithMergeIntentTools(runTools, resolver, key, identity, history)
			}
			if job != nil {
				runTools = leasedAgentTools{AgentTools: runTools, jobs: h.jobs, job: *job}
				if job.Attempts > 1 {
					input += "\n\nHost recovery: this same Slack request was interrupted. Discover existing repository tasks and inspect their status and saved edits before continuing. Reuse the existing task for this request; completed branch publishing must not be repeated."
				}
			}
			log.Printf("slack_request=%s stage=agent_started", requestID)
			result, err = h.runtime.Run(ctx, service.AgentRequest{
				Key: key, ThreadID: state.AgentThreadID, Input: input, History: history,
				Tools: runTools,
			})
			if err == nil && (strings.TrimSpace(result.Text) == "" || utf8.RuneCountInString(result.Text) > 35000) {
				err = fmt.Errorf("agent returned empty or oversized output")
			}
		}
	} else {
		log.Printf("slack_request=%s stage=history_load_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
	}
	success := err == nil && connectionOK
	message := agentFailureMessage
	if err == nil {
		message = result.Text
	}
	analyticsRecord.ResponseText = message
	analyticsRecord.Action = "AGENT"
	if localReply != "" {
		analyticsRecord.Action = "SOCIAL_REPLY"
	}
	if connectProvider != "" {
		analyticsRecord.Action = "CONNECTION_LINK"
	}
	if err != nil {
		log.Printf("slack_request=%s stage=agent_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
		if historyLoaded {
			state.AgentThreadID = ""
			state.AgentTokenUsage = nil
			if job == nil {
				h.saveConversation(key, state, requestID)
			}
		}
	} else {
		log.Printf("slack_request=%s stage=response_ready chars=%d elapsed_ms=%d", requestID, utf8.RuneCountInString(message), time.Since(started).Milliseconds())
	}
	if job != nil {
		if parent.Err() != nil {
			return parent.Err()
		}
		prepared := queuedReply{Version: 1, Message: message, Success: success, Record: analyticsRecord}
		prepared.Record.LatencyMS = time.Since(started).Milliseconds()
		prepared.Record.Usage = tracker.Snapshot()
		prepared.Record.Operations = operations.Snapshot()
		prepared.Record.Status = "degraded"
		if success {
			prepared.Record.Status = "success"
		}
		if codingReply != nil {
			codingReply.mu.Lock()
			for _, review := range codingReply.reviews {
				prepared.Reviews = append(prepared.Reviews, review)
			}
			codingReply.mu.Unlock()
		}
		if success && localReply == "" {
			state.AgentThreadID = result.ThreadID
			state.AgentTokenUsage = result.TokenUsage
			state.Remember(event.Event.Text, message, "AGENT")
			prepared.State = &state
		} else if !success && historyLoaded {
			prepared.State = &state
		}
		raw, marshalErr := json.Marshal(prepared)
		if marshalErr != nil {
			return marshalErr
		}
		persistCtx, persistCancel := context.WithTimeout(parent, 5*time.Second)
		persistErr := h.jobs.SaveResult(persistCtx, *job, raw)
		persistCancel()
		if persistErr != nil {
			return persistErr
		}
		job.Result = raw
		return h.deliverQueuedReply(parent, event, *job, prepared)
	}
	// Generation timeout must not prevent a clear error response being delivered.
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer sendCancel()
	log.Printf("slack_request=%s stage=slack_send_started elapsed_ms=%d", requestID, time.Since(started).Milliseconds())
	if err := h.postReply(sendCtx, installation.BotToken, event.Event.Channel, threadTS, message, codingReply); err != nil {
		analyticsRecord.Status = "failed"
		log.Printf("slack_request=%s stage=slack_send_failed elapsed_ms=%d error=%q", requestID, time.Since(started).Milliseconds(), err)
		if success && localReply == "" {
			state.AgentThreadID = ""
			state.AgentTokenUsage = nil
			h.saveConversation(key, state, requestID)
		}
		return err
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
	return nil
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

func (h *SlackEventHandler) WithGitLab(handler *CodingHandler) *SlackEventHandler {
	h.gitlab = handler
	h.linkMergePeers()
	return h
}

func (h *SlackEventHandler) linkMergePeers() {
	if h.coding != nil && h.gitlab != nil {
		h.coding.service.WithMergePeer(h.gitlab.service)
		h.gitlab.service.WithMergePeer(h.coding.service)
	}
}
