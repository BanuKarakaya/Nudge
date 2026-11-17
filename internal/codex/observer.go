package codex

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"nudge/internal/analytics"
)

// Each invocation gets a loopback-only, authenticated observer. It forwards
// exclusively to its configured provider, streams without buffering delivery,
// and never records HTTP headers or authentication credentials.
func observeProvider(ctx context.Context, cfg Config) (string, string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", nil, err
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		listener.Close()
		return "", "", nil, err
	}
	token := hex.EncodeToString(secret[:])
	upstream, _ := url.Parse(cfg.BaseURL)
	provider := upstream.Hostname()
	client := &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/models" && cfg.ModelCatalogURL != "" {
			req, _ := http.NewRequestWithContext(r.Context(), "GET", cfg.ModelCatalogURL, nil)
			if version := r.URL.Query().Get("client_version"); len(version) <= 40 {
				query := req.URL.Query()
				query.Set("client_version", version)
				req.URL.RawQuery = query.Encode()
			}
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
			resp, err := client.Do(req)
			if err != nil {
				http.Error(w, "catalog unavailable", http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, io.LimitReader(resp.Body, 8<<20))
			return
		}
		if r.Method != "POST" || r.URL.Path != "/responses" || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
		if err != nil {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		call := analytics.ModelCall{StartedAt: time.Now().UTC(), Provider: provider, Model: cfg.Model}
		var request struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(raw, &request) == nil && request.Model != "" {
			call.Model = request.Model
		}
		call.Request = boundedTrace(analytics.RedactJSON(raw, cfg.APIKey), &call.Truncated)
		defer func() {
			call.DurationMS = time.Since(call.StartedAt).Milliseconds()
			if call.Usage != nil {
				call.Usage.LatencyMS = call.DurationMS
				if tracker := analytics.UsageTrackerFromContext(ctx); tracker != nil {
					tracker.Add(*call.Usage)
				}
			}
			if call.ErrorCode != "" {
				analytics.Report(ctx, analytics.LiveEvent{Stage: "provider_failed", Model: call.Model, ErrorCode: call.ErrorCode, DurationMS: call.DurationMS})
			}
			analytics.ReportModel(ctx, call)
		}()
		req, err := http.NewRequestWithContext(r.Context(), "POST", strings.TrimRight(cfg.BaseURL, "/")+"/responses", bytes.NewReader(raw))
		if err != nil {
			call.ErrorCode = "request_invalid"
			http.Error(w, "invalid upstream", http.StatusBadGateway)
			return
		}
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream, application/json")
		// Stable session routing is useful for prompt caching, but contains no secrets.
		for _, key := range []string{"OpenAI-Beta", "OpenAI-Organization", "OpenAI-Project", "session_id"} {
			if value := r.Header.Get(key); value != "" {
				req.Header.Set(key, value)
			}
		}
		response, err := client.Do(req)
		if err != nil {
			call.ErrorCode = "provider_connection_failed"
			http.Error(w, "provider unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		call.Status = response.StatusCode
		for _, key := range []string{"Content-Type", "Retry-After", "X-Request-Id"} {
			if value := response.Header.Get(key); value != "" {
				w.Header().Set(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		capture := &traceBuffer{limit: 8 << 20}
		buffer := make([]byte, 32<<10)
		for {
			n, readErr := response.Body.Read(buffer)
			if n > 0 {
				capture.Write(buffer[:n])
				if _, err = w.Write(buffer[:n]); err != nil {
					call.ErrorCode = "client_disconnected"
					break
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					call.ErrorCode = "provider_stream_interrupted"
				}
				break
			}
		}
		call.Truncated = call.Truncated || capture.truncated
		parseObservedResponse(&call, capture.Bytes(), cfg.APIKey)
		if call.Status >= 400 && call.ErrorCode == "" {
			call.ErrorCode = "provider_http_error"
		}
	})}
	go func() { _ = server.Serve(listener) }()
	return "http://" + listener.Addr().String(), token, func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
		client.CloseIdleConnections()
	}, nil
}

type traceBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *traceBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if n > remaining {
		b.truncated = true
		p = p[:max(remaining, 0)]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
func boundedTrace(s string, truncated *bool) string {
	if len(s) > 120000 {
		*truncated = true
		s = s[:120000]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		return s + "\n… [kısaltıldı]"
	}
	return s
}
func parseObservedResponse(call *analytics.ModelCall, raw []byte, key string) {
	// A terminal Responses event carries the complete response and usage. Ignore
	// deltas and keep only this final object; failed HTTP bodies are redacted too.
	body := raw
	completed := false
	if !json.Valid(raw) {
		body = nil
		for _, line := range bytes.Split(raw, []byte("\n")) {
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			var event struct {
				Type     string          `json:"type"`
				Response json.RawMessage `json:"response"`
				Error    json.RawMessage `json:"error"`
			}
			if json.Unmarshal(data, &event) != nil {
				continue
			}
			if event.Type == "response.completed" || event.Type == "response.incomplete" || event.Type == "response.failed" {
				body = event.Response
				completed = event.Type == "response.completed"
				if event.Type != "response.completed" {
					call.ErrorCode = "provider_response_incomplete"
				}
			} else if event.Type == "error" {
				body = data
				completed = false
				call.ErrorCode = "provider_stream_error"
			}
		}
	}
	if len(body) == 0 {
		if call.ErrorCode == "" {
			call.ErrorCode = "provider_usage_unavailable"
		}
		return
	}
	call.Response = boundedTrace(analytics.RedactJSON(body, key), &call.Truncated)
	var response struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Usage  *struct {
			Input        int      `json:"input_tokens"`
			Output       int      `json:"output_tokens"`
			Total        int      `json:"total_tokens"`
			Cost         *float64 `json:"cost"`
			InputDetails struct {
				Cached int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails struct {
				Reasoning int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil {
		return
	}
	// Native clients may close their request immediately after the completed
	// event. Its cancellation can interrupt the upstream read after the full
	// response was forwarded. Preserve real failures and failed downstream
	// writes, but do not label an already completed response as a provider error.
	if call.ErrorCode == "provider_stream_interrupted" && call.Status >= 200 && call.Status < 300 && (completed || response.Status == "completed") {
		call.ErrorCode = ""
	}
	call.ID = response.ID
	if u := response.Usage; u != nil && u.Input >= 0 && u.Output >= 0 && u.Total >= 0 {
		cost := u.Cost
		if cost != nil && (*cost < 0 || math.IsInf(*cost, 0) || math.IsNaN(*cost)) {
			cost = nil
		}
		call.Usage = &analytics.AIUsage{Provider: call.Provider, Model: call.Model, PromptTokens: u.Input, CompletionTokens: u.Output, TotalTokens: u.Total, CachedTokens: min(max(u.InputDetails.Cached, 0), u.Input), ReasoningTokens: max(u.OutputDetails.Reasoning, 0), CostUSD: cost}
	}
}
