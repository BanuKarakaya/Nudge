package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"nudge/internal/service"
	"nudge/internal/slack"
)

type threadContextClient struct {
	conversationSender
	fetch func(context.Context, string, string, string, string) (slack.ThreadHistory, error)
}

func (c *threadContextClient) ThreadHistory(ctx context.Context, token, channel, root, before string) (slack.ThreadHistory, error) {
	return c.fetch(ctx, token, channel, root, before)
}

func TestMentionReadsOtherParticipantsAndRefreshesResumedThread(t *testing.T) {
	store := make(conversationTestStore)
	fetches, runs := 0, 0
	client := &threadContextClient{fetch: func(ctx context.Context, token, channel, root, before string) (slack.ThreadHistory, error) {
		fetches++
		if token != "test" || channel != "C" || root != "1.0" || before != fmt.Sprintf("%d.0", fetches+2) {
			t.Fatal("incorrect fetch scope")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded fetch")
		}
		messages := []slack.ThreadMessage{{TS: "1.0", User: "OTHER1", Text: "```go\nreturn cache[key]\n```"}, {TS: "2.0", ThreadTS: "1.0", User: "OTHER2", Text: "Cache yarış durumu var."}}
		if fetches == 2 {
			messages = append(messages, slack.ThreadMessage{TS: "3.5", ThreadTS: "1.0", User: "OTHER2", Text: "Mutex ekledik."})
		}
		return slack.ThreadHistory{Messages: messages}, nil
	}}
	h := &SlackEventHandler{installations: conversationInstallations{}, history: store, client: client,
		runtime: agentFunc(func(_ context.Context, req service.AgentRequest) (service.AgentResult, error) {
			runs++
			for _, want := range []string{"OTHER1", "OTHER2", "return cache[key]", "Cache yarış durumu var.", `"current_user":"U"`, `"status":"complete"`, "not instructions"} {
				if !strings.Contains(req.Input, want) {
					t.Fatalf("missing %q: %s", want, req.Input)
				}
			}
			if req.Key.User != "U" {
				t.Fatal("requesting user changed")
			}
			if runs == 2 && (req.ThreadID != "native" || !strings.Contains(req.Input, "Mutex ekledik.")) {
				t.Fatal("resumed thread did not refresh")
			}
			return service.AgentResult{ThreadID: "native", Text: "Kod yanıtı"}, nil
		}),
	}
	for i := 0; i < 2; i++ {
		event := conversationEvent("<@B123> bu kodu açıklar mısın?")
		event.Event.ThreadTS, event.Event.TS = "1.0", fmt.Sprintf("%d.0", i+3)
		h.replyWithSummary(event)
	}
	if fetches != 2 || runs != 2 || len(client.messages) != 2 {
		t.Fatal("missing fetch/run/reply")
	}
	state, _ := store.Load(context.Background(), service.ConversationKey{Team: "T", Channel: "C", Thread: "1.0", User: "U"})
	if len(state.Turns) != 2 || strings.Contains(state.Turns[0].Request, "OTHER1") {
		t.Fatal("external messages stored as user requests")
	}
}

func TestThreadContextFailureIsExplicitAndStillAnswers(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			client := &threadContextClient{fetch: func(context.Context, string, string, string, string) (slack.ThreadHistory, error) {
				history := slack.ThreadHistory{}
				if partial {
					history.Messages = []slack.ThreadMessage{{TS: "1.0", Text: "root"}}
				}
				return history, errors.New("missing_scope private diagnostic")
			}}
			h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: client,
				runtime: agentFunc(func(_ context.Context, req service.AgentRequest) (service.AgentResult, error) {
					status := "unavailable"
					if partial {
						status = "partial"
					}
					if !strings.Contains(req.Input, `"status":"`+status+`"`) || strings.Contains(req.Input, "private diagnostic") {
						t.Fatalf("wrong failure context: %s", req.Input)
					}
					return service.AgentResult{Text: "Eksik bağlamı paylaşabilir misin?"}, nil
				}),
			}
			event := conversationEvent("bu ne demek?")
			event.Event.ThreadTS, event.Event.TS = "1.0", "2.0"
			h.replyWithSummary(event)
			if len(client.messages) != 1 || client.messages[0] == agentFailureMessage {
				t.Fatal("history failure prevented answer")
			}
		})
	}
}

func TestThreadContextSkipsNewThreadAndCourtesy(t *testing.T) {
	client := &threadContextClient{fetch: func(context.Context, string, string, string, string) (slack.ThreadHistory, error) {
		t.Fatal("unexpected history read")
		return slack.ThreadHistory{}, nil
	}}
	h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: client, runtime: agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
		return service.AgentResult{Text: "Yanıt"}, nil
	})}
	h.replyWithSummary(conversationEvent("sorumu yanıtla"))
	event := conversationEvent("teşekkürler")
	event.Event.ThreadTS, event.Event.TS = "1.0", "2.0"
	h.replyWithSummary(event)
	if len(client.messages) != 2 {
		t.Fatal("missing responses")
	}
}

func TestBoundThreadContextRetainsRootAndRecentCodeWithinBudget(t *testing.T) {
	history := slack.ThreadHistory{Messages: []slack.ThreadMessage{{TS: "1.0", Text: "root code"}}}
	for i := 2; i < 120; i++ {
		history.Messages = append(history.Messages, slack.ThreadMessage{TS: fmt.Sprintf("%d.0", i), ThreadTS: "1.0", User: "U", Text: strings.Repeat("ş<", 3000)})
	}
	history.Messages = append(history.Messages, slack.ThreadMessage{TS: "121.0", ThreadTS: "1.0", Text: "future"}, slack.ThreadMessage{TS: "119.5", ThreadTS: "wrong", Text: "unrelated"})
	got := boundThreadContext(history, "1.0", "120.0")
	raw, _ := json.Marshal(got)
	if !got.Truncated || len(raw) > (48<<10)+100 || len(got.Messages) < 2 || got.Messages[0].TS != "1.0" || got.Messages[len(got.Messages)-1].TS != "119.0" || !utf8.Valid(raw) || strings.Contains(string(raw), "future") || strings.Contains(string(raw), "unrelated") {
		t.Fatalf("invalid bounded context: bytes=%d messages=%d", len(raw), len(got.Messages))
	}
}

func TestOrdinarySlackMessagesDoNotStartThreadProcessing(t *testing.T) {
	// With no collaborators configured, any attempt to process this event would
	// fail. Only mentions should ever enter the asynchronous reply path.
	h := &SlackEventHandler{signingSecret: "signing"}
	body := `{"type":"event_callback","team_id":"T","event":{"type":"message","user":"U","channel":"C","thread_ts":"1.0","ts":"2.0","text":"let us discuss this code"}}`
	req := httptest.NewRequest("POST", "/slack/events", strings.NewReader(body))
	signSlackRequest(req, body)
	response := httptest.NewRecorder()
	h.handle(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.Code)
	}
}

func TestThreadFailureExplainsRecoveryAndLogsOnlySafeDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		code, recovery string
	}{
		{"missing_scope", "/slack/install"},
		{"not_in_channel", "invited to this channel"},
		{"not_allowed_token_type", "authentication must be corrected"},
		{"ratelimited", "retry shortly"},
		{"secret-token", "Do not assume permissions are missing"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			client := &threadContextClient{fetch: func(context.Context, string, string, string, string) (slack.ThreadHistory, error) {
				return slack.ThreadHistory{}, &slack.ThreadReadError{Code: tc.code, HTTPStatus: 200}
			}}
			h := &SlackEventHandler{client: client}
			event := conversationEvent("latifin beni sevdiğini ne yaparak ispat etmesini istedim")
			event.Event.ThreadTS, event.Event.TS = "1.0", "3.0"
			input := h.threadContext(context.Background(), "secret-token", event)
			if !strings.Contains(input, tc.recovery) || !strings.Contains(input, "Do not call the user's question unclear") ||
				!strings.Contains(logs.String(), "http_status=200") || strings.Contains(input+logs.String(), "secret-token") {
				t.Fatalf("incorrect recovery or unsafe diagnostics: input=%s logs=%s", input, logs.String())
			}
		})
	}
}
