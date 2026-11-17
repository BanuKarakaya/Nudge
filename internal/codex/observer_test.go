package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"nudge/internal/analytics"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProviderObserverStreamsCapturesUsageAndRedacts(t *testing.T) {
	const terminal = `{"type":"response.completed","response":{"id":"gen-test","output":[{"type":"message","content":[{"text":"answer"}]}],"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":70},"output_tokens_details":{"reasoning_tokens":5},"cost":0.001}}}`
	stream := "event: response.completed\ndata: " + terminal + "\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-key" {
			t.Error("upstream key missing")
		}
		if r.URL.Path == "/models" {
			w.Write([]byte(`{"data":[]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(stream))
	}))
	defer upstream.Close()
	calls := make(chan analytics.ModelCall, 1)
	tracker := analytics.NewUsageTracker()
	ctx := analytics.WithModelReporter(analytics.WithUsageTracker(context.Background(), tracker), func(c analytics.ModelCall) { calls <- c })
	base, token, stop, err := observeProvider(ctx, Config{BaseURL: upstream.URL, ModelCatalogURL: upstream.URL + "/models", APIKey: "private-key", Model: "model", Timeout: time.Second * 5})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	req, _ := http.NewRequest("POST", base+"/responses", strings.NewReader(`{"model":"model","input":"private-key", "api_key":"another-secret"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(raw) != stream {
		t.Fatal("stream changed")
	}
	select {
	case c := <-calls:
		if c.Usage == nil || c.Usage.CachedTokens != 70 || c.Usage.ReasoningTokens != 5 || c.Usage.CostUSD == nil || *c.Usage.CostUSD != 0.001 || len(tracker.Snapshot()) != 1 {
			t.Fatalf("usage lost: %+v", c)
		}
		if strings.Contains(c.Request, "private-key") || strings.Contains(c.Request, "another-secret") || !strings.Contains(c.Request, "REDACTED") {
			t.Fatal("secret retained")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("call not observed")
	}
	for _, path := range []string{"/responses", "/elsewhere"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatal("unauthenticated loopback request accepted")
		}
	}
	req, _ = http.NewRequest("GET", base+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("catalog blocked")
	}
}
func TestObservedMissingCostAndIncompleteStream(t *testing.T) {
	c := analytics.ModelCall{Model: "m"}
	parseObservedResponse(&c, []byte(`{"usage":{"input_tokens":12,"output_tokens":2,"total_tokens":14}}`), "")
	if c.Usage == nil || c.Usage.CostUSD != nil {
		t.Fatal("unknown cost became zero")
	}
	c = analytics.ModelCall{}
	parseObservedResponse(&c, []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n"), "")
	if c.Usage != nil || c.ErrorCode == "" {
		t.Fatal("incomplete stream appeared complete")
	}
}

func TestObserverDistinguishesAbruptEOFAfterCompletion(t *testing.T) {
	for _, completed := range []bool{true, false} {
		t.Run(fmt.Sprintf("completed=%t", completed), func(t *testing.T) {
			stream := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
			if completed {
				stream += "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"done\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"total_tokens\":12}}}\n\n"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, buffered, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				// Missing the terminating HTTP chunk reproduces an interrupted
				// transport with or without a complete Responses event.
				fmt.Fprintf(buffered, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n", len(stream), stream)
				_ = buffered.Flush()
			}))
			defer upstream.Close()
			calls := make(chan analytics.ModelCall, 1)
			ctx := analytics.WithModelReporter(context.Background(), func(call analytics.ModelCall) { calls <- call })
			base, token, stop, err := observeProvider(ctx, Config{BaseURL: upstream.URL, APIKey: "synthetic", Model: "test", Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			req, _ := http.NewRequest("POST", base+"/responses", strings.NewReader(`{"model":"test","input":"hello"}`))
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || string(raw) != stream {
				t.Fatal("observer changed delivered bytes", err)
			}
			select {
			case call := <-calls:
				if completed && (call.ErrorCode != "" || call.Usage == nil || call.Usage.TotalTokens != 12) {
					t.Fatal("completed response misclassified", call.ErrorCode)
				}
				if !completed && call.ErrorCode != "provider_stream_interrupted" {
					t.Fatal("incomplete response appeared successful", call.ErrorCode)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("missing observation")
			}
		})
	}
}

func TestNativeObservedRoundTrip(t *testing.T) {
	binary := os.Getenv("NUDGY_TEST_CODEX_BINARY")
	if binary == "" {
		t.Skip("pinned native binary required")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Error("wrong upstream credential")
		}
		if r.URL.Path == "/models" {
			if r.URL.Query().Get("client_version") == "" {
				t.Error("catalog version dropped")
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"models":[]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		item := map[string]any{"type": "message", "id": "msg_observed", "role": "assistant", "phase": "final_answer", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "Observed answer.", "annotations": []any{}}}}
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_observed", "status": "in_progress", "output": []any{}}},
			map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_observed", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 100, "output_tokens": 20, "total_tokens": 120, "cost": 0.001, "input_tokens_details": map[string]any{"cached_tokens": 60}}}},
		} {
			raw, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
	}))
	defer upstream.Close()
	runtime, err := New(Config{Binary: binary, Model: "offline-observer", BaseURL: upstream.URL, ModelCatalogURL: upstream.URL + "/models", APIKey: "synthetic-key", StateDir: t.TempDir(), Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	tracker := analytics.NewUsageTracker()
	calls := make(chan analytics.ModelCall, 4)
	ctx := analytics.WithModelReporter(analytics.WithUsageTracker(context.Background(), tracker), func(c analytics.ModelCall) { calls <- c })
	result, err := runtime.Run(ctx, runtimeTestRequest())
	if err != nil || result.Text != "Observed answer." {
		t.Fatal("native observer failed", result, err)
	}
	usage := tracker.Snapshot()
	if len(usage) != 1 || usage[0].CachedTokens != 60 || usage[0].TotalTokens != 120 {
		t.Fatalf("double-counted or lost usage: %+v", usage)
	}
	select {
	case call := <-calls:
		if !strings.Contains(call.Response, "Observed answer") || call.Usage.CostUSD == nil {
			t.Fatal("missing provider detail")
		}
	default:
		t.Fatal("no trace")
	}
}
