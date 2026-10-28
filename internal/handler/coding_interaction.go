package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"nudge/internal/coding"
)

var slackActionTime = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

func buttonAcknowledgement(w http.ResponseWriter, text string) {
	writeJSON(w, http.StatusOK, map[string]any{"response_type": "ephemeral", "replace_original": false, "text": text})
}

// Called only after the common Slack handler has verified the signature over
// the complete raw body. Return within Slack's 3-second acknowledgement window.
func (h *CodingHandler) handlePushButton(w http.ResponseWriter, r *http.Request, p slackInteractionPayload) {
	var value codingPushValue
	if p.Type != "block_actions" || len(p.Actions) != 1 || json.Unmarshal([]byte(p.Actions[0].Value), &value) != nil || p.Team.ID == "" || p.User.ID == "" || p.Channel.ID == "" || p.Container.ChannelID != p.Channel.ID || p.Container.MessageTS != p.Message.TS || !slackActionTime.MatchString(p.Message.TS) || !slackActionTime.MatchString(p.Message.ThreadTS) || !slackActionTime.MatchString(p.Actions[0].ActionTS) {
		buttonAcknowledgement(w, "Bu push isteğini doğrulayamadım. Nudgy’den güncel inceleme mesajını iste.")
		return
	}
	if !h.links.Verify(value.Token, value.UserID, pushPurpose(value.TaskID, value.Digest), time.Now()) {
		buttonAcknowledgement(w, "Bu butonun süresi dolmuş veya onayı geçersiz. Nudgy’den yeni bir inceleme mesajı iste.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	userID, err := h.installations.GetUserIDBySlackUserID(ctx, p.Team.ID, p.User.ID)
	if err != nil || userID != value.UserID {
		buttonAcknowledgement(w, "Bu değişikliği yalnızca görevi oluşturan kişi pushlayabilir.")
		return
	}
	select {
	case h.pushSlots <- struct{}{}:
	default:
		buttonAcknowledgement(w, "Şu an diğer push işlemleri tamamlanıyor. Biraz sonra aynı butona tekrar basabilirsin.")
		return
	}
	identity := coding.Identity{UserID: userID, Team: p.Team.ID, Channel: p.Channel.ID, Thread: p.Message.ThreadTS, Event: "button:" + p.Message.TS + ":" + p.Actions[0].ActionTS}
	buttonAcknowledgement(w, "Push isteğini aldım. Sonucu bu konuşmaya yazacağım.")
	go func() { defer func() { <-h.pushSlots }(); h.performButtonPush(identity, value, p) }()
}

func (h *CodingHandler) performButtonPush(i coding.Identity, value codingPushValue, p slackInteractionPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	installation, err := h.installations.GetByUserID(ctx, i.UserID)
	if err != nil || installation.TeamID != i.Team || installation.SlackUserID != p.User.ID {
		return
	}
	info, err := h.service.PushEditFromButton(ctx, i, value.TaskID, value.Digest)
	// Publishing may consume its deadline; delivery gets its own short context.
	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer notifyCancel()
	if err != nil {
		text := "Push tamamlanamadı. Kodun inceleme sürümü korundu; erişimini kontrol edip tekrar deneyebilirsin. Ana dal değiştiyse güncel kodla yeni bir görev hazırlatmalısın."
		if errors.Is(err, coding.ErrNotFound) || errors.Is(err, coding.ErrDenied) {
			text = "Bu inceleme sürümü artık pushlanamıyor veya bu konuşmaya ait değil. Kod değişmiş, görev iptal edilmiş ya da erişimin kaldırılmış olabilir. Nudgy’den güncel inceleme mesajını iste."
		}
		if errors.Is(err, coding.ErrConnect) {
			text = "GitHub bağlantın yeniden doğrulanmalı. Nudgy’den GitHub’ını bağlamasını iste, sonra görevi yeniden incele."
		}
		channel, dmErr := h.client.OpenDirectMessage(notifyCtx, installation.BotToken, p.User.ID)
		if dmErr == nil {
			_ = h.client.PostMessage(notifyCtx, installation.BotToken, channel, text)
		}
		log.Printf("coding_event=button_push_failed user_id=%d task_id=%s", i.UserID, value.TaskID)
		return
	}
	if client, ok := h.client.(slackBlocksClient); ok {
		blocks := completedPushBlocks(p.Message.Blocks, value.TaskID)
		if err := client.UpdateMessageBlocks(notifyCtx, installation.BotToken, i.Channel, p.Message.TS, blocks); err != nil {
			log.Printf("coding_event=push_button_update_failed user_id=%d task_id=%s", i.UserID, value.TaskID)
		}
	}
	link := "https://github.com/" + info.Task.Repository.FullName + "/tree/" + url.PathEscape(info.Branch)
	text := "Onayladığın değişiklikler GitHub’a pushlandı.\n<" + link + "|" + info.Task.Repository.FullName + " · Dalı aç>\nAna dal değiştirilmedi."
	if client, ok := h.client.(slackThreadMessageClient); ok {
		if err := client.PostMessageInThread(notifyCtx, installation.BotToken, i.Channel, i.Thread, text); err != nil {
			log.Printf("coding_event=push_confirmation_failed user_id=%d task_id=%s", i.UserID, value.TaskID)
		}
	}
}

func completedPushBlocks(original []map[string]any, id string) []map[string]any {
	result := make([]map[string]any, 0, len(original))
	for _, block := range original {
		if block["block_id"] == "coding_push:"+id {
			result = append(result, map[string]any{"type": "context", "elements": []map[string]string{{"type": "plain_text", "text": "✓ Onayladığın sürüm GitHub’a pushlandı."}}})
			continue
		}
		result = append(result, block)
	}
	return result
}
