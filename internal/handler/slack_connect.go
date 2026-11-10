package handler

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"nudge/internal/analytics"
	"nudge/internal/coding"
	"nudge/internal/repository"
	"nudge/internal/service"
	"nudge/internal/tracker"
)

// Match only standalone requests from the current signed Slack event. Quotes,
// negation, questions about connecting, and compound requests stay with the agent.
func connectionProvider(text string) string {
	text = strings.Join(strings.Fields(strings.ToLower(text)), " ")
	text = strings.TrimRight(text, ".!?")
	text = strings.TrimPrefix(text, "lütfen ")
	if text == "panel" || text == "paneli aç" || text == "entegrasyonlar" || text == "dashboard" {
		return "workspace"
	}
	for _, provider := range []string{"gitlab", "github", "jira", "trello"} {
		for _, request := range []string{
			provider + " hesabımı bağla",
			provider + " hesabımı bağlamak istiyorum",
			provider + " hesabımı yeniden bağla",
			provider + " hesabımı tekrar bağla",
			provider + " bağlantı linkini gönder",
			provider + " için yeni bağlantı linki gönder",
			"connect " + provider,
			"reconnect " + provider,
		} {
			if text == request {
				return provider
			}
		}
	}
	return ""
}

func (h *SlackEventHandler) connectionReply(ctx context.Context, provider string, identity coding.Identity, job *repository.SlackJob) (string, bool) {
	if h.workspace != nil {
		if job != nil {
			if err := h.jobs.CheckLease(ctx, *job); err != nil {
				return "Panel bağlantısı gönderilemedi. Yeniden dene.", false
			}
		}
		started := time.Now()
		sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		err := h.workspace.SendLink(sendCtx, identity.UserID, identity.Team)
		analytics.RecordTool(ctx, "workspace_connect", "host", started, err == nil, `{"private_panel_link":true}`)
		if err != nil {
			return "Panel bağlantısını özel mesajına gönderemedim. Biraz sonra tekrar dene.", false
		}
		return "Panel bağlantını Nudgy ile özel mesajına (DM) gönderdim. Yönetici entegrasyonları kurabilir; ekip üyeleri kendi hesaplarını doğrulayabilir.", true
	}
	if provider == "workspace" {
		return "Çalışma alanı paneli bu kurulumda etkin değil.", false
	}
	if provider == "jira" || provider == "trello" {
		return h.trackerConnectionReply(ctx, provider, identity, job)
	}
	ch, name := h.coding, "GitHub"
	if provider == "gitlab" {
		ch, name = h.gitlab, "GitLab"
	}
	if ch == nil {
		return name + " bağlantısı bu Nudgy kurulumunda etkin değil.", false
	}
	base := service.NewAgentTools(nil, nil, nil, identity.UserID, time.Now(), time.UTC)
	var tools service.AgentTools = ch.Tools(base, identity)
	if job != nil {
		tools = leasedAgentTools{AgentTools: tools, jobs: h.jobs, job: *job}
	}
	started := time.Now()
	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := tools.Call(sendCtx, provider+"_connect", json.RawMessage(`{}`))
	analytics.RecordTool(ctx, provider+"_connect", "host", started, err == nil, output)
	// Never log signed URLs, tokens, or raw transport errors.
	log.Printf("connection_provider=%s stage=private_link_delivery success=%t user_id=%d", provider, err == nil, identity.UserID)
	if err != nil {
		return name + " bağlantı linkini özel mesajına gönderemedim. Lütfen biraz sonra tekrar dene; sorun sürerse Nudgy’nin Slack bağlantısını kontrol etmeliyiz.", false
	}
	return name + " yetkilendirme bağlantısını Nudgy ile özel mesajına (DM) gönderdim. Slack’te Nudgy → Mesajlar bölümünden yeni bağlantıyı açıp izinleri onayla ve projelerini seç. Mevcut hesabın bağlı olsa da yeniden yetkilendirebilirsin.", true
}

func (h *SlackEventHandler) trackerConnectionReply(ctx context.Context, provider string, identity coding.Identity, job *repository.SlackJob) (string, bool) {
	name := "Jira"
	if provider == "trello" {
		name = "Trello"
	}
	for _, integration := range h.trackers {
		if integration.service.Kind() != provider {
			continue
		}
		base := service.NewAgentTools(nil, nil, nil, identity.UserID, time.Now(), time.UTC)
		tools := integration.Tools(base, tracker.Identity{UserID: identity.UserID, Team: identity.Team})
		if job != nil {
			tools = leasedAgentTools{AgentTools: tools, jobs: h.jobs, job: *job}
		}
		sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		started := time.Now()
		output, err := tools.Call(sendCtx, provider+"_connect", json.RawMessage(`{}`))
		analytics.RecordTool(ctx, provider+"_connect", "host", started, err == nil, output)
		if err != nil {
			return name + " bağlantı linkini özel mesajına gönderemedim. Biraz sonra tekrar dene.", false
		}
		return name + " yetkilendirme bağlantısını Nudgy ile özel mesajına (DM) gönderdim. Bağlantıyı aç, izinleri onayla ve Nudgy’nin okuyabileceği alanları seç.", true
	}
	return name + " bağlantısı bu Nudgy kurulumunda etkin değil.", false
}
