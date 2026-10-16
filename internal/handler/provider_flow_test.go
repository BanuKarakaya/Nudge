package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"nudge/internal/ai"
	"nudge/internal/domain"
	"nudge/internal/feedbin"
	"nudge/internal/repository"
	"nudge/internal/service"
)

type flowConnections struct {
	repository.FeedbinConnectionRepository
	err error
}

func (f flowConnections) GetByUserID(context.Context, int64) (domain.FeedbinConnection, error) {
	return domain.FeedbinConnection{Email: "test", Password: "test"}, f.err
}

type flowFeedbin struct {
	entries []feedbin.Entry
	calls   int
	since   time.Time
}

func (f *flowFeedbin) ListUnreadSince(_ context.Context, _, _ string, since time.Time) ([]feedbin.Entry, error) {
	f.calls++
	f.since = since
	return f.entries, nil
}
func (*flowFeedbin) ListTaggings(context.Context, string, string) ([]feedbin.Tagging, error) {
	return nil, nil
}

type flowTransport func(*http.Request) (*http.Response, error)

func (f flowTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise real provider parsing, dispatch, data services, rendering and history.
// Only HTTP, the database and Slack delivery are replaced with test doubles.
func TestProviderToSlackConversationFlow(t *testing.T) {
	clock := time.Now()
	entries := &flowFeedbin{entries: []feedbin.Entry{
		{ID: 1, Title: "Bir", URL: "https://example.com/1", CreatedAt: clock},
		{ID: 2, Title: "İki", URL: "https://example.com/2", CreatedAt: clock},
		{ID: 3, Title: "Üç", URL: "https://example.com/3", CreatedAt: clock},
	}}
	outputs := []string{
		`{"action":"REPLY","reply":"İyiyim, teşekkürler! Sen nasılsın?","article_id":"","search_request":""}`,
		`{"action":"RSS_TODAY_STATS","reply":"","article_id":"","search_request":""}`,
		`{"action":"RSS_SUMMARY","reply":"" a}`, // exact production failure shape
		`{"action":"RSS_SUMMARY","reply":"","article_id":"","search_request":""}`,
		`{"articles":[{"id":"RSS-1","topic":"Birinci konu.","importance":"Birinci gerekçe."},{"id":"RSS-2","topic":"İkinci konu.","importance":"İkinci gerekçe."},{"id":"RSS-3","topic":"Üçüncü konu.","importance":"Üçüncü gerekçe."}]}`,
		`{"action":"SELECT_PREVIOUS","reply":"İkinci makaleyi seçerdim, kısa bir giriş sunuyor.","article_id":"PREVIOUS-2","search_request":""}`,
	}
	calls := 0
	model := ai.OpenAICompatibleClient{APIKey: "test", Model: "openrouter/free", BaseURL: "https://openrouter.ai/api/v1", HTTPClient: &http.Client{Transport: flowTransport(func(r *http.Request) (*http.Response, error) {
		if calls >= len(outputs) {
			t.Fatal("unexpected extra model request")
		}
		var payload struct {
			Messages       []struct{ Role, Content string }
			ResponseFormat json.RawMessage `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(payload.ResponseFormat), "json_schema") {
			t.Fatal("schema missing")
		}
		if calls == 0 && strings.Contains(payload.Messages[len(payload.Messages)-1].Content, "<@") {
			t.Fatal("mention leaked into request")
		}
		if calls == 5 {
			found := false
			for _, m := range payload.Messages {
				if m.Role == "assistant" && strings.Contains(m.Content, "https://example.com/2") {
					found = true
				}
			}
			if !found {
				t.Fatal("delivered summary missing from follow-up context")
			}
		}
		body, _ := json.Marshal(map[string]any{"model": "mock-free-model", "choices": []any{map[string]any{"message": map[string]any{"content": outputs[calls]}, "finish_reason": "stop"}}})
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}}
	sender := &conversationSender{}
	h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: sender, dialogue: model, ai: model, recommender: model, timezone: time.UTC, feedbinDigest: service.NewFeedbinDigestService(flowConnections{}, entries)}
	event := slackEventEnvelope{TeamID: "T"}
	event.Event.User, event.Event.Channel, event.Event.ThreadTS = "U", "C", "1.0"
	requests := []string{"<@B123> nasılsın", "bugün kaç RSS'im var", "bugünkü özetimi verir misin", "bunlardan sadece birini okuyabilsem hangisini seçerdin"}
	for _, request := range requests {
		event.Event.Text = request
		h.replyWithSummary(event)
	}
	if calls != 6 || len(sender.messages) != 4 {
		t.Fatalf("model calls=%d, replies=%v", calls, sender.messages)
	}
	if !strings.Contains(sender.messages[0], "İyiyim") || !strings.Contains(sender.messages[1], "*3 RSS'in*") {
		t.Fatalf("wrong greeting/count: %v", sender.messages[:2])
	}
	if strings.Count(sender.messages[2], "Neden önemli:") != 3 {
		t.Fatalf("not a complete summary: %s", sender.messages[2])
	}
	if strings.Count(sender.messages[3], "https://") != 1 || !strings.Contains(sender.messages[3], "https://example.com/2") {
		t.Fatalf("wrong follow-up: %s", sender.messages[3])
	}
	if entries.calls != 2 {
		t.Fatalf("follow-up refetched Feedbin: %d", entries.calls)
	}
}

func TestTodayCountWindowDeduplicationAndMissingConnection(t *testing.T) {
	now := time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC)
	since := lastFeedbinStart(now, time.UTC)
	f := &flowFeedbin{entries: []feedbin.Entry{
		{ID: 1, CreatedAt: since.Add(-time.Second)},
		{ID: 2, CreatedAt: since}, {ID: 2, CreatedAt: since},
		{ID: 3, CreatedAt: now}, {ID: 4, CreatedAt: now.Add(time.Second)},
	}}
	h := &SlackEventHandler{timezone: time.UTC, feedbinDigest: service.NewFeedbinDigestService(flowConnections{}, f), dialogue: dialogueFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		return service.ConversationDecision{Action: service.ConversationRSSTodayStats}, nil
	})}
	text, _, err := h.conversationReply(context.Background(), service.ConversationState{}, "bugün kaç rss", 1, now)
	if err != nil || !strings.Contains(text, "*2 RSS'in*") || !f.since.Equal(since) {
		t.Fatalf("%s %v since=%s", text, err, f.since)
	}
	h.feedbinDigest = service.NewFeedbinDigestService(flowConnections{err: pgx.ErrNoRows}, f)
	text, _, err = h.conversationReply(context.Background(), service.ConversationState{}, "bugün kaç rss", 1, now)
	if err == nil || !strings.Contains(text, "Feedbin hesabın henüz bağlı") {
		t.Fatalf("%s %v", text, err)
	}
}
