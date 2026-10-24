package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"nudge/internal/analytics"
	"nudge/internal/service"
)

func TestSlackPersistsToolOperationsAfterAgentFailure(t *testing.T) {
	a := &socialAnalytics{}
	h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: &conversationSender{}, analytics: a,
		runtime: agentFunc(func(ctx context.Context, _ service.AgentRequest) (service.AgentResult, error) {
			analytics.RecordTool(ctx, "rss_inventory", "test-model", time.Now(), true, `{"total":4}`)
			return service.AgentResult{}, errors.New("agent unavailable")
		}),
	}
	h.replyWithSummary(conversationEvent("RSS başlıklarımı getir"))
	if len(a.record.Operations) != 1 || a.record.Operations[0].Tool != "rss_inventory" || a.record.Status == "success" {
		t.Fatalf("trace lost or wrong outcome: %+v", a.record)
	}
}
