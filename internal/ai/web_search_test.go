package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"nudge/internal/service"
)

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
		var payload generateRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Tools) != 1 || payload.Tools[0].GoogleSearch == nil {
			t.Fatal("Google Search tool not enabled")
		}
		if payload.GenerationConfig.ResponseSchema != nil || payload.GenerationConfig.ResponseMimeType != "" {
			t.Fatal("search must not use the structured routing schema")
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

func TestConverseCanRequestWebSearchDespiteEarlierRSSConversation(t *testing.T) {
	var state service.ConversationState
	state.Remember("RSS öner", "<https://example.com/fridges|Fridges>", service.ConversationRSSSummary)
	state.Remember("AI agent makalesi bul", "İnternet arama yeteneğim yok.", service.ConversationReply)
	client := GeminiClient{APIKey: "test", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		var payload generateRequest
		_ = json.NewDecoder(r.Body).Decode(&payload)
		prompt := payload.Contents[0].Parts[0].Text
		if !strings.Contains(prompt, "WEB_SEARCH") || !strings.Contains(prompt, "bugünkü araç listesini esas al") || !strings.Contains(prompt, "RSSlerimden olmayan") {
			t.Fatal("missing current request/capabilities")
		}
		decision, _ := json.Marshal(service.ConversationDecision{Action: service.ConversationWebSearch, SearchRequest: "AI agent hakkında açık webden bir makale öner."})
		body, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": string(decision)}}}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}}
	decision, err := client.Converse(context.Background(), state, "Bana RSSlerimden olmayan AI agent ile ilgili internetten bir makale öner")
	if err != nil || decision.Action != service.ConversationWebSearch || decision.SearchRequest == "" {
		t.Fatalf("web action rejected: %+v %v", decision, err)
	}
}
