package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Explicit opt-in ONLY: sends synthetic prompts to two paid OpenRouter models.
// The local proxy enforces a global eight-request budget, 32 KiB inputs and
// 1024 output tokens/request, with no automatic test-level retries. Never log
// credentials, raw HTTP errors, request bodies or native stderr.
// This probes functional turns, not rollout readiness: catalog_compatible=false
// is a separate blocking deployment finding even when those turns pass.
func TestLiveOpenRouterHarness(t *testing.T) {
	if os.Getenv("NUDGY_LIVE_OPENROUTER_TEST") != "1" {
		t.Skip("paid test disabled; requires explicit approval and NUDGY_LIVE_OPENROUTER_TEST=1")
	}
	binary := os.Getenv("NUDGY_TEST_CODEX_BINARY")
	if binary == "" {
		t.Fatal("pinned Codex binary required")
	}
	env, err := os.ReadFile("../../.env.local")
	if err != nil {
		t.Fatal("local credential file unavailable")
	}
	key := ""
	for _, line := range strings.Split(string(env), "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && strings.TrimSpace(strings.TrimPrefix(name, "export ")) == "OPENROUTER_API_KEY" {
			key = strings.Trim(strings.TrimSpace(value), "\"'")
		}
	}
	if !strings.HasPrefix(key, "sk-or-") || strings.ContainsAny(key, " \n\r\t") {
		t.Fatal("local credential format invalid; value withheld")
	}
	const simple = "deepseek/deepseek-v4.1-flash-1017"
	const complex = "openai/gpt-6-luna"
	client := &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var mu sync.Mutex
	var requests, catalogRequests, completed int
	var cost float64
	var models []string
	var catalogCompatible bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-test-proxy" {
			http.Error(w, "unauthorized", 401)
			return
		}
		var input []byte
		if r.Method == "POST" && r.URL.Path == "/responses" {
			var readErr error
			input, readErr = io.ReadAll(io.LimitReader(r.Body, (32<<10)+1))
			if readErr != nil || len(input) > 32<<10 {
				http.Error(w, "input cap", 413)
				return
			}
			var body map[string]any
			if json.Unmarshal(input, &body) != nil || (body["model"] != simple && body["model"] != complex) {
				http.Error(w, "model denied", 400)
				return
			}
			mu.Lock()
			if requests >= 8 {
				mu.Unlock()
				http.Error(w, "test request budget exhausted", 429)
				return
			}
			requests++
			n := requests
			mu.Unlock()
			body["max_output_tokens"] = 1024
			body["store"] = false
			input, _ = json.Marshal(body)
			t.Logf("live request=%d model=%s input_bytes=%d", n, body["model"], len(input))
		} else if r.Method == "GET" && r.URL.Path == "/models" {
			mu.Lock()
			catalogRequests++
			mu.Unlock()
		} else {
			http.Error(w, "test route denied", 404)
			return
		}
		upstream, err := http.NewRequestWithContext(r.Context(), r.Method, "https://openrouter.ai/api/v1"+r.URL.RequestURI(), bytes.NewReader(input))
		if err != nil {
			http.Error(w, "request construction failed", 500)
			return
		}
		upstream.Header.Set("Authorization", "Bearer "+key)
		upstream.Header.Set("Content-Type", "application/json")
		upstream.Header.Set("User-Agent", r.UserAgent())
		if origin := r.Header.Get("Originator"); origin != "" {
			upstream.Header.Set("Originator", origin)
		}
		started := time.Now()
		resp, err := client.Do(upstream)
		if err != nil {
			http.Error(w, "upstream request failed", 502)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Logf("live upstream HTTP %d", resp.StatusCode)
			http.Error(w, "upstream rejected test", resp.StatusCode)
			return
		}
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		if r.Method == "GET" {
			body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			if err != nil {
				http.Error(w, "catalog read failed", 502)
				return
			}
			var catalog struct {
				Models []struct {
					Slug string `json:"slug"`
				} `json:"models"`
			}
			_ = json.Unmarshal(body, &catalog)
			found := map[string]bool{}
			for _, m := range catalog.Models {
				found[m.Slug] = true
			}
			mu.Lock()
			catalogCompatible = found[simple] && found[complex]
			mu.Unlock()
			_, _ = w.Write(body)
			return
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 4096), 2<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var event struct {
					Type     string `json:"type"`
					Response struct {
						Model string `json:"model"`
						Usage struct {
							Cost   float64 `json:"cost"`
							Input  int     `json:"input_tokens"`
							Output int     `json:"output_tokens"`
						} `json:"usage"`
					} `json:"response"`
				}
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && event.Type == "response.completed" {
					mu.Lock()
					completed++
					cost += event.Response.Usage.Cost
					models = append(models, event.Response.Model)
					mu.Unlock()
					t.Logf("live completed model=%s input=%d output=%d cost_usd=%.8f duration_ms=%d", event.Response.Model, event.Response.Usage.Input, event.Response.Usage.Output, event.Response.Usage.Cost, time.Since(started).Milliseconds())
				}
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer proxy.Close()
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		t.Logf("live summary: requests=%d completed=%d reported_cost_usd=%.8f catalog_requests=%d catalog_compatible=%t", requests, completed, cost, catalogRequests, catalogCompatible)
	}()
	r, err := New(Config{Binary: binary, Model: simple, ComplexModel: complex, BaseURL: proxy.URL, ModelCatalogURL: proxy.URL + "/models", APIKey: "local-test-proxy", StateDir: t.TempDir(), Timeout: 60 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	req := runtimeTestRequest()
	tools := &recordingAgentTools{}
	req.Tools = tools
	req.Input = "Kaç okunmamış RSS kaydım var? Sadece sayıyı söyle."
	result, err := r.Run(ctx, req)
	if err != nil || !strings.Contains(result.Text, "3") || len(tools.calls) == 0 {
		t.Fatalf("native RSS test: err=%v answer_contains_count=%t tools=%d", err, strings.Contains(result.Text, "3"), len(tools.calls))
	}
	t.Log("native RSS tool roundtrip passed")
	req.ThreadID = result.ThreadID
	req.History.AgentTokenUsage = result.TokenUsage
	req.Input = "Teşekkürler. Çok kısa yanıt ver."
	thanks, err := r.Run(ctx, req)
	if err != nil || strings.TrimSpace(thanks.Text) == "" || thanks.ThreadID != result.ThreadID {
		t.Fatalf("native thanks/resume failed: %v", err)
	}
	t.Log("native thanks and persistent thread resume passed")
	req.ThreadID = thanks.ThreadID
	req.History.AgentTokenUsage = thanks.TokenUsage
	req.Input = "Go dilinde iki int toplayan Add(a, b int) int fonksiyonunu ve onun için tek bir unit test yaz. Çok kısa tut; dosya çalıştırma."
	code, err := r.Run(ctx, req)
	if err != nil || !strings.Contains(code.Text, "func Add") {
		t.Fatalf("native coding route failed: err=%v includes_function=%t", err, strings.Contains(code.Text, "func Add"))
	}
	mu.Lock()
	sawLuna := false
	for _, model := range models {
		sawLuna = sawLuna || model == complex
	}
	mu.Unlock()
	if !sawLuna {
		t.Fatal("coding answered without observed Luna completion")
	}
	t.Log("native DeepSeek-to-Luna handoff and final code answer passed")
}
