package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func groundedFixture(finish string) string {
	payload, _ := json.Marshal(map[string]any{
		"candidates": []any{map[string]any{
			"finishReason": finish,
			"content": map[string]any{"parts": []any{
				map[string]any{"text": "Hidden internal reasoning", "thought": true},
				map[string]any{"text": "Building effective agents\nAI agent mimarilerini ve ne zaman kullanılacaklarını açıklıyor."},
			}},
			"groundingMetadata": map[string]any{
				"webSearchQueries": []string{"AI agent architecture article"},
				"groundingChunks":  []any{map[string]any{"web": map[string]any{"uri": "https://example.com/agents", "title": "Building effective agents"}}},
				"searchEntryPoint": map[string]any{"renderedContent": `<div><a href="https://www.google.com/search?q=AI+agents&amp;source=test"><span>AI agents</span></a></div>`},
			},
		}},
	})
	return string(payload)
}

func TestSearchWebUsesGoogleSearchWithoutJSONSchema(t *testing.T) {
	calls := 0
	client := GeminiClient{APIKey: "secret-test", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload generateRequest
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Tools) != 1 || payload.Tools[0].GoogleSearch == nil {
			t.Fatal("Google Search tool not enabled")
		}
		var wire struct {
			GenerationConfig map[string]json.RawMessage `json:"generationConfig"`
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"responseSchema", "responseMimeType"} {
			if _, exists := wire.GenerationConfig[key]; exists {
				t.Fatalf("search must not send %s", key)
			}
		}
		if !strings.Contains(payload.Contents[0].Parts[0].Text, "AI agent hakkında bir makale") {
			t.Fatal("lost requested topic")
		}
		if strings.Contains(r.URL.String(), "secret-test") || r.Header.Get("x-goog-api-key") != "secret-test" {
			t.Fatal("API key must be in header, not URL/logs")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(groundedFixture("STOP")))}, nil
	})}}
	answer, err := client.SearchWeb(context.Background(), "AI agent hakkında bir makale öner ve neden önemli açıkla")
	if err != nil || calls != 1 {
		t.Fatalf("search failed: %s %v", answer, err)
	}
	for _, expected := range []string{"<https://example.com/agents|Building effective agents>", "Google arama önerileri", "<https://www.google.com/search?q=AI+agents&amp;source=test|AI agents>"} {
		if !strings.Contains(answer, expected) {
			t.Fatalf("missing source/suggestion %q in %s", expected, answer)
		}
	}
	if strings.Contains(answer, "Hidden internal") || strings.Contains(answer, "<div>") {
		t.Fatalf("non-user content leaked: %s", answer)
	}
}

func TestSearchWebRejectsUngroundedAndIncompleteAnswers(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"no evidence", `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"Made up article"}]}}]}`, 200},
		{"truncated", groundedFixture("MAX_TOKENS"), 200},
		{"quota", `{"error":{"message":"secret user data"}}`, 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := GeminiClient{APIKey: "test", HTTPClient: &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			answer, err := client.SearchWeb(context.Background(), "find article")
			if answer != "" || err == nil || strings.Contains(err.Error(), "secret user data") {
				t.Fatalf("invalid answer/error: %s %v", answer, err)
			}
			if calls > 2 || (tc.status == 429 && calls != 1) {
				t.Fatalf("unbounded or quota retry: %d", calls)
			}
		})
	}
}

func TestSearchWebRetriesIncompleteAnswer(t *testing.T) {
	calls := 0
	client := GeminiClient{APIKey: "test", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		var payload generateRequest
		_ = json.NewDecoder(r.Body).Decode(&payload)
		calls++
		finish := "MAX_TOKENS"
		if calls == 2 {
			finish = "STOP"
			if payload.GenerationConfig.MaxOutputTokens != 8192 {
				t.Fatal("retry must raise token limit")
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(groundedFixture(finish)))}, nil
	})}}
	answer, err := client.SearchWeb(context.Background(), "find article")
	if err != nil || answer == "" || calls != 2 {
		t.Fatalf("failed retry: %s %v (%d calls)", answer, err, calls)
	}
}

func TestGroundedLinksCannotInjectSlackMarkup(t *testing.T) {
	var response generateResponse
	_ = json.Unmarshal([]byte(groundedFixture("STOP")), &response)
	metadata := response.Candidates[0].GroundingMetadata
	metadata.GroundingChunks[0].Web.Title = "<!here>|bad"
	answer, err := renderGroundedAnswer("A useful article. <!channel> [Fake](https://invented.test/article)", metadata)
	if err != nil || strings.Contains(answer, "<!") || strings.Contains(answer, "invented.test") {
		t.Fatalf("unsafe rendering: %s %v", answer, err)
	}
	metadata.GroundingChunks[0].Web.URI = "javascript:alert(1)"
	if _, err := renderGroundedAnswer("Article", metadata); err == nil {
		t.Fatal("unsafe source accepted")
	}
}

func TestSearchWebRejectsMissingConfigurationOrRequestWithoutCallingAPI(t *testing.T) {
	for _, tc := range []struct {
		name, apiKey, request string
	}{
		{"missing key", "", "find article"},
		{"empty request", "test", ""},
		{"blank request", "test", " \n\t "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := GeminiClient{APIKey: tc.apiKey, HTTPClient: &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid search must not make an HTTP request")
				return nil, nil
			})}}
			answer, err := client.SearchWeb(context.Background(), tc.request)
			if answer != "" || err == nil {
				t.Fatalf("invalid search accepted: %q %v", answer, err)
			}
		})
	}
}
