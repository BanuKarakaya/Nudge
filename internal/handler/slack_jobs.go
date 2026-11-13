package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"nudge/internal/coding"
	"nudge/internal/repository"
	"nudge/internal/service"
)

func (h *SlackEventHandler) WithJobQueue(jobs repository.SlackJobRepository) *SlackEventHandler {
	h.jobs = jobs
	return h
}

func eventConversationKey(e slackEventEnvelope) repository.SlackJobKey {
	thread := e.Event.ThreadTS
	if thread == "" {
		thread = e.Event.TS
	}
	return repository.SlackJobKey{Team: e.TeamID, Channel: e.Event.Channel, Thread: thread, User: e.Event.User}
}

func slackEventKey(e slackEventEnvelope) string {
	if e.EventID != "" {
		return "event:" + e.EventID
	}
	// JSON avoids delimiter collisions for the legacy fallback identity.
	key, _ := json.Marshal([]string{e.Event.Channel, e.Event.TS})
	return "message:" + string(key)
}

type queuedReply struct {
	Version int
	Message string
	Success bool
	State   *service.ConversationState
	Record  repository.InteractionRecord
	// Store the host-observed review metadata, not expiring signed buttons.
	Reviews []coding.EditInfo
}

// ProcessJob bypasses process-local replay suppression: durable ownership has
// already been acquired, and a new attempt must be allowed after interruption.
func (h *SlackEventHandler) ProcessJob(ctx context.Context, job repository.SlackJob) error {
	if h.jobs == nil {
		return errors.New("Slack job queue unavailable")
	}
	var event slackEventEnvelope
	if err := json.Unmarshal(job.Payload, &event); err != nil {
		return err
	}
	if event.Event.Type != "app_mention" || eventConversationKey(event) != job.Key || slackEventKey(event) != job.EventKey {
		return errors.New("Slack job identity mismatch")
	}
	if err := h.jobs.CheckLease(ctx, job); err != nil {
		return err
	}
	if len(job.Result) != 0 {
		var prepared queuedReply
		if err := json.Unmarshal(job.Result, &prepared); err != nil {
			return err
		}
		if prepared.Version != 1 || prepared.Message == "" {
			return errors.New("invalid saved Slack response")
		}
		err := h.deliverQueuedReply(ctx, event, job, prepared)
		if err == nil {
			h.clearRecoveredReaction(ctx, event)
		}
		return err
	}
	if event.ApprovalID != "" {
		return h.processActionJob(ctx, event, job)
	}
	return h.processSlackEvent(ctx, event, &job)
}

func (h *SlackEventHandler) deliverQueuedReply(ctx context.Context, event slackEventEnvelope, job repository.SlackJob, prepared queuedReply) error {
	if !job.Delivered {
		// Resolve current credentials and membership on every delivery attempt.
		userID, err := h.installations.GetUserIDBySlackUserID(ctx, event.TeamID, event.Event.User)
		if err != nil {
			return err
		}
		if userID != prepared.Record.UserID {
			return errors.New("Slack job owner changed")
		}
		installation, err := h.installations.GetByUserID(ctx, userID)
		if err != nil {
			return err
		}
		var reply *codingReply
		if len(prepared.Reviews) > 0 {
			ch := h.coding
			if ch == nil {
				ch = h.gitlab
			}
			if ch == nil {
				return errors.New("coding reply transport unavailable")
			}
			reply = &codingReply{h: ch, identity: coding.Identity{UserID: userID}, reviews: make(map[string]coding.EditInfo)}
			for _, review := range prepared.Reviews {
				reply.reviews[review.Task.ID] = review
			}
		}
		if err := h.jobs.CheckLease(ctx, job); err != nil {
			return err
		}
		sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = h.postReply(sendCtx, installation.BotToken, event.Event.Channel, job.Key.Thread, prepared.Message, reply)
		cancel()
		if err != nil {
			return err
		}
		// Slack and Postgres cannot commit atomically. A crash before this mark
		// can resend a message, but never regenerates a persisted response.
		if err := h.jobs.MarkDelivered(ctx, job); err != nil {
			return err
		}
	}
	var state json.RawMessage
	if prepared.State != nil {
		raw, err := json.Marshal(prepared.State)
		if err != nil {
			return err
		}
		state = raw
	}
	prepared.Record.LatencyMS = time.Since(prepared.Record.StartedAt).Milliseconds()
	if err := h.jobs.Complete(ctx, job, state, prepared.Record, prepared.Success); err != nil {
		return err
	}
	return nil
}

// A hard crash can leave the original processing reaction behind. Completion
// must not depend on this cosmetic cleanup succeeding.
func (h *SlackEventHandler) clearRecoveredReaction(ctx context.Context, event slackEventEnvelope) {
	client, ok := h.client.(slackReactionClient)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	userID, err := h.installations.GetUserIDBySlackUserID(ctx, event.TeamID, event.Event.User)
	if err != nil {
		return
	}
	installation, err := h.installations.GetByUserID(ctx, userID)
	if err != nil {
		return
	}
	_ = client.RemoveReaction(ctx, installation.BotToken, event.Event.Channel, event.Event.TS, processingReaction)
}

type leasedAgentTools struct {
	service.AgentTools
	jobs repository.SlackJobRepository
	job  repository.SlackJob
}

func (t leasedAgentTools) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := t.jobs.CheckLease(ctx, t.job); err != nil {
		return "", fmt.Errorf("job ownership: %w", err)
	}
	return t.AgentTools.Call(ctx, name, args)
}
