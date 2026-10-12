package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"nudge/internal/service"
	"strings"
	"testing"
)

func TestConverseSendsHistoryAndValidatesPriorArticleSelection(t *testing.T) {
	var state service.ConversationState
	state.Remember("özetimi ver", "<https://example.com/1|One>\nKonu: Test.", service.ConversationRSSSummary)
	state.Remember("teşekkürler", "Rica ederim.", service.ConversationReply)
	request := "Verdiğin RSS özetinden sadece birini okuyabilirim, hangisini seçerdin?"
	calls := 0
	client := GeminiClient{APIKey: "test", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		var body generateRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		prompt := body.Contents[0].Parts[0].Text
		for _, part := range []string{"PREVIOUS-1", "https://example.com/1", "teşekkürler", request} {
			if !strings.Contains(prompt, part) {
				t.Fatalf("missing context %q", part)
			}
		}
		if body.GenerationConfig.ResponseMimeType != "application/json" {
			t.Fatal("missing decision schema")
		}
		id := "PREVIOUS-999"
		if calls > 0 {
			id = "PREVIOUS-1"
		}
		calls++
		answer, _ := json.Marshal(service.ConversationDecision{Action: service.ConversationSelect, ArticleID: id, Reply: "Kısa ve açıklayıcı olduğu için."})
		payload, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": string(answer)}}}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(payload))), Header: make(http.Header)}, nil
	})}}
	decision, err := client.Converse(context.Background(), state, request)
	if err != nil || decision.ArticleID != "PREVIOUS-1" || calls != 2 {
		t.Fatalf("expected validated retry: %+v %v calls=%d", decision, err, calls)
	}
}

func TestSummaryRequestsJSONSchema(t *testing.T) {
	client := GeminiClient{APIKey: "test", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		var request generateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.GenerationConfig.ResponseMimeType != "application/json" || request.GenerationConfig.ResponseSchema == nil {
			t.Fatal("summary must request structured JSON")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"{}"}]}}]}`)), Header: make(http.Header)}, nil
	})}}
	if _, err := client.Summarize(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGenerateRetriesTruncationWithoutReturningPartialText(t *testing.T) {
	for _, finishes := range [][]string{{"MAX_TOKENS", "STOP"}, {"MAX_TOKENS", "MAX_TOKENS"}} {
		calls := 0
		client := GeminiClient{APIKey: "test", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			finish := finishes[calls]
			calls++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"finishReason":"` + finish + `","content":{"parts":[{"text":"Complete."}]}}]}`)), Header: make(http.Header)}, nil
		})}}
		output, err := client.generate(context.Background(), "test", 100)
		if calls != 2 {
			t.Fatalf("expected one retry, got %d calls", calls)
		}
		if finishes[1] == "STOP" && (err != nil || output != "Complete.") {
			t.Fatalf("completed response rejected: %q %v", output, err)
		}
		if finishes[1] != "STOP" && (err == nil || output != "") {
			t.Fatalf("partial response returned: %q %v", output, err)
		}
	}
}
