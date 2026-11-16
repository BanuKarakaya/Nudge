package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"nudge/internal/analytics"
	"nudge/internal/service"
)

func TestToolOperationsSurviveFailureAndDeduplicate(t *testing.T) {
	tracker := &analytics.OperationTracker{}
	var events []analytics.LiveEvent
	ctx, cancel := context.WithTimeout(analytics.WithOperationTracker(context.Background(), tracker), time.Second)
	defer cancel()
	ctx = analytics.WithReporter(ctx, func(e analytics.LiveEvent) { events = append(events, e) })
	tools := &recordingAgentTools{call: func(context.Context, string, json.RawMessage) (string, error) {
		return "", errors.New("private credential")
	}}
	var output bytes.Buffer
	c := &connection{encoder: json.NewEncoder(&output), pending: []packet{
		toolPacket(t, "1", "call-1", "rss_articles", "thread-1", "turn-1", ""),
		toolPacket(t, "2", "call-1", "rss_articles", "thread-1", "turn-1", ""),
		completedPacket(t, "thread-1", "turn-1", "failed"),
	}}
	_, err := (&Runtime{config: Config{Model: "actual-model"}}).consume(ctx, c, service.AgentRequest{Tools: tools}, "thread-1", "turn-1")
	if err == nil {
		t.Fatal("expected failed turn")
	}
	if len(events) != 2 || events[0].Stage != "tool_started" || events[1].Stage != "tool_failed" || events[0].Model != "actual-model" || events[1].ErrorCode == "" {
		t.Fatalf("live tool events missing or duplicated: %+v", events)
	}
	encoded, _ := json.Marshal(events)
	if bytes.Contains(encoded, []byte("private credential")) {
		t.Fatal("raw error leaked into live events")
	}
	ops := tracker.Snapshot()
	if len(ops) != 1 || ops[0].Status != "failed" || ops[0].Model != "actual-model" || len(tools.calls) != 1 {
		t.Fatalf("trace=%+v calls=%v", ops, tools.calls)
	}
}
