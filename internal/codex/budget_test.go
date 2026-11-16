package codex

import (
	"errors"
	"nudge/internal/analytics"
	"nudge/internal/service"
	"testing"
)

func TestReservedBudgetLimitsRuntimeAndModelHandoff(t *testing.T) {
	t.Setenv("NUDGY_RUN_TOKEN_LIMIT", "1000")
	req := service.AgentRequest{TokenLimit: 98, ThreadID: "thread-1", History: service.ConversationState{AgentTokenUsage: &service.AgentTokenUsage{Input: 100, Total: 100}}}
	tracker := analytics.NewUsageTracker()
	first := &service.AgentTokenUsage{Input: 140, Total: 140}
	result, err := consumeUsagePackets(t, req, tracker, tokenUsagePacket(t, "thread-1", "turn-1", first), messagePacket(t, "thread-1", "turn-1", "final_answer", "done"), completedPacket(t, "thread-1", "turn-1", "completed"))
	if err != nil || result.Text != "done" {
		t.Fatalf("remaining allowance rejected: %v", err)
	}
	req.TokenLimit = remainingTokenLimit(req, result.TokenUsage)
	if req.TokenLimit != 58 {
		t.Fatalf("handoff reset allowance: %d", req.TokenLimit)
	}
	req.History.AgentTokenUsage = result.TokenUsage
	_, err = consumeUsagePackets(t, req, tracker, tokenUsagePacket(t, "thread-1", "turn-1", &service.AgentTokenUsage{Input: 199, Total: 199}))
	var coded *analytics.OperationalError
	if !errors.As(err, &coded) || coded.Code != "token_limit" {
		t.Fatalf("reserved limit bypassed: %v", err)
	}
	if effectiveTokenLimit(2000) != 1000 || effectiveTokenLimit(0) != 1000 {
		t.Fatal("configured run ceiling ignored")
	}
}
