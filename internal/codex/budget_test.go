package codex

import (
	"nudge/internal/analytics"
	"nudge/internal/service"
	"testing"
)

func TestTokenUsageDoesNotImposeNudgyQuota(t *testing.T) {
	// Old deployment variables must not silently restore removed quotas.
	t.Setenv("NUDGY_RUN_TOKEN_LIMIT", "1")
	t.Setenv("NUDGY_USER_DAILY_TOKENS", "1")
	t.Setenv("NUDGY_WORKSPACE_DAILY_TOKENS", "1")
	tracker := analytics.NewUsageTracker()
	total := service.AgentTokenUsage{Input: 1500000, Output: 100000, Total: 1600000}
	result, err := consumeUsagePackets(t, service.AgentRequest{}, tracker,
		tokenUsagePacket(t, "thread-1", "turn-1", &total),
		messagePacket(t, "thread-1", "turn-1", "final_answer", "completed"),
		completedPacket(t, "thread-1", "turn-1", "completed"))
	if err != nil || result.Text != "completed" {
		t.Fatalf("usage blocked completion: %v", err)
	}
	assertUsageRecord(t, tracker, total)
}
