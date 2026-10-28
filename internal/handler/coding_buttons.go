package handler

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"nudge/internal/coding"
	"nudge/internal/service"
)

const codingPushAction = "coding_push"

type codingPushValue struct {
	UserID int64  `json:"user_id"`
	TaskID string `json:"task_id"`
	Digest string `json:"digest"`
	Token  string `json:"token"`
}

func pushPurpose(id, digest string) string { return "coding-push:" + id + ":" + digest }

type slackBlocksClient interface {
	PostMessageInThreadWithBlocks(context.Context, string, string, string, string, []map[string]any) error
	UpdateMessageBlocks(context.Context, string, string, string, []map[string]any) error
}

// Collected by the host callback, never by model-produced JSON or prose.
type codingReply struct {
	h        *CodingHandler
	identity coding.Identity
	mu       sync.Mutex
	reviews  map[string]coding.EditInfo
}

func (h *CodingHandler) ToolsForReply(base service.AgentTools, i coding.Identity) (service.AgentTools, *codingReply) {
	reply := &codingReply{h: h, identity: i, reviews: make(map[string]coding.EditInfo)}
	return h.Tools(base, i, func(info coding.EditInfo) {
		reply.mu.Lock()
		defer reply.mu.Unlock()
		reply.reviews[info.Task.ID] = info
	}), reply
}

func (h *SlackEventHandler) postReply(ctx context.Context, token, channel, thread, text string, reply *codingReply) error {
	if reply == nil {
		return h.client.PostMessageInThread(ctx, token, channel, thread, text)
	}
	blocks, err := reply.blocks(text)
	if err != nil {
		return err
	}
	if len(blocks) == 0 {
		return h.client.PostMessageInThread(ctx, token, channel, thread, text)
	}
	client, ok := h.client.(slackBlocksClient)
	if !ok {
		return errors.New("Slack push button transport unavailable")
	}
	return client.PostMessageInThreadWithBlocks(ctx, token, channel, thread, text, blocks)
}

func textBlocks(text string) []map[string]any {
	var blocks []map[string]any
	runes := []rune(text)
	for len(runes) > 0 {
		n := min(len(runes), 2900)
		blocks = append(blocks, map[string]any{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": string(runes[:n])}})
		runes = runes[n:]
	}
	return blocks
}

func (r *codingReply) blocks(text string) ([]map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reviews) == 0 {
		return nil, nil
	}
	blocks := textBlocks(text)
	ids := make([]string, 0, len(r.reviews))
	for id := range r.reviews {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		info := r.reviews[id]
		if info.Task.Status != "awaiting_user_test" {
			continue
		}
		token, err := r.h.links.Token(r.identity.UserID, pushPurpose(id, info.Digest), time.Now().Add(24*time.Hour))
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(codingPushValue{UserID: r.identity.UserID, TaskID: id, Digest: info.Digest, Token: token})
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, map[string]any{"type": "context", "elements": []map[string]string{{"type": "plain_text", "text": info.Task.Repository.FullName + " · Testin tamamlandıysa Pushla’ya bas. Yalnızca bu görevin sahibi onaylayabilir."}}},
			map[string]any{"type": "actions", "block_id": "coding_push:" + id, "elements": []map[string]any{{"type": "button", "text": map[string]string{"type": "plain_text", "text": "Pushla"}, "style": "primary", "action_id": codingPushAction, "value": string(value)}}})
	}
	return blocks, nil
}
