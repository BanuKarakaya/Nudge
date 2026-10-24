package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"nudge/internal/analytics"
	"nudge/internal/service"
)

// Use the service's actual definitions so the transport tests exercise the
// shared AgentTools contract without fetching data or calling a model.
type recordingAgentTools struct {
	calls []string
	args  []json.RawMessage
	call  func(context.Context, string, json.RawMessage) (string, error)
}

var _ service.AgentTools = (*recordingAgentTools)(nil)

func (*recordingAgentTools) Definitions() []service.ToolDefinition {
	return service.NewAgentTools(nil, nil, nil, 1, time.Now(), time.UTC).Definitions()
}

func (s *recordingAgentTools) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	s.calls = append(s.calls, name)
	s.args = append(s.args, append(json.RawMessage(nil), args...))
	if s.call != nil {
		return s.call(ctx, name, args)
	}
	return `{"total":3}`, nil
}

func runtimeTestConfig(t *testing.T) Config {
	t.Helper()
	return Config{Binary: os.Args[0], Model: "test-model", APIKey: "test-api-key", StateDir: filepath.Join(t.TempDir(), "state")}
}

func runtimeTestRequest() service.AgentRequest {
	return service.AgentRequest{
		Key:   service.ConversationKey{Team: "team", User: "user", Channel: "channel", Thread: "slack-thread"},
		Input: "Show my unread articles.",
	}
}

func TestNewValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Config)
		want string
	}{
		{"missing model", func(c *Config) { c.Model = " \t" }, "CODEX_SIMPLE_MODEL"},
		{"missing key", func(c *Config) { c.APIKey = "\n " }, "OPENROUTER_API_KEY"},
		{"empty state directory", func(c *Config) { c.StateDir = "" }, "absolute"},
		{"relative state directory", func(c *Config) { c.StateDir = "relative/state" }, "absolute"},
		{"missing binary", func(c *Config) { c.Binary = filepath.Join(c.StateDir, "missing-codex") }, "binary not found"},
		{"negative timeout", func(c *Config) { c.Timeout = -time.Second }, "timeout"},
		{"short timeout", func(c *Config) { c.Timeout = time.Second - 1 }, "timeout"},
		{"long timeout", func(c *Config) { c.Timeout = 140*time.Second + 1 }, "timeout"},
		{"negative concurrency", func(c *Config) { c.MaxConcurrent = -1 }, "concurrency"},
		{"excess concurrency", func(c *Config) { c.MaxConcurrent = 33 }, "concurrency"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := runtimeTestConfig(t)
			tc.edit(&cfg)
			r, err := New(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) || r != nil {
				t.Fatalf("New() = (%v, %v), want nil runtime and error containing %q", r, err, tc.want)
			}
		})
	}
}

func TestNewDefaultsAndExplicitLimits(t *testing.T) {
	for _, tc := range []struct {
		name        string
		timeout     time.Duration
		concurrency int
		wantTimeout time.Duration
		wantSlots   int
	}{
		{"defaults", 0, 0, 120 * time.Second, 4},
		{"minimum", time.Second, 1, time.Second, 1},
		{"maximum", 140 * time.Second, 32, 140 * time.Second, 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := runtimeTestConfig(t)
			cfg.Timeout, cfg.MaxConcurrent = tc.timeout, tc.concurrency
			r, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if r.config.Timeout != tc.wantTimeout || cap(r.slots) != tc.wantSlots {
				t.Fatalf("timeout/slots = %v/%d, want %v/%d", r.config.Timeout, cap(r.slots), tc.wantTimeout, tc.wantSlots)
			}
			if r.config.Model != cfg.Model || r.config.APIKey != cfg.APIKey || r.config.StateDir != cfg.StateDir {
				t.Fatal("explicit model, credentials, or state directory changed")
			}
			info, err := os.Stat(cfg.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
				t.Fatalf("state directory has unsafe mode %v", info.Mode())
			}
		})
	}
}

func TestNewRejectsStateFile(t *testing.T) {
	cfg := runtimeTestConfig(t)
	if err := os.WriteFile(cfg.StateDir, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "state directory") {
		t.Fatalf("New() error = %v, want state directory error", err)
	}
}

func eventPacket(t *testing.T, method string, params any) packet {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return packet{Method: method, Params: raw}
}

func messagePacket(t *testing.T, thread, turn, phase, text string) packet {
	t.Helper()
	return eventPacket(t, "item/completed", map[string]any{
		"threadId": thread, "turnId": turn,
		"item": map[string]string{"id": "message", "type": "agentMessage", "phase": phase, "text": text},
	})
}

func completedPacket(t *testing.T, thread, turn, status string, items ...agentItem) packet {
	t.Helper()
	return eventPacket(t, "turn/completed", map[string]any{
		"threadId": thread,
		"turn":     map[string]any{"id": turn, "status": status, "items": items},
	})
}

func toolPacket(t *testing.T, rpcID, callID, name, thread, turn, namespace string) packet {
	t.Helper()
	msg := eventPacket(t, "item/tool/call", map[string]any{
		"threadId": thread, "turnId": turn, "callId": callID, "tool": name,
		"namespace": namespace, "arguments": json.RawMessage(`{"scope":"all_unread"}`),
	})
	msg.ID = json.RawMessage(rpcID)
	return msg
}

func consumePackets(t *testing.T, tools service.AgentTools, messages ...packet) (service.AgentResult, error, []packet) {
	t.Helper()
	var output bytes.Buffer
	packets := make(chan packet, len(messages))
	for _, msg := range messages {
		packets <- msg
	}
	c := &connection{encoder: json.NewEncoder(&output), packets: packets, errors: make(chan error)}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := (&Runtime{}).consume(ctx, c, service.AgentRequest{Tools: tools}, "thread-1", "turn-1")
	var replies []packet
	decoder := json.NewDecoder(&output)
	for {
		var reply packet
		decodeErr := decoder.Decode(&reply)
		if decodeErr == io.EOF {
			break
		}
		if decodeErr != nil {
			t.Fatalf("invalid outgoing protocol: %v", decodeErr)
		}
		replies = append(replies, reply)
	}
	return result, err, replies
}

func TestConsumeFinalVersusCommentary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []packet
		want     string
	}{
		{"completed item", []packet{
			messagePacket(t, "thread-1", "turn-1", "commentary", "I will inspect your feeds."),
			messagePacket(t, "thread-1", "turn-1", "final_answer", "You have three unread articles."),
			messagePacket(t, "thread-1", "turn-1", "commentary", "Do not deliver this commentary."),
			completedPacket(t, "thread-1", "turn-1", "completed"),
		}, "You have three unread articles."},
		{"completion items", []packet{
			completedPacket(t, "thread-1", "turn-1", "completed",
				agentItem{Type: "agentMessage", Phase: "final_answer", Text: "Merhaba!"},
				agentItem{Type: "agentMessage", Phase: "commentary", Text: "Planning"}),
		}, "Merhaba!"},
		{"phase absent", []packet{
			messagePacket(t, "thread-1", "turn-1", "", "A finished answer."),
			completedPacket(t, "thread-1", "turn-1", "completed"),
		}, "A finished answer."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err, _ := consumePackets(t, nil, tc.messages...)
			if err != nil || result.Text != tc.want || result.ThreadID != "thread-1" {
				t.Fatalf("consume() = (%+v, %v), want thread-1 and %q", result, err, tc.want)
			}
		})
	}
}

func TestConsumePreservesOrderedAnswerItemsAndDeduplicatesCompletion(t *testing.T) {
	partial := agentItem{ID: "partial-1", Type: "agentMessage", Phase: "partial_answer", Text: "First part of the answer."}
	final := agentItem{ID: "final-1", Type: "agentMessage", Phase: "final_answer", Text: "Final part of the answer."}
	commentary := agentItem{ID: "commentary-1", Type: "agentMessage", Phase: "commentary", Text: "Still working."}
	itemEvent := func(item agentItem) packet {
		return eventPacket(t, "item/completed", map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": item})
	}
	for _, tc := range []struct {
		name  string
		items []agentItem
		want  string
	}{
		{"repeated items", []agentItem{partial, commentary, final, partial, final}, partial.Text + "\n\n" + final.Text},
		{"reversed completion keeps original order", []agentItem{final, partial}, partial.Text + "\n\n" + final.Text},
		{"same ID replaces text in place", []agentItem{final, {ID: partial.ID, Type: "agentMessage", Phase: "partial_answer", Text: "Updated first part."}}, "Updated first part.\n\n" + final.Text},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err, _ := consumePackets(t, nil,
				itemEvent(partial), itemEvent(commentary), itemEvent(final), itemEvent(partial),
				completedPacket(t, "thread-1", "turn-1", "completed", tc.items...),
			)
			if err != nil || result.Text != tc.want || result.ThreadID != "thread-1" {
				t.Fatalf("consume() = (%+v, %v), want ordered answer %q", result, err, tc.want)
			}
		})
	}
}

func TestConsumeRejectsUnsuccessfulOrEmptyFinal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []packet
		want     string
	}{
		{"failed", []packet{messagePacket(t, "thread-1", "turn-1", "final_answer", "partial"), completedPacket(t, "thread-1", "turn-1", "failed")}, "status failed"},
		{"interrupted", []packet{messagePacket(t, "thread-1", "turn-1", "final_answer", "partial"), completedPacket(t, "thread-1", "turn-1", "interrupted")}, "status interrupted"},
		{"empty", []packet{completedPacket(t, "thread-1", "turn-1", "completed")}, "empty"},
		{"whitespace", []packet{messagePacket(t, "thread-1", "turn-1", "final_answer", " \n\t"), completedPacket(t, "thread-1", "turn-1", "completed")}, "empty"},
		{"commentary only", []packet{messagePacket(t, "thread-1", "turn-1", "commentary", "I'll answer later"), completedPacket(t, "thread-1", "turn-1", "completed", agentItem{Type: "agentMessage", Phase: "commentary", Text: "Still planning"})}, "empty"},
		{"wrong thread message", []packet{messagePacket(t, "other-thread", "turn-1", "final_answer", "private"), completedPacket(t, "thread-1", "turn-1", "completed")}, "empty"},
		{"wrong turn message", []packet{messagePacket(t, "thread-1", "other-turn", "final_answer", "stale"), completedPacket(t, "thread-1", "turn-1", "completed")}, "empty"},
		{"non-agent item", []packet{completedPacket(t, "thread-1", "turn-1", "completed", agentItem{Type: "commandExecution", Text: "not an answer"})}, "empty"},
		{"oversized", []packet{messagePacket(t, "thread-1", "turn-1", "final_answer", strings.Repeat("ç", 35001)), completedPacket(t, "thread-1", "turn-1", "completed")}, "oversized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err, _ := consumePackets(t, nil, tc.messages...)
			if err == nil || !strings.Contains(err.Error(), tc.want) || result != (service.AgentResult{}) {
				t.Fatalf("consume() = (%+v, %v), want empty result and %q error", result, err, tc.want)
			}
		})
	}
}

func TestConsumeIgnoresOtherTurnCompletions(t *testing.T) {
	result, err, _ := consumePackets(t, nil,
		completedPacket(t, "other-thread", "turn-1", "failed"),
		completedPacket(t, "thread-1", "other-turn", "interrupted"),
		messagePacket(t, "thread-1", "turn-1", "final_answer", "Current answer"),
		completedPacket(t, "thread-1", "turn-1", "completed"),
	)
	if err != nil || result.Text != "Current answer" {
		t.Fatalf("consume() = (%+v, %v)", result, err)
	}
}

func TestConsumeToolCallIdentity(t *testing.T) {
	for _, tc := range []struct{ name, thread, turn, call, namespace string }{
		{"wrong thread", "other-thread", "turn-1", "call-1", ""},
		{"wrong turn", "thread-1", "other-turn", "call-1", ""},
		{"missing call ID", "thread-1", "turn-1", "", ""},
		{"namespace", "thread-1", "turn-1", "call-1", "external"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := &recordingAgentTools{}
			result, err, replies := consumePackets(t, tools, toolPacket(t, "1", tc.call, "rss_inventory", tc.thread, tc.turn, tc.namespace))
			if err == nil || !strings.Contains(err.Error(), "identity mismatch") || result != (service.AgentResult{}) {
				t.Fatalf("consume() = (%+v, %v), want identity mismatch", result, err)
			}
			if len(tools.calls) != 0 || len(replies) != 0 {
				t.Fatalf("unauthorized call executed or answered: calls=%v replies=%v", tools.calls, replies)
			}
		})
	}
}

func toolReply(t *testing.T, reply packet) (bool, string) {
	t.Helper()
	var result struct {
		Success      bool `json:"success"`
		ContentItems []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"contentItems"`
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.ContentItems) != 1 || result.ContentItems[0].Type != "inputText" {
		t.Fatalf("unexpected tool reply: %s", reply.Result)
	}
	return result.Success, result.ContentItems[0].Text
}

func TestConsumeUnknownToolRejected(t *testing.T) {
	for _, withTools := range []bool{false, true} {
		t.Run(fmt.Sprintf("tools=%t", withTools), func(t *testing.T) {
			stub := &recordingAgentTools{}
			var tools service.AgentTools
			if withTools {
				tools = stub
			}
			result, err, replies := consumePackets(t, tools,
				toolPacket(t, `"request-1"`, "call-1", "shell", "thread-1", "turn-1", ""),
				completedPacket(t, "thread-1", "turn-1", "completed", agentItem{Type: "agentMessage", Text: "That tool is unavailable."}),
			)
			if err != nil || result.Text == "" || len(stub.calls) != 0 || len(replies) != 1 {
				t.Fatalf("result=%+v err=%v calls=%v replies=%v", result, err, stub.calls, replies)
			}
			success, text := toolReply(t, replies[0])
			if success || text != "Tool is unavailable." || string(replies[0].ID) != `"request-1"` {
				t.Fatalf("unknown tool reply: %+v", replies[0])
			}
		})
	}
}

func TestConsumeToolFailureContinuesAndSanitizes(t *testing.T) {
	tools := &recordingAgentTools{call: func(ctx context.Context, name string, args json.RawMessage) (string, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("tool context has no deadline")
		}
		if name == "rss_inventory" {
			return "sensitive partial result", errors.New("private credential in upstream error")
		}
		return `{"items":[]}`, nil
	}}
	result, err, replies := consumePackets(t, tools,
		toolPacket(t, "1", "failed-call", "rss_inventory", "thread-1", "turn-1", ""),
		toolPacket(t, "2", "next-call", "rss_articles", "thread-1", "turn-1", ""),
		completedPacket(t, "thread-1", "turn-1", "completed", agentItem{Type: "agentMessage", Text: "Inventory failed; please retry."}),
	)
	if err != nil || result.Text != "Inventory failed; please retry." || len(replies) != 2 || len(tools.calls) != 2 {
		t.Fatalf("result=%+v err=%v calls=%v replies=%v", result, err, tools.calls, replies)
	}
	success, text := toolReply(t, replies[0])
	if success || !strings.Contains(text, "Tool failed") || strings.Contains(text, "sensitive") || strings.Contains(text, "credential") {
		t.Fatalf("unsafe failure response: %s", replies[0].Result)
	}
	if success, text := toolReply(t, replies[1]); !success || text != `{"items":[]}` {
		t.Fatalf("next tool call failed: %s", replies[1].Result)
	}
	if string(tools.args[0]) != `{"scope":"all_unread"}` {
		t.Fatalf("tool arguments changed: %s", tools.args[0])
	}
}

func TestConsumeDuplicateCallExecutesOnce(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%t", fail), func(t *testing.T) {
			tools := &recordingAgentTools{call: func(context.Context, string, json.RawMessage) (string, error) {
				if fail {
					return "", errors.New("upstream failed")
				}
				return `{"total":3}`, nil
			}}
			_, err, replies := consumePackets(t, tools,
				toolPacket(t, "10", "same-call", "rss_inventory", "thread-1", "turn-1", ""),
				toolPacket(t, "11", "same-call", "rss_inventory", "thread-1", "turn-1", ""),
				completedPacket(t, "thread-1", "turn-1", "completed", agentItem{Type: "agentMessage", Text: "Done"}),
			)
			if err != nil || len(tools.calls) != 1 || len(replies) != 2 {
				t.Fatalf("err=%v calls=%v replies=%v", err, tools.calls, replies)
			}
			if string(replies[0].ID) != "10" || string(replies[1].ID) != "11" || !bytes.Equal(replies[0].Result, replies[1].Result) {
				t.Fatalf("duplicate call did not replay its result under the new request ID: %+v", replies)
			}
		})
	}
}

func TestConsumeRejectsUnsupportedServerRequest(t *testing.T) {
	request := eventPacket(t, "item/commandExecution/requestApproval", map[string]any{"threadId": "thread-1"})
	request.ID = json.RawMessage("4")
	_, err, replies := consumePackets(t, nil, request,
		completedPacket(t, "thread-1", "turn-1", "completed", agentItem{Type: "agentMessage", Text: "No execution available."}),
	)
	if err != nil || len(replies) != 1 || replies[0].Error == nil || replies[0].Error.Code != -32601 || string(replies[0].ID) != "4" {
		t.Fatalf("err=%v replies=%+v, want unsupported-method response", err, replies)
	}
}

func TestConsumeMalformedEvent(t *testing.T) {
	_, err, _ := consumePackets(t, nil, packet{Method: "item/completed", Params: json.RawMessage(`{"threadId":`)})
	if err == nil || !strings.Contains(err.Error(), "invalid Codex event") {
		t.Fatalf("consume() error = %v", err)
	}
}

func TestConsumeContextCancelAndEOF(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%t", cancelled), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			failures := make(chan error, 1)
			want := error(io.EOF)
			if cancelled {
				cancel()
				want = context.Canceled
			} else {
				failures <- io.EOF
			}
			c := &connection{encoder: json.NewEncoder(io.Discard), packets: make(chan packet), errors: failures}
			result, err := (&Runtime{}).consume(ctx, c, service.AgentRequest{}, "thread-1", "turn-1")
			if !errors.Is(err, want) || result != (service.AgentResult{}) {
				t.Fatalf("consume() = (%+v, %v), want %v", result, err, want)
			}
		})
	}
}

func tokenUsagePacket(t *testing.T, thread, turn string, total *service.AgentTokenUsage) packet {
	t.Helper()
	var counters any
	if total != nil {
		counters = map[string]int{"inputTokens": total.Input, "outputTokens": total.Output, "totalTokens": total.Total}
	}
	return eventPacket(t, "thread/tokenUsage/updated", map[string]any{
		"threadId": thread, "turnId": turn,
		"tokenUsage": map[string]any{
			"total": counters,
			// Deliberately different: accounting must use cumulative total, not
			// the most recent model call's last counters.
			"last": map[string]int{"inputTokens": 7, "outputTokens": 3, "totalTokens": 10},
		},
	})
}

func consumeUsagePackets(t *testing.T, req service.AgentRequest, tracker *analytics.UsageTracker, messages ...packet) (service.AgentResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if tracker != nil {
		ctx = analytics.WithUsageTracker(ctx, tracker)
	}
	// Replay events buffered during turn/start before delivering transport EOF.
	// Keeping EOF separate prevents it racing usage and completion notifications.
	failures := make(chan error, 1)
	failures <- io.EOF
	c := &connection{encoder: json.NewEncoder(io.Discard), pending: messages, errors: failures}
	r := &Runtime{config: Config{Model: "usage-test-model"}}
	return r.consume(ctx, c, req, "thread-1", "turn-1")
}

func assertUsageRecord(t *testing.T, tracker *analytics.UsageTracker, want service.AgentTokenUsage) {
	t.Helper()
	records := tracker.Snapshot()
	if len(records) != 1 {
		t.Fatalf("usage records = %+v, want exactly one record", records)
	}
	got := records[0]
	if got.Provider != "Codex" || got.Model != "usage-test-model" || got.PromptTokens != want.Input || got.CompletionTokens != want.Output || got.TotalTokens != want.Total || got.LatencyMS < 0 {
		t.Fatalf("usage record = %+v, want Codex/usage-test-model with counters %+v and nonnegative latency", got, want)
	}
}

func TestConsumeTokenUsageCumulativeBaseline(t *testing.T) {
	baseline := service.AgentTokenUsage{Input: 1000, Output: 200, Total: 1200}
	latest := service.AgentTokenUsage{Input: 1300, Output: 260, Total: 1560}
	for _, tc := range []struct {
		name     string
		threadID string
		history  *service.AgentTokenUsage
		want     service.AgentTokenUsage
	}{
		{"resumed thread records difference", "thread-1", &baseline, service.AgentTokenUsage{Input: 300, Output: 60, Total: 360}},
		{"new thread ignores old history baseline", "", &baseline, latest},
		{"new thread without baseline", "", nil, latest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker := analytics.NewUsageTracker()
			req := service.AgentRequest{
				ThreadID: tc.threadID,
				History:  service.ConversationState{AgentThreadID: "thread-1", AgentTokenUsage: tc.history},
			}
			result, err := consumeUsagePackets(t, req, tracker,
				tokenUsagePacket(t, "thread-1", "turn-1", &service.AgentTokenUsage{Input: 1100, Output: 220, Total: 1320}),
				messagePacket(t, "thread-1", "turn-1", "final_answer", "Finished answer"),
				tokenUsagePacket(t, "thread-1", "turn-1", &latest),
				completedPacket(t, "thread-1", "turn-1", "completed"),
			)
			if err != nil || result.Text != "Finished answer" || result.ThreadID != "thread-1" || result.TokenUsage == nil || *result.TokenUsage != latest {
				t.Fatalf("consume() = (%+v, %v), want successful answer with cumulative usage %+v", result, err, latest)
			}
			assertUsageRecord(t, tracker, tc.want)
			if baseline != (service.AgentTokenUsage{Input: 1000, Output: 200, Total: 1200}) {
				t.Fatalf("persisted baseline was mutated: %+v", baseline)
			}
		})
	}
}

func TestConsumeResumedTokenUsageWaitsForKnownBaseline(t *testing.T) {
	latest := service.AgentTokenUsage{Input: 1300, Output: 260, Total: 1560}
	for _, status := range []string{"completed", "failed", "interrupted"} {
		t.Run(status, func(t *testing.T) {
			tracker := analytics.NewUsageTracker()
			req := service.AgentRequest{ThreadID: "thread-1", History: service.ConversationState{AgentThreadID: "thread-1"}}
			result, err := consumeUsagePackets(t, req, tracker,
				tokenUsagePacket(t, "thread-1", "turn-1", &service.AgentTokenUsage{Input: 1100, Output: 220, Total: 1320}),
				tokenUsagePacket(t, "thread-1", "turn-1", &latest),
				completedPacket(t, "thread-1", "turn-1", status, agentItem{Type: "agentMessage", Text: "Answer"}),
			)
			if records := tracker.Snapshot(); len(records) != 0 {
				t.Fatalf("unknown resumed baseline charged cumulative history or guessed a delta: %+v", records)
			}
			if status != "completed" {
				if err == nil || !strings.Contains(err.Error(), "status "+status) || result != (service.AgentResult{}) {
					t.Fatalf("consume() = (%+v, %v), want failed result for %s", result, err, status)
				}
				return
			}
			if err != nil || result.TokenUsage == nil || *result.TokenUsage != latest || result.ThreadID != "thread-1" || result.Text != "Answer" {
				t.Fatalf("consume() = (%+v, %v), want cumulative counters retained for next request", result, err)
			}
			// After the successful result is persisted, a later request can
			// charge only its own difference against the now-known baseline.
			req.History.AgentTokenUsage = result.TokenUsage
			next := service.AgentTokenUsage{Input: 1450, Output: 290, Total: 1740}
			result, err = consumeUsagePackets(t, req, tracker,
				tokenUsagePacket(t, "thread-1", "turn-1", &next),
				completedPacket(t, "thread-1", "turn-1", "completed", agentItem{Type: "agentMessage", Text: "Next answer"}),
			)
			if err != nil || result.TokenUsage == nil || *result.TokenUsage != next {
				t.Fatalf("next consume() = (%+v, %v)", result, err)
			}
			assertUsageRecord(t, tracker, service.AgentTokenUsage{Input: 150, Output: 30, Total: 180})
		})
	}
}

func TestConsumeTokenUsageRecordedOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ending []packet
		want   string
	}{
		{"failed completion", []packet{completedPacket(t, "thread-1", "turn-1", "failed")}, "status failed"},
		{"interrupted completion", []packet{completedPacket(t, "thread-1", "turn-1", "interrupted")}, "status interrupted"},
		{"empty final", []packet{completedPacket(t, "thread-1", "turn-1", "completed")}, "empty"},
		{"protocol EOF", nil, "EOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker := analytics.NewUsageTracker()
			req := service.AgentRequest{
				ThreadID: "thread-1",
				History:  service.ConversationState{AgentTokenUsage: &service.AgentTokenUsage{Input: 1000, Output: 200, Total: 1200}},
			}
			messages := append([]packet{tokenUsagePacket(t, "thread-1", "turn-1", &service.AgentTokenUsage{Input: 1300, Output: 260, Total: 1560})}, tc.ending...)
			result, err := consumeUsagePackets(t, req, tracker, messages...)
			if err == nil || !strings.Contains(err.Error(), tc.want) || result != (service.AgentResult{}) {
				t.Fatalf("consume() = (%+v, %v), want empty result and %q error", result, err, tc.want)
			}
			assertUsageRecord(t, tracker, service.AgentTokenUsage{Input: 300, Output: 60, Total: 360})
		})
	}
}

func TestConsumeMissingTokenUsageDoesNotFabricateZero(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []packet
	}{
		{"no event", nil},
		{"null total with last counters", []packet{tokenUsagePacket(t, "thread-1", "turn-1", nil)}},
		{"missing total", []packet{eventPacket(t, "thread/tokenUsage/updated", map[string]any{"threadId": "thread-1", "turnId": "turn-1", "tokenUsage": map[string]any{}})}},
		{"wrong thread", []packet{tokenUsagePacket(t, "other-thread", "turn-1", &service.AgentTokenUsage{Input: 100, Output: 20, Total: 120})}},
		{"wrong turn", []packet{tokenUsagePacket(t, "thread-1", "other-turn", &service.AgentTokenUsage{Input: 100, Output: 20, Total: 120})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, status := range []string{"completed", "failed"} {
				t.Run(status, func(t *testing.T) {
					tracker := analytics.NewUsageTracker()
					req := service.AgentRequest{
						ThreadID: "thread-1",
						History:  service.ConversationState{AgentTokenUsage: &service.AgentTokenUsage{Input: 1000, Output: 200, Total: 1200}},
					}
					messages := append(append([]packet(nil), tc.events...), completedPacket(t, "thread-1", "turn-1", status, agentItem{Type: "agentMessage", Text: "Answer"}))
					result, err := consumeUsagePackets(t, req, tracker, messages...)
					if (status == "completed" && err != nil) || (status == "failed" && err == nil) {
						t.Fatalf("completion status=%s, unexpected error %v", status, err)
					}
					if result.TokenUsage != nil || len(tracker.Snapshot()) != 0 {
						t.Fatalf("missing usage fabricated counters: result=%+v records=%+v", result, tracker.Snapshot())
					}
				})
			}
		})
	}
}

func TestConsumeTokenUsageIgnoresUnrelatedAndMissingUpdates(t *testing.T) {
	want := service.AgentTokenUsage{Input: 100, Output: 20, Total: 120}
	tracker := analytics.NewUsageTracker()
	result, err := consumeUsagePackets(t, service.AgentRequest{}, tracker,
		tokenUsagePacket(t, "thread-1", "turn-1", &want),
		tokenUsagePacket(t, "other-thread", "turn-1", &service.AgentTokenUsage{Input: 9000, Output: 900, Total: 9900}),
		tokenUsagePacket(t, "thread-1", "other-turn", &service.AgentTokenUsage{Input: 8000, Output: 800, Total: 8800}),
		tokenUsagePacket(t, "thread-1", "turn-1", nil),
		completedPacket(t, "thread-1", "turn-1", "completed", agentItem{Type: "agentMessage", Text: "Answer"}),
	)
	if err != nil || result.TokenUsage == nil || *result.TokenUsage != want {
		t.Fatalf("consume() = (%+v, %v), want cumulative usage %+v", result, err, want)
	}
	assertUsageRecord(t, tracker, want)
}

func TestConsumeTokenUsageWithoutTracker(t *testing.T) {
	want := service.AgentTokenUsage{Input: 100, Output: 20, Total: 120}
	result, err := consumeUsagePackets(t, service.AgentRequest{}, nil,
		tokenUsagePacket(t, "thread-1", "turn-1", &want),
		completedPacket(t, "thread-1", "turn-1", "completed", agentItem{Type: "agentMessage", Text: "Answer"}),
	)
	if err != nil || result.TokenUsage == nil || *result.TokenUsage != want {
		t.Fatalf("consume() = (%+v, %v), want cumulative usage even without a tracker", result, err)
	}
}

func TestConsumeTokenUsageCounterRegressionNotRecorded(t *testing.T) {
	for _, total := range []service.AgentTokenUsage{
		{Input: 99, Output: 30, Total: 129},
		{Input: 110, Output: 19, Total: 129},
		{Input: 100, Output: 20, Total: 119},
	} {
		t.Run(fmt.Sprintf("%d/%d/%d", total.Input, total.Output, total.Total), func(t *testing.T) {
			tracker := analytics.NewUsageTracker()
			req := service.AgentRequest{ThreadID: "thread-1", History: service.ConversationState{AgentTokenUsage: &service.AgentTokenUsage{Input: 100, Output: 20, Total: 120}}}
			result, err := consumeUsagePackets(t, req, tracker,
				tokenUsagePacket(t, "thread-1", "turn-1", &total),
				completedPacket(t, "thread-1", "turn-1", "completed", agentItem{Type: "agentMessage", Text: "Answer"}),
			)
			if err != nil || result.TokenUsage == nil || *result.TokenUsage != total || len(tracker.Snapshot()) != 0 {
				t.Fatalf("regressed counters must return native totals without a negative usage record: result=%+v err=%v records=%+v", result, err, tracker.Snapshot())
			}
		})
	}
}

func TestSafeErrorInfoWhitelist(t *testing.T) {
	for _, code := range []string{"contextWindowExceeded", "sessionBudgetExceeded", "usageLimitExceeded", "rateLimitExceeded", "flexUnavailable", "serverOverloaded", "unauthorized", "badRequest", "internalServerError", "sandboxError", "tooManyDenials"} {
		t.Run(code, func(t *testing.T) {
			raw, err := json.Marshal(code)
			if err != nil {
				t.Fatal(err)
			}
			if got := safeErrorInfo(raw); got != code {
				t.Fatalf("safeErrorInfo() = %q, want %q", got, code)
			}
		})
	}
	for _, category := range []string{"httpConnectionFailed", "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts"} {
		for _, status := range []int{100, 401, 429, 500, 599, 0, 99, 600} {
			t.Run(fmt.Sprintf("%s/%d", category, status), func(t *testing.T) {
				raw, err := json.Marshal(map[string]any{category: map[string]any{
					"httpStatusCode": status, "message": "secret upstream response", "additionalDetails": "private prompt and credential",
				}})
				if err != nil {
					t.Fatal(err)
				}
				want := category
				if status >= 100 && status <= 599 {
					want += fmt.Sprintf(" HTTP %d", status)
				}
				if got := safeErrorInfo(raw); got != want {
					t.Fatalf("safeErrorInfo() = %q, want only %q", got, want)
				}
			})
		}
	}
}

func TestSafeErrorInfoRedactsUntrustedDetails(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"missing", "", "details_redacted"},
		{"null", "null", "details_redacted"},
		{"malformed", `{"httpConnectionFailed":`, "details_redacted"},
		{"arbitrary string", `"private prompt and credential"`, "details_redacted"},
		{"whitelist prefix", `"unauthorized: secret API key"`, "details_redacted"},
		{"unknown category", `{"private upstream message":{"httpStatusCode":401}}`, "details_redacted"},
		{"array", `["unauthorized"]`, "details_redacted"},
		{"wrong status type", `{"httpConnectionFailed":{"httpStatusCode":"secret"}}`, "details_redacted"},
		{"missing status", `{"httpConnectionFailed":{"message":"secret","additionalDetails":"private prompt"}}`, "httpConnectionFailed"},
		{"null status", `{"responseStreamDisconnected":{"httpStatusCode":null,"message":"secret"}}`, "responseStreamDisconnected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeErrorInfo(json.RawMessage(tc.raw)); got != tc.want {
				t.Fatalf("safeErrorInfo() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConsumeFailedTurnRedactsErrorDetails(t *testing.T) {
	failed := eventPacket(t, "turn/completed", map[string]any{
		"threadId": "thread-1",
		"turn": map[string]any{
			"id": "turn-1", "status": "failed",
			"error": map[string]any{
				"message": "secret upstream message", "additionalDetails": "private user prompt",
				"codexErrorInfo": map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": 401, "message": "secret credential"}},
			},
		},
	})
	result, err, _ := consumePackets(t, nil, failed)
	if err == nil || err.Error() != "Codex turn ended with status failed (httpConnectionFailed HTTP 401)" || result != (service.AgentResult{}) {
		t.Fatalf("consume() = (%+v, %v), want only failed status and whitelisted error category/status", result, err)
	}
}

func TestConnectionRPC(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
		wantErr  string
	}{
		{"success", `{"id":1,"result":{"thread":{"id":"thread-1"}}}`, ""},
		{"RPC error", `{"id":1,"error":{"code":-32000,"message":"secret provider diagnostics"}}`, "RPC code -32000"},
		{"invalid response", `{"id":1,"result":"unexpected string"}`, "invalid Codex thread/start response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response packet
			if err := json.Unmarshal([]byte(tc.response), &response); err != nil {
				t.Fatal(err)
			}
			event := messagePacket(t, "thread-1", "turn-1", "commentary", "Pending event")
			packets := make(chan packet, 2)
			packets <- event
			packets <- response
			var output bytes.Buffer
			c := &connection{encoder: json.NewEncoder(&output), packets: packets, errors: make(chan error)}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var out struct{ Thread struct{ ID string } }
			err := c.rpc(ctx, "thread/start", map[string]string{"model": "test-model"}, &out)
			if tc.wantErr == "" {
				if err != nil || out.Thread.ID != "thread-1" {
					t.Fatalf("rpc() = (%+v, %v)", out, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("rpc() error = %v, want sanitized %q", err, tc.wantErr)
			}
			if !reflect.DeepEqual(c.pending, []packet{event}) {
				t.Fatalf("interleaved event lost: %+v", c.pending)
			}
			var request packet
			if err := json.Unmarshal(output.Bytes(), &request); err != nil {
				t.Fatal(err)
			}
			if string(request.ID) != "1" || request.Method != "thread/start" {
				t.Fatalf("unexpected request: %+v", request)
			}
		})
	}
}

type failingProtocolWriter struct{}

func (failingProtocolWriter) Write([]byte) (int, error) {
	return 0, errors.New("private transport failure")
}

func TestConnectionRPCTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
		want string
	}{
		{"write", "write", "transport write failed"},
		{"EOF", "eof", "EOF"},
		{"cancel", "cancel", "context canceled"},
		{"event overflow", "overflow", "event overflow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			packets := make(chan packet, 257)
			failures := make(chan error, 1)
			c := &connection{encoder: json.NewEncoder(io.Discard), packets: packets, errors: failures}
			switch tc.kind {
			case "write":
				c.encoder = json.NewEncoder(failingProtocolWriter{})
			case "eof":
				failures <- io.EOF
			case "cancel":
				cancel()
			case "overflow":
				for i := 0; i < 257; i++ {
					packets <- packet{Method: "unrelated/event"}
				}
			}
			if err := c.rpc(ctx, "initialize", nil, nil); err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private") {
				t.Fatalf("rpc() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunRejectsInvalidRequestsBeforeStartingProcess(t *testing.T) {
	for _, field := range []string{"team", "user", "channel", "thread", "empty input", "oversized input"} {
		t.Run(field, func(t *testing.T) {
			r, err := New(runtimeTestConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			req := runtimeTestRequest()
			want := "identity"
			switch field {
			case "team":
				req.Key.Team = ""
			case "user":
				req.Key.User = ""
			case "channel":
				req.Key.Channel = ""
			case "thread":
				req.Key.Thread = ""
			case "empty input":
				req.Input, want = " \n", "input"
			case "oversized input":
				req.Input, want = strings.Repeat("x", (128<<10)+1), "input"
			}
			// This executable could not speak the protocol if accidentally launched.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := r.Run(ctx, req); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("Run() error = %v, want %q", err, want)
			}
			if len(r.slots) != 0 {
				t.Fatal("invalid request leaked a concurrency slot")
			}
		})
	}
}

func TestRunCancellationWhileWaitingForSlot(t *testing.T) {
	cfg := runtimeTestConfig(t)
	cfg.MaxConcurrent = 1
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.slots <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Run(ctx, runtimeTestRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context cancellation", err)
	}
	if len(r.slots) != 1 {
		t.Fatal("cancelled waiter changed an occupied slot")
	}
}

// The wrapper supplies the test runner flags that Config.Binary cannot carry.
// Its mode is an argument, since Run deliberately strips inherited environment.
func helperRuntime(t *testing.T, mode string) *Runtime {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("helper launcher requires /bin/sh")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	cfg := runtimeTestConfig(t)
	cfg.Binary = filepath.Join(t.TempDir(), "codex-helper")
	cfg.Timeout = 5 * time.Second
	script := "#!/bin/sh\nexec " + quote(os.Args[0]) + " -test.run='^TestCodexHelper$' -- " + quote(mode) + " \"$@\"\n"
	if err := os.WriteFile(cfg.Binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type helperObservation struct {
	Env    map[string]string          `json:"env"`
	Args   []string                   `json:"args"`
	CWD    string                     `json:"cwd"`
	Start  map[string]json.RawMessage `json:"start"`
	Turn   map[string]json.RawMessage `json:"turn"`
	Method string                     `json:"method"`
}

func TestRunSubprocessConfiguration(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ambient-openai-key")
	t.Setenv("CODEX_HOME", "/ambient/desktop/login")
	t.Setenv("NUDGY_MODEL_API_KEY", "ambient-model-key")
	t.Setenv("NUDGE_PRIVATE_SENTINEL", "application-secret")
	for _, resumed := range []bool{false, true} {
		t.Run(fmt.Sprintf("resumed=%t", resumed), func(t *testing.T) {
			r := helperRuntime(t, "observe")
			req := runtimeTestRequest()
			req.Tools = &recordingAgentTools{}
			if resumed {
				req.ThreadID = "thread-1"
			}
			result, err := r.Run(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if result.ThreadID != "thread-1" || len(r.slots) != 0 {
				t.Fatalf("result=%+v occupied slots=%d", result, len(r.slots))
			}
			var got helperObservation
			if err := json.Unmarshal([]byte(result.Text), &got); err != nil {
				t.Fatal(err)
			}
			if got.Env["NUDGY_MODEL_API_KEY"] != r.config.APIKey || got.Env["OPENAI_API_KEY"] != "" || got.Env["NUDGE_PRIVATE_SENTINEL"] != "" {
				t.Fatalf("child inherited ambient secrets or lost explicit key: %v", got.Env)
			}
			home := got.Env["CODEX_HOME"]
			if filepath.Dir(home) != r.config.StateDir || len(filepath.Base(home)) != 64 {
				t.Fatalf("child state directory %q is not isolated under %q", home, r.config.StateDir)
			}
			// macOS may resolve a temporary directory through /private/var.
			wantCWD, err := filepath.EvalSymlinks(filepath.Join(home, "workspace"))
			if err != nil {
				t.Fatal(err)
			}
			if got.CWD != wantCWD {
				t.Fatalf("child cwd=%q, want %q", got.CWD, wantCWD)
			}
			if len(got.Args) < 3 || !reflect.DeepEqual(got.Args[:3], []string{"app-server", "--listen", "stdio://"}) {
				t.Fatalf("unexpected app-server arguments: %v", got.Args)
			}
			settings := make(map[string]bool)
			for i := 3; i+1 < len(got.Args); i += 2 {
				if got.Args[i] != "-c" {
					t.Fatalf("unexpected config flag %q", got.Args[i])
				}
				settings[got.Args[i+1]] = true
			}
			for _, setting := range []string{`model_provider="nudgy"`, `model_providers.nudgy.env_key="NUDGY_MODEL_API_KEY"`, `model_providers.nudgy.requires_openai_auth=false`, `web_search="disabled"`, `features.shell_tool=false`, `features.apps=false`, `features.plugins=false`} {
				if !settings[setting] {
					t.Errorf("missing isolation setting %s", setting)
				}
			}
			for key, want := range map[string]string{"model": r.config.Model, "modelProvider": "nudgy", "approvalPolicy": "never", "sandbox": "read-only"} {
				var value string
				if err := json.Unmarshal(got.Start[key], &value); err != nil || value != want {
					t.Errorf("thread parameter %s=%s, want %q", key, got.Start[key], want)
				}
			}
			if resumed {
				if got.Method != "thread/resume" || string(got.Start["threadId"]) != `"thread-1"` || string(got.Start["excludeTurns"]) != "true" {
					t.Fatalf("invalid resume request: %s %v", got.Method, got.Start)
				}
			} else {
				var definitions []service.ToolDefinition
				if err := json.Unmarshal(got.Start["dynamicTools"], &definitions); err != nil {
					t.Fatal(err)
				}
				want := req.Tools.Definitions()
				if got.Method != "thread/start" || len(definitions) != len(want) || string(got.Start["environments"]) != "[]" || string(got.Start["allowProviderModelFallback"]) != "false" {
					t.Fatalf("invalid start request: %s %v", got.Method, got.Start)
				}
				for i := range want {
					if definitions[i].Name != want[i].Name || definitions[i].Description != want[i].Description || len(definitions[i].InputSchema) == 0 {
						t.Errorf("tool definition %d lost metadata: %+v", i, definitions[i])
					}
				}
			}
			var input []struct{ Type, Text string }
			if err := json.Unmarshal(got.Turn["input"], &input); err != nil || len(input) != 1 || input[0].Type != "text" || input[0].Text != req.Input {
				t.Fatalf("incorrect turn input: %s (%v)", got.Turn["input"], err)
			}
		})
	}
}

func TestRunSubprocessProtocolFailures(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"eof", "connection closed before completion"},
		{"malformed", "invalid Codex protocol message"},
		{"rpc-error", "initialize rejected (RPC code -32001)"},
		{"missing-thread", "invalid Codex thread identity"},
		{"wrong-resumed-thread", "invalid Codex thread identity"},
		{"missing-turn", "missing Codex turn identity"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			r := helperRuntime(t, tc.mode)
			req := runtimeTestRequest()
			if tc.mode == "wrong-resumed-thread" {
				req.ThreadID = "expected-thread"
			}
			result, err := r.Run(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret") || result != (service.AgentResult{}) {
				t.Fatalf("Run() = (%+v, %v), want sanitized %q", result, err, tc.want)
			}
			if len(r.slots) != 0 {
				t.Fatal("protocol failure leaked a concurrency slot")
			}
		})
	}
}

// TestCodexHelper is executed only by helperRuntime's temporary shell wrapper.
// Exit directly to prevent the Go test runner's PASS line entering the protocol.
func TestCodexHelper(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	mode := os.Args[separator+1]
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	read := func() packet {
		var msg packet
		if decoder.Decode(&msg) != nil {
			os.Exit(2)
		}
		return msg
	}
	write := func(value any) {
		if encoder.Encode(value) != nil {
			os.Exit(2)
		}
	}
	reply := func(msg packet, result any) {
		write(map[string]any{"id": msg.ID, "result": result})
	}
	initialize := read()
	switch mode {
	case "eof":
		os.Exit(0)
	case "malformed":
		fmt.Fprintln(os.Stdout, "not-json")
		os.Exit(0)
	case "rpc-error":
		write(map[string]any{"id": initialize.ID, "error": map[string]any{"code": -32001, "message": "secret provider diagnostics"}})
		os.Exit(0)
	}
	if initialize.Method != "initialize" {
		os.Exit(3)
	}
	reply(initialize, map[string]any{})
	if read().Method != "initialized" {
		os.Exit(3)
	}
	start := read()
	threadID := "thread-1"
	if mode == "missing-thread" {
		threadID = ""
	}
	reply(start, map[string]any{"thread": map[string]string{"id": threadID}})
	turn := read()
	turnID := "turn-1"
	if mode == "missing-turn" {
		turnID = ""
	}
	reply(turn, map[string]any{"turn": map[string]string{"id": turnID}})
	observation := helperObservation{Env: make(map[string]string), Args: os.Args[separator+2:], Method: start.Method}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		observation.Env[key] = value
	}
	observation.CWD, _ = os.Getwd()
	if json.Unmarshal(start.Params, &observation.Start) != nil || json.Unmarshal(turn.Params, &observation.Turn) != nil {
		os.Exit(3)
	}
	text, err := json.Marshal(observation)
	if err != nil {
		os.Exit(3)
	}
	write(map[string]any{"method": "item/completed", "params": map[string]any{
		"threadId": threadID, "turnId": turnID,
		"item": map[string]string{"type": "agentMessage", "phase": "commentary", "text": "This must not be the final answer."},
	}})
	write(map[string]any{"method": "turn/completed", "params": map[string]any{
		"threadId": threadID,
		"turn":     map[string]any{"id": turnID, "status": "completed", "items": []map[string]string{{"type": "agentMessage", "phase": "final_answer", "text": string(text)}}},
	}})
	// Keep stdout open until the parent has consumed the final event and closed
	// stdin; otherwise EOF could race the final packet in connection.receive.
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}
