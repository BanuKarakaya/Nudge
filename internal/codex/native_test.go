package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in integration test against the pinned binary. Model traffic is served
// ONLY by a local fake Responses endpoint: no real credential or inference.
func TestNativeAppServerToolRoundTrip(t *testing.T) {
	binary := os.Getenv("NUDGY_TEST_CODEX_BINARY")
	if binary == "" {
		t.Skip("set NUDGY_TEST_CODEX_BINARY to the pinned Codex binary for the offline integration test")
	}
	var requests atomic.Int32
	var resultReceived atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/responses" {
			http.Error(w, "not found", 404)
			return
		}
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		n := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(value any) { data, _ := json.Marshal(value); fmt.Fprintf(w, "data: %s\n\n", data) }
		send(map[string]any{"type": "response.created", "response": map[string]any{"id": fmt.Sprintf("resp_%d", n), "status": "in_progress", "output": []any{}}})
		var item any
		if n == 1 {
			// Enforce the entire surface, not merely absence of one shell name.
			var exposed []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			}
			if err := json.Unmarshal(body["tools"], &exposed); err != nil {
				t.Errorf("invalid tools: %v", err)
			}
			// request_user_input is a native control request; the adapter refuses
			// it so Codex must ask in the final Slack answer instead of hanging.
			allowed := map[string]bool{"rss_inventory": true, "rss_articles": true, "bookmarks_list": true, "request_user_input": true}
			foundInventory := false
			for _, tool := range exposed {
				if tool.Type != "function" || !allowed[tool.Name] {
					t.Errorf("unexpected native tool: %s/%s", tool.Type, tool.Name)
				}
				foundInventory = foundInventory || tool.Name == "rss_inventory"
			}
			if !foundInventory {
				t.Error("RSS inventory tool missing")
			}
			item = map[string]any{"type": "function_call", "id": "fc_1", "name": "rss_inventory", "call_id": "call_1", "arguments": `{"scope":"all_unread"}`}
		} else {
			resultReceived.Store(strings.Contains(string(body["input"]), "function_call_output") && strings.Contains(string(body["input"]), "total"))
			item = map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "phase": "final_answer", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "Üç okunmamış RSS kaydın var.", "annotations": []any{}}}}
		}
		send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		send(map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("resp_%d", n), "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 20, "output_tokens": 5, "total_tokens": 25}}})
	}))
	defer server.Close()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	wrapper := filepath.Join(t.TempDir(), "native-codex")
	// Override the production endpoint after the normal arguments. The child
	// still receives only a fake credential and isolated CODEX_HOME.
	script := "#!/bin/sh\nexec " + quote(binary) + " \"$@\" -c " + quote(`model_providers.nudgy.base_url="`+server.URL+`"`) + "\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{Binary: wrapper, APIKey: "offline-test-key", Model: "offline-test-model", StateDir: t.TempDir(), Timeout: 25 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	req := runtimeTestRequest()
	tools := &recordingAgentTools{}
	req.Tools = tools
	result, err := r.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("native protocol roundtrip: %v (local model requests=%d)", err, requests.Load())
	}
	if result.Text != "Üç okunmamış RSS kaydın var." || result.ThreadID == "" || len(tools.calls) != 1 || requests.Load() != 2 || !resultReceived.Load() {
		t.Fatalf("roundtrip failed: result=%+v calls=%v requests=%d tool_result=%t", result, tools.calls, requests.Load(), resultReceived.Load())
	}
	// Run starts a new subprocess. The second turn must resume from disk rather
	// than create another thread or replay the previous tool call.
	req.ThreadID = result.ThreadID
	req.History.AgentTokenUsage = result.TokenUsage
	req.Input = "Teşekkürler"
	continued, err := r.Run(context.Background(), req)
	if err != nil || continued.ThreadID != result.ThreadID || continued.Text == "" || len(tools.calls) != 1 || requests.Load() != 3 {
		t.Fatalf("native resume failed: result=%+v err=%v calls=%v requests=%d", continued, err, tools.calls, requests.Load())
	}
}
