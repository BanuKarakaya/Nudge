package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"nudge/internal/analytics"
	"nudge/internal/service"
)

func handoffPacket(t *testing.T, args string) packet {
	t.Helper()
	p := eventPacket(t, "item/tool/call", map[string]any{"threadId": "thread-1", "turnId": "turn-1", "callId": "handoff-1", "tool": handoffTool, "arguments": json.RawMessage(args)})
	p.ID = json.RawMessage(`"tool-handoff"`)
	return p
}

func routingPackets(t *testing.T, cfg Config, messages ...packet) (service.AgentResult, error, []packet, []analytics.AIUsage) {
	t.Helper()
	var output bytes.Buffer
	c := &connection{encoder: json.NewEncoder(&output), pending: messages}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	tracker := analytics.NewUsageTracker()
	ctx = analytics.WithUsageTracker(ctx, tracker)
	r := &Runtime{config: cfg}
	result, err := r.consume(ctx, c, service.AgentRequest{Tools: &recordingAgentTools{}}, "thread-1", "turn-1")
	var replies []packet
	d := json.NewDecoder(&output)
	for {
		var p packet
		if err := d.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		replies = append(replies, p)
	}
	return result, err, replies, tracker.Snapshot()
}

func TestHandoffWaitsForCompletionAndInterruptAck(t *testing.T) {
	for _, status := range []string{"interrupted", "completed"} {
		for _, ackFirst := range []bool{false, true} {
			ack := packet{ID: json.RawMessage("1"), Result: json.RawMessage(`{}`)}
			completed := completedPacket(t, "thread-1", "turn-1", status, agentItem{Type: "agentMessage", Text: "Do not deliver partial DeepSeek text"})
			events := []packet{handoffPacket(t, `{}`), tokenUsagePacket(t, "thread-1", "turn-1", &service.AgentTokenUsage{Input: 20, Output: 5, Total: 25})}
			if ackFirst {
				events = append(events, ack, completed)
			} else {
				events = append(events, completed, ack)
			}
			result, err, replies, usage := routingPackets(t, Config{Model: "deepseek", ComplexModel: "luna"}, events...)
			if !errors.Is(err, errModelHandoff) || result.ThreadID != "thread-1" || result.Text != "" || result.TokenUsage == nil || result.TokenUsage.Total != 25 {
				t.Fatalf("handoff result=%+v err=%v", result, err)
			}
			if len(replies) != 2 || replies[1].Method != "turn/interrupt" || !strings.Contains(string(replies[1].Params), `"turnId":"turn-1"`) {
				t.Fatalf("missing bound interrupt: %+v", replies)
			}
			if success, _ := toolReply(t, replies[0]); !success {
				t.Fatal("valid handoff rejected")
			}
			if len(usage) != 1 || usage[0].Model != "deepseek" || usage[0].TotalTokens != 25 {
				t.Fatalf("wrong routing usage: %+v", usage)
			}
		}
	}
}

func TestHandoffCannotSelectArbitraryModelOrRepeatOnLuna(t *testing.T) {
	for _, tc := range []struct{ model, args string }{
		{"luna", `{"model":"unapproved/expensive-model"}`},
		{"luna", `null`}, {"luna", `[]`}, {"luna", `""`},
		{"", `{}`},
	} {
		result, err, replies, _ := routingPackets(t, Config{ComplexModel: tc.model}, handoffPacket(t, tc.args), completedPacket(t, "thread-1", "turn-1", "completed", agentItem{Type: "agentMessage", Text: "Final answer"}))
		if err != nil || result.Text != "Final answer" || len(replies) != 1 {
			t.Fatalf("result=%+v err=%v replies=%v", result, err, replies)
		}
		if success, _ := toolReply(t, replies[0]); success {
			t.Fatalf("unauthorized handoff accepted: %+v", tc)
		}
	}
}

func TestHandoffRejectsFurtherToolsAndDuplicateInterrupts(t *testing.T) {
	result, err, replies, _ := routingPackets(t, Config{ComplexModel: "luna"},
		handoffPacket(t, `{}`), handoffPacket(t, `{}`),
		toolPacket(t, `"late-tool"`, "late-call", "rss_inventory", "thread-1", "turn-1", ""),
		packet{ID: json.RawMessage("1"), Result: json.RawMessage(`{}`)},
		completedPacket(t, "thread-1", "turn-1", "interrupted"))
	if !errors.Is(err, errModelHandoff) || result.ThreadID != "thread-1" || len(replies) != 4 {
		t.Fatalf("result=%+v err=%v replies=%v", result, err, replies)
	}
	if success, _ := toolReply(t, replies[3]); success {
		t.Fatal("data tool executed while switching")
	}
	interrupts := 0
	for _, p := range replies {
		if p.Method == "turn/interrupt" {
			interrupts++
		}
	}
	if interrupts != 1 {
		t.Fatalf("duplicate interrupts: %d", interrupts)
	}
}

func TestHandoffFailsClosedWithoutConfirmedStop(t *testing.T) {
	var rejected packet
	if err := json.Unmarshal([]byte(`{"id":1,"error":{"code":-32600}}`), &rejected); err != nil {
		t.Fatal(err)
	}
	for _, events := range [][]packet{
		{rejected},
		{completedPacket(t, "thread-1", "turn-1", "failed")},
		{packet{ID: json.RawMessage("1"), Result: json.RawMessage(`{}`)}},
		{completedPacket(t, "thread-1", "turn-1", "interrupted")},
	} {
		result, err, _, _ := routingPackets(t, Config{ComplexModel: "luna"}, append([]packet{handoffPacket(t, `{}`)}, events...)...)
		if err == nil || errors.Is(err, errModelHandoff) || result != (service.AgentResult{}) {
			t.Fatalf("unsafe handoff result=%+v err=%v", result, err)
		}
	}
}

func TestProviderURLValidation(t *testing.T) {
	for _, raw := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com?key=secret", "https://example.com#fragment", "not a URL", "https://%"} {
		cfg := runtimeTestConfig(t)
		cfg.BaseURL = raw
		if _, err := New(cfg); err == nil {
			t.Fatalf("unsafe provider URL accepted: %q", raw)
		}
	}
	for _, raw := range []string{"https://openrouter.ai/api/v1", "http://127.0.0.1:12345", "http://[::1]:12345", "http://localhost:12345"} {
		cfg := runtimeTestConfig(t)
		cfg.BaseURL, cfg.ModelCatalogURL = raw, raw+"/models"
		if _, err := New(cfg); err != nil {
			t.Fatalf("valid same-origin provider rejected: %v", err)
		}
	}
	cfg := runtimeTestConfig(t)
	cfg.ModelCatalogURL = "https://other.example/models"
	if _, err := New(cfg); err == nil {
		t.Fatal("catalog could receive credentials on a different origin")
	}
	cfg.ModelCatalogURL = "https://%"
	if _, err := New(cfg); err == nil {
		t.Fatal("invalid catalog accepted")
	}
	cfg.ModelCatalogURL = ""
	cfg.ComplexModel = cfg.Model
	if _, err := New(cfg); err == nil {
		t.Fatal("same model handoff accepted")
	}
}
