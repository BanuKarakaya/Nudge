package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nudge/internal/coding"
	"nudge/internal/repository"
	"nudge/internal/service"
	"nudge/internal/slack"
	"nudge/internal/tracker"
)

type actionStore interface {
	service.ActionStore
	ApproveAction(context.Context, string, int64, string, string, string) error
	StartAction(context.Context, string, int64) (service.ActionProposal, error)
	FinishAction(context.Context, string, bool) error
	SetActionMode(context.Context, int64, string, string) error
}

func (h *SlackEventHandler) WithActions(store actionStore, interactions *SlackInteractionHandler) {
	h.actions = store
	interactions.actions = h
}
func (h *SlackEventHandler) notifyAction(ctx context.Context, p service.ActionProposal) error {
	installation, err := h.installations.GetByUserID(ctx, p.Identity.UserID)
	if err != nil {
		return err
	}
	client, ok := h.client.(slackBlocksClient)
	if !ok {
		return errors.New("approval transport unavailable")
	}
	var formatted strings.Builder
	var args any
	_ = json.Unmarshal(p.Arguments, &args)
	pretty, _ := json.MarshalIndent(args, "", "  ")
	formatted.WriteString(p.Tool + "\n" + string(pretty))
	if p.Revision != "" {
		formatted.WriteString("\nİncelenen sürüm: " + p.Revision)
	}
	blocks := []map[string]any{{"type": "section", "text": map[string]string{"type": "plain_text", "text": "Bu işlem dış sistemde değişiklik yapacak. Aşağıdaki hedef ve içeriği kontrol et. Onay 20 dakika geçerli; yalnızca isteğin sahibi onaylayabilir."}}}
	text := []rune(formatted.String())
	for len(text) > 0 {
		n := min(len(text), 2800)
		blocks = append(blocks, map[string]any{"type": "section", "text": map[string]string{"type": "plain_text", "text": string(text[:n])}})
		text = text[n:]
	}
	if p.Revision != "" && strings.HasSuffix(p.Tool, "repository_edit_finish") {
		ch := h.coding
		if strings.HasPrefix(p.Tool, "gitlab_") {
			ch = h.gitlab
		}
		var a struct {
			Task string `json:"task_id"`
		}
		_ = json.Unmarshal(p.Arguments, &a)
		token, err := ch.links.Token(p.Identity.UserID, ch.reviewPurpose(a.Task, p.Revision), time.Now().Add(20*time.Minute))
		if err != nil {
			return err
		}
		link := ch.publicURL + "/" + ch.service.Kind() + "/review?" + url.Values{"user_id": {strconv.FormatInt(p.Identity.UserID, 10)}, "task_id": {a.Task}, "digest": {p.Revision}, "token": {token}}.Encode()
		blocks = append(blocks, map[string]any{"type": "actions", "elements": []map[string]any{{"type": "button", "text": map[string]string{"type": "plain_text", "text": "Dosya değişikliklerini incele"}, "url": link}}})
	}
	blocks = append(blocks, map[string]any{"type": "actions", "elements": []map[string]any{{"type": "button", "text": map[string]string{"type": "plain_text", "text": "Değişikliği onayla"}, "action_id": "nudgy_action_approve", "value": p.ID, "style": "primary"}}})
	return client.PostMessageInThreadWithBlocks(ctx, installation.BotToken, p.Identity.Channel, p.Identity.Thread, "Onay bekleyen değişiklik: "+p.Tool, blocks)
}
func (h *SlackEventHandler) approveAction(w http.ResponseWriter, r *http.Request, p slackInteractionPayload) {
	user, err := h.installations.GetUserIDBySlackUserID(r.Context(), p.Team.ID, p.User.ID)
	if err != nil || h.actions == nil || h.jobs == nil {
		writeError(w, 403, "approval owner unavailable")
		return
	}
	thread := p.Message.ThreadTS
	if thread == "" {
		thread = p.Message.TS
	}
	channel := p.Channel.ID
	if channel == "" {
		channel = p.Container.ChannelID
	}
	id := p.Actions[0].Value
	if err = h.actions.ApproveAction(r.Context(), id, user, p.Team.ID, channel, thread); err != nil {
		writeError(w, 403, "approval expired, used or owner mismatch")
		return
	}
	var event slackEventEnvelope
	event.TeamID = p.Team.ID
	event.EventID = "approval:" + id
	event.ApprovalID = id
	event.Event.Type = "app_mention"
	event.Event.User = p.User.ID
	event.Event.Channel = channel
	event.Event.ThreadTS = thread
	event.Event.TS = thread
	raw, _ := json.Marshal(event)
	err = h.jobs.Enqueue(r.Context(), repository.SlackJob{Key: eventConversationKey(event), EventKey: slackEventKey(event), Payload: raw})
	if err != nil {
		writeError(w, 503, "approval could not be queued")
		return
	}
	writeJSON(w, 200, map[string]string{"text": "Onayın alındı; işlem sonucu bu konuşmaya gelecek."})
}
func (h *SlackEventHandler) processActionJob(ctx context.Context, event slackEventEnvelope, job repository.SlackJob) error {
	user, err := h.installations.GetUserIDBySlackUserID(ctx, event.TeamID, event.Event.User)
	if err != nil {
		return err
	}
	if h.actions == nil {
		return errors.New("actions unavailable")
	}
	p, err := h.actions.StartAction(ctx, event.ApprovalID, user)
	if err != nil && !errors.Is(err, service.ErrApprovalUnavailable) {
		return err
	}
	message := "İşlem yeniden çalıştırılmadı: onay sona ermiş veya önceki denemenin sonucu belirsiz. Sağlayıcıdaki güncel durumu kontrol et."
	success := false
	if err == nil {
		if p.Identity.Team != event.TeamID || p.Identity.Channel != job.Key.Channel || p.Identity.Thread != job.Key.Thread {
			return errors.New("action scope mismatch")
		}
		mode, modeErr := h.actions.ActionMode(ctx, user, p.Tool)
		if modeErr != nil || mode == "disabled" {
			err = service.ErrActionDisabled
		} else {
			tools := service.NewAgentTools(h.feedbinDigest, h.bookmarks, h.web, user, time.Now(), time.UTC)
			if h.coding != nil {
				tools = h.coding.Tools(tools, p.Identity)
			}
			if h.gitlab != nil {
				tools = h.gitlab.Tools(tools, p.Identity)
			}
			for _, integration := range h.trackers {
				tools = integration.Tools(tools, tracker.Identity{UserID: user, Team: event.TeamID}, "approval:"+p.ID)
			}
			tools, err = h.moduleTools(tools, p.Identity)
			if err != nil {
				return err
			}
			// The human approved this exact tool and immutable arguments. Merge services
			// still check live rights, branch protection and their persisted head binding.
			callCtx := coding.WithMergeIntentResolver(ctx, func(_ context.Context, i coding.Identity) (coding.MergeIntent, error) {
				if i != p.Identity {
					return coding.MergeIntent{}, coding.ErrDenied
				}
				var a struct {
					Repository string `json:"repository"`
					Number     int64  `json:"number"`
					Source     string `json:"source_branch"`
					Target     string `json:"target_branch"`
				}
				if json.Unmarshal(p.Arguments, &a) != nil {
					return coding.MergeIntent{}, coding.ErrArguments
				}
				provider := "github"
				if strings.HasPrefix(p.Tool, "gitlab_") {
					provider = "gitlab"
				}
				return coding.MergeIntent{Action: "merge", Provider: provider, Repository: a.Repository, Number: int(a.Number), SourceBranch: a.Source, TargetBranch: a.Target}, nil
			})
			callCtx = coding.WithApprovedRevision(callCtx, p.Revision)
			var result string
			if strings.HasSuffix(p.Tool, "repository_edit_finish") {
				var a struct {
					Task string `json:"task_id"`
				}
				_ = json.Unmarshal(p.Arguments, &a)
				s := h.coding
				if strings.HasPrefix(p.Tool, "gitlab_") {
					s = h.gitlab
				}
				if s == nil || p.Revision == "" {
					err = coding.ErrDenied
				} else {
					identity := p.Identity
					identity.Event = "button:" + p.ID
					var info coding.EditInfo
					info, err = s.service.PushEditFromButton(callCtx, identity, a.Task, p.Revision)
					if err == nil {
						raw, _ := json.Marshal(info)
						result = string(raw)
					}
				}
			} else {
				result, err = tools.Call(callCtx, p.Tool, p.Arguments)
			}
			if err == nil {
				success = true
				message = "Onayladığın işlem tamamlandı.\n" + slack.SanitizeGeneratedText(result)
			}
		}
		if err != nil {
			code, description := service.AgentToolFailure(err)
			message = fmt.Sprintf("İşlem tamamlanamadı (%s). %s", code, description)
		}
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		saveErr := h.actions.FinishAction(finish, p.ID, success)
		cancel()
		if saveErr != nil {
			return saveErr
		}
	}
	prepared := queuedReply{Version: 1, Message: message, Success: success, Record: repository.InteractionRecord{RequestID: fmt.Sprintf("slack-job-%d", job.ID), UserID: user, TeamID: event.TeamID, ChannelID: job.Key.Channel, ThreadTS: job.Key.Thread, SlackUserID: event.Event.User, RequestText: "approved action", ResponseText: message, Status: map[bool]string{true: "success", false: "failed"}[success], StartedAt: time.Now()}}
	raw, err := json.Marshal(prepared)
	if err != nil {
		return err
	}
	if err = h.jobs.SaveResult(ctx, job, raw); err != nil {
		return err
	}
	return h.deliverQueuedReply(ctx, event, job, prepared)
}

func (h *SlackEventHandler) prepareAction(ctx context.Context, p service.ActionProposal) (service.ActionProposal, error) {
	if strings.HasSuffix(p.Tool, "repository_change_request_merge") || strings.HasSuffix(p.Tool, "repository_branches_merge") {
		var a struct {
			Repository string `json:"repository"`
			Number     int    `json:"number"`
			Source     string `json:"source_branch"`
			Target     string `json:"target_branch"`
		}
		if json.Unmarshal(p.Arguments, &a) != nil {
			return p, coding.ErrArguments
		}
		ch := h.coding
		if strings.HasPrefix(p.Tool, "gitlab_") {
			ch = h.gitlab
		}
		if ch == nil {
			return p, coding.ErrUnavailable
		}
		var err error
		p.Revision, err = ch.service.PreviewMerge(ctx, p.Identity, a.Repository, a.Number, a.Source, a.Target)
		return p, err
	}
	if !strings.HasSuffix(p.Tool, "repository_edit_finish") {
		return p, nil
	}
	var a struct {
		Task    string `json:"task_id"`
		Summary string `json:"summary"`
	}
	if json.Unmarshal(p.Arguments, &a) != nil {
		return p, coding.ErrArguments
	}
	ch := h.coding
	if strings.HasPrefix(p.Tool, "gitlab_") {
		ch = h.gitlab
	}
	if ch == nil {
		return p, coding.ErrUnavailable
	}
	info, err := ch.service.FinishEdit(ctx, p.Identity, a.Task, a.Summary)
	if err != nil {
		return p, err
	}
	p.Revision = info.Digest
	return p, nil
}
