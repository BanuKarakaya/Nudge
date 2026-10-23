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

func completionResponse(content, finish string) *http.Response {
	body, _ := json.Marshal(map[string]any{"model": "test-free-model", "choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": finish}}})
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}
}

func TestOpenRouterDecisionSchemaAndCompatibility(t *testing.T) {
	for _, id := range []string{`0`, `""`, `null`} {
		t.Run(id, func(t *testing.T) {
			client := OpenAICompatibleClient{APIKey: "test", Model: "openrouter/free", BaseURL: "https://openrouter.ai/api/v1", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				var payload deepSeekRequest
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if r.URL.Path != "/api/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test" {
					t.Fatal("incorrect endpoint/auth")
				}
				if payload.Model != "openrouter/free" || payload.Provider == nil || !payload.Provider.RequireParameters {
					t.Fatal("missing free route/parameter enforcement")
				}
				if payload.Provider.Sort != "latency" {
					t.Fatal("free route must prefer low latency endpoints")
				}
				if payload.ResponseFormat.Type != "json_schema" || !payload.ResponseFormat.JSONSchema.Strict {
					t.Fatal("missing strict decision schema")
				}
				props := payload.ResponseFormat.JSONSchema.Schema["properties"].(map[string]any)
				if props["article_id"].(map[string]any)["type"] != "string" {
					t.Fatal("ID schema must be string")
				}
				if props["needs_deepseek"].(map[string]any)["type"] != "boolean" {
					t.Fatal("missing complexity-routing field")
				}
				return completionResponse(`{"action":"REPLY","reply":"İyiyim, teşekkürler! Sen nasılsın?","article_id":`+id+`,"search_request":"","needs_deepseek":false}`, "stop"), nil
			})}}
			decision, err := client.Converse(context.Background(), service.ConversationState{}, "nasılsın")
			if err != nil || decision.Action != service.ConversationReply || !strings.Contains(decision.Reply, "İyiyim") {
				t.Fatalf("%+v %v", decision, err)
			}
		})
	}
}

func TestLatencyRoutingDoesNotChangePaidProviderPolicy(t *testing.T) {
	for _, baseURL := range []string{"https://openrouter.ai/api/v1", "https://api.deepseek.com"} {
		client := OpenAICompatibleClient{APIKey: "test", Model: "configured-paid-model", BaseURL: baseURL, HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			var payload deepSeekRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Model != "configured-paid-model" || (payload.Provider != nil && payload.Provider.Sort != "") {
				t.Fatal("changed paid model or provider price policy")
			}
			if baseURL == "https://api.deepseek.com" && payload.Provider != nil {
				t.Fatal("OpenRouter routing sent to another provider")
			}
			return completionResponse(`{"action":"REPLY","reply":"Merhaba!","article_id":"","search_request":""}`, "stop"), nil
		})}}
		if _, err := client.Converse(context.Background(), service.ConversationState{}, "selam"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompatibleDecisionRepairsInvalidOutputAndPreservesHistory(t *testing.T) {
	state := service.ConversationState{}
	state.Remember("özet", "<https://example.com/article|Başlık>\nKonu: Konu.", service.ConversationRSSSummary)
	state.Remember("teşekkürler", "Rica ederim!", service.ConversationReply)
	for _, invalid := range []string{`not json`, `{"action":"WHAT"}`, `{"action":"SELECT_PREVIOUS","article_id":"PREVIOUS-999","reply":"Bunu seçtim."}`, `{"action":"REPLY","reply":""}`} {
		calls := 0
		client := OpenAICompatibleClient{APIKey: "test", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			var payload deepSeekRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Messages[1].Role != "user" || payload.Messages[2].Role != "assistant" || !strings.Contains(payload.Messages[2].Content, "example.com/article") {
				t.Fatal("missing native conversation history")
			}
			calls++
			if calls == 1 {
				return completionResponse(invalid, "stop"), nil
			}
			return completionResponse("```json\n"+`{"action":"SELECT_PREVIOUS","article_id":"PREVIOUS-1","reply":"Kısa ve faydalı."}`+"\n```", "stop"), nil
		})}}
		decision, err := client.Converse(context.Background(), state, "sadece birini seçsen?")
		if err != nil || calls != 2 || decision.ArticleID != "PREVIOUS-1" {
			t.Fatalf("%+v %v calls=%d", decision, err, calls)
		}
	}
}

func TestCompatibleRejectsInvalidSelectionAfterBoundedRetry(t *testing.T) {
	calls := 0
	client := OpenAICompatibleClient{APIKey: "test", HTTPClient: &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return completionResponse(`{"action":"SELECT_PREVIOUS","article_id":"PREVIOUS-99","reply":"Test."}`, "stop"), nil
	})}}
	_, err := client.Converse(context.Background(), service.ConversationState{}, "birini seç")
	if err == nil || calls != 2 {
		t.Fatalf("unvalidated selection: %v calls=%d", err, calls)
	}
}

func TestReplyGroundingGuardReroutesFabricatedRSSHeadlines(t *testing.T) {
	client := OpenAICompatibleClient{APIKey: "test", Model: "openrouter/free", BaseURL: "https://openrouter.ai/api/v1", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		var payload deepSeekRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.ResponseFormat == nil || payload.ResponseFormat.Type != "json_schema" {
			t.Fatal("expected a structured response")
		}
		if payload.ResponseFormat.JSONSchema == nil {
			t.Fatal("missing structured response schema")
		}
		if payload.ResponseFormat.JSONSchema.Name == "reply_grounding_review" {
			if payload.MaxTokens != 1000 || len(payload.Messages) != 2 || !strings.Contains(payload.Messages[0].Content, "Taslak yanıt") || !strings.Contains(payload.Messages[1].Content, "[Başlık 1]") {
				t.Fatal("review must check the untrusted draft with a bounded response")
			}
			return completionResponse(`{"action":"RSS_TITLES","supported":false,"rss_scope":"","rss_continue":false,"search_request":""}`, "stop"), nil
		}
		return completionResponse(`{"action":"REPLY","reply":"Elbette! 1. [Başlık 1] — [Başlık 2]","article_id":"","search_request":"","needs_deepseek":false}`, "stop"), nil
	})}}
	decision, err := client.Converse(context.Background(), service.ConversationState{}, "bana rss titlelarımı verebilir misin bugünki")
	if err != nil || decision.Action != service.ConversationRSSTitles || decision.Reply != "" {
		t.Fatalf("fabricated response was not discarded and rerouted: %+v %v", decision, err)
	}
}

func TestReplyGroundingGuardPreservesHistoryBasedAnswers(t *testing.T) {
	state := service.ConversationState{}
	state.Remember("özet", "<https://example.com/article|Gerçek başlık>\nKonu: Gerçek konu.", service.ConversationRSSSummary)
	calls := 0
	client := OpenAICompatibleClient{APIKey: "test", Model: "openrouter/free", BaseURL: "https://openrouter.ai/api/v1", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		var payload deepSeekRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		calls++
		if payload.ResponseFormat.JSONSchema.Name == "reply_grounding_review" {
			if !strings.Contains(payload.Messages[1].Content, "Gerçek başlık") {
				t.Fatal("review must receive conversation context")
			}
			return completionResponse(`{"action":"REPLY","supported":true,"rss_scope":"","rss_continue":false,"search_request":""}`, "stop"), nil
		}
		return completionResponse(`{"action":"REPLY","reply":"Gerçek başlığı seçerdim; ilgini çekiyor.","article_id":"","search_request":""}`, "stop"), nil
	})}}
	decision, err := client.Converse(context.Background(), state, "önceki başlığı neden seçtin?")
	if err != nil || decision.Action != service.ConversationReply || calls != 2 {
		t.Fatalf("history-based reply was rejected: %+v %v calls=%d", decision, err, calls)
	}
}

func TestCompatibleSummaryAndRecommendationMatchServiceContract(t *testing.T) {
	input := "[RSS-1]\nBaşlık: Bir\nURL: https://example.com/1\n[RSS-2]\nBaşlık: İki\nURL: https://example.com/2\n[RSS-3]\nBaşlık: Üç\nURL: https://example.com/3"
	article := `{"id":"RSS-1","topic":"Konu.","importance":"Önemli."}`
	summary := `{"articles":[` + article + `,{"id":"RSS-2","topic":"Konu.","importance":"Önemli."},{"id":"RSS-3","topic":"Konu.","importance":"Önemli."}]}`
	client := OpenAICompatibleClient{APIKey: "test", Model: "openrouter/free", BaseURL: "https://openrouter.ai/api/v1", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		var payload deepSeekRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.ResponseFormat == nil || payload.ResponseFormat.Type != "json_schema" {
			t.Fatal("RSS response must be structured")
		}
		if payload.ResponseFormat.JSONSchema.Name == "rss_summary" {
			return completionResponse(summary, "stop"), nil
		}
		return completionResponse(article, "stop"), nil
	})}}
	raw, err := client.Summarize(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if rendered, ok := service.RenderFeedbinAISummary(raw, input); !ok || strings.Count(rendered, "Neden önemli:") != 3 {
		t.Fatalf("summary rejected: %s", raw)
	}
	raw, err = client.Recommend(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := service.RenderFeedbinRecommendation(raw, input); !ok {
		t.Fatalf("recommendation rejected: %s", raw)
	}
}

func TestCompatibleNeverDeliversTruncatedOutput(t *testing.T) {
	for _, finalFinish := range []string{"stop", "length"} {
		calls := 0
		client := OpenAICompatibleClient{APIKey: "test", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			var payload deepSeekRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			calls++
			if calls == 1 {
				return completionResponse(`{"articles":[`, "length"), nil
			}
			if payload.MaxTokens != 8000 {
				t.Fatal("retry must allow completion")
			}
			return completionResponse(`{"articles":[]}`, finalFinish), nil
		})}}
		text, err := client.Summarize(context.Background(), "test")
		if calls != 2 || (finalFinish == "length" && (err == nil || text != "")) || (finalFinish == "stop" && err != nil) {
			t.Fatalf("%q %v calls=%d", text, err, calls)
		}
	}
}

func TestCompatibleQuotaFailureDoesNotRetryOrLeakProviderBody(t *testing.T) {
	calls := 0
	client := OpenAICompatibleClient{APIKey: "test", HTTPClient: &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"error":"private prompt or key"}`))}, nil
	})}}
	_, err := client.Converse(context.Background(), service.ConversationState{}, "selam")
	if err == nil || calls != 1 || strings.Contains(err.Error(), "private") {
		t.Fatalf("%v calls=%d", err, calls)
	}
}
