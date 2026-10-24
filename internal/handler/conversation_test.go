package handler

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/service"
)

type conversationTestStore map[service.ConversationKey][]byte

func (s conversationTestStore) Load(_ context.Context, k service.ConversationKey) (service.ConversationState, error) {
	var state service.ConversationState
	if raw, ok := s[k]; ok {
		err := json.Unmarshal(raw, &state)
		return state, err
	}
	return state, nil
}
func (s conversationTestStore) Save(ctx context.Context, k service.ConversationKey, state service.ConversationState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	s[k] = raw
	return err
}

type agentFunc func(context.Context, service.AgentRequest) (service.AgentResult, error)

func (f agentFunc) Run(ctx context.Context, request service.AgentRequest) (service.AgentResult, error) {
	return f(ctx, request)
}

// Shared with auth_test.go.
type conversationInstallations struct {
	repository.InstallationRepository
}

func (conversationInstallations) GetUserIDBySlackUserID(context.Context, string, string) (int64, error) {
	return 1, nil
}
func (conversationInstallations) GetByUserID(context.Context, int64) (domain.SlackInstallation, error) {
	return domain.SlackInstallation{BotToken: "test"}, nil
}

type conversationSender struct {
	messages   []string
	fail       bool
	beforeSend func(context.Context, string)
}

func (s *conversationSender) PostMessageInThread(ctx context.Context, _, _, _, text string) error {
	if s.beforeSend != nil {
		s.beforeSend(ctx, text)
	}
	if s.fail {
		return errors.New("send failed")
	}
	s.messages = append(s.messages, text)
	return nil
}
func conversationEvent(text string) slackEventEnvelope {
	event := slackEventEnvelope{TeamID: "T", Type: "event_callback"}
	event.Event.Type = "app_mention"
	event.Event.Channel, event.Event.User, event.Event.TS, event.Event.Text = "C", "U", "1.0", text
	return event
}
func deliveredState() service.ConversationState {
	return service.ConversationState{
		Turns:    []service.ConversationExchange{{Request: "özet", Reply: "<https://example.com/one|One>"}},
		Articles: []service.ConversationArticle{{ID: "PREVIOUS-1", Title: "One", URL: "https://example.com/one"}},
		RSS:      &service.RSSSummaryProgress{Scope: "today", ShownKeys: []string{"url:https://example.com/one"}},
	}
}
func assertAgentInput(t *testing.T, input, message, timezone string) {
	t.Helper()
	prefix, ok := strings.CutSuffix(input, "\n\nSlack message:\n"+message)
	if !ok || !strings.HasSuffix(prefix, "\nTimezone: "+timezone) {
		t.Fatalf("missing message/timezone: %q", input)
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(prefix, "Current time: "), "\nTimezone: "+timezone)
	now, err := time.Parse(time.RFC3339, stamp)
	if err != nil || time.Since(now) < -time.Second || time.Since(now) > time.Minute {
		t.Fatalf("invalid current time: %q %v", stamp, err)
	}
}
func TestAgentBootstrapsJSONHistoryAndResumesAfterHandlerRestart(t *testing.T) {
	store := make(conversationTestStore)
	key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1.0", User: "U"}
	state := deliveredState()
	if err := store.Save(context.Background(), key, state); err != nil {
		t.Fatal(err)
	}
	sender := &conversationSender{}
	for i, input := range []string{"<@B123> teşekkürler", "bunlardan hangisini okuyayım?"} {
		want := state
		runtime := agentFunc(func(_ context.Context, request service.AgentRequest) (service.AgentResult, error) {
			if request.Key != key || request.ThreadID != want.AgentThreadID || !reflect.DeepEqual(request.History, want) {
				t.Fatalf("missing delivered context: %+v", request)
			}
			assertAgentInput(t, request.Input, strings.TrimSpace(slackUserMention.ReplaceAllString(input, "")), "UTC")
			return service.AgentResult{ThreadID: "codex-thread", Text: "Agent reply"}, nil
		})
		sender.beforeSend = func(context.Context, string) {
			before, err := store.Load(context.Background(), key)
			if err != nil || !reflect.DeepEqual(before, want) {
				t.Fatalf("history advanced before delivery: %+v %v", before, err)
			}
		}
		h := NewSlackEventHandler("signing", conversationInstallations{}, nil, nil, runtime, nil, store, sender, time.UTC)
		event := conversationEvent(input)
		if i > 0 {
			event.Event.TS, event.Event.ThreadTS = "2.0", "1.0"
		}
		h.replyWithSummary(event)
		state.AgentThreadID = "codex-thread"
		state.Remember(input, "Agent reply", "AGENT")
		got, err := store.Load(context.Background(), key)
		if err != nil || !reflect.DeepEqual(got, state) {
			t.Fatalf("history not saved after delivery: %+v %v", got, err)
		}
	}
	if len(sender.messages) != 2 {
		t.Fatalf("messages=%v", sender.messages)
	}
}
func TestAgentHistoryAndThreadIDAreIsolatedByFullSlackScope(t *testing.T) {
	base := service.ConversationKey{Team: "T", Channel: "C", Thread: "1.0", User: "U"}
	for _, field := range []string{"team", "channel", "thread", "user"} {
		t.Run(field, func(t *testing.T) {
			store := make(conversationTestStore)
			state := deliveredState()
			state.AgentThreadID = "private-thread"
			if err := store.Save(context.Background(), base, state); err != nil {
				t.Fatal(err)
			}
			event := conversationEvent("merhaba")
			switch field {
			case "team":
				event.TeamID = "T2"
			case "channel":
				event.Event.Channel = "C2"
			case "thread":
				event.Event.ThreadTS = "2.0"
			case "user":
				event.Event.User = "U2"
			}
			calls := 0
			h := &SlackEventHandler{installations: conversationInstallations{}, history: store, client: &conversationSender{},
				runtime: agentFunc(func(_ context.Context, request service.AgentRequest) (service.AgentResult, error) {
					calls++
					if request.Key == base || request.ThreadID != "" || !reflect.DeepEqual(request.History, service.ConversationState{}) {
						t.Fatalf("context leaked across %s: %+v", field, request)
					}
					return service.AgentResult{ThreadID: "separate-thread", Text: "Merhaba"}, nil
				}),
			}
			h.replyWithSummary(event)
			got, err := store.Load(context.Background(), base)
			if calls != 1 || err != nil || !reflect.DeepEqual(got, state) {
				t.Fatalf("original scope changed: calls=%d state=%+v err=%v", calls, got, err)
			}
		})
	}
}
func TestAgentFailureResetsThreadAndBootstrapsOnlyDeliveredHistory(t *testing.T) {
	for _, tc := range []struct {
		name, text            string
		err                   error
		sendFails, nilRuntime bool
	}{
		{name: "generation", text: "unshown output", err: errors.New("private provider error")},
		{name: "generation timeout", err: context.DeadlineExceeded},
		{name: "empty"},
		{name: "whitespace", text: " \n\t"},
		{name: "too long", text: strings.Repeat("ş", 35001)},
		{name: "delivery", text: "unshown output", sendFails: true},
		{name: "missing runtime", nilRuntime: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := make(conversationTestStore)
			key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1.0", User: "U"}
			old := deliveredState()
			old.AgentThreadID = "old-thread"
			old.AgentTokenUsage = &service.AgentTokenUsage{Input: 100, Output: 20, Total: 120}
			if err := store.Save(context.Background(), key, old); err != nil {
				t.Fatal(err)
			}
			sender := &conversationSender{fail: tc.sendFails, beforeSend: func(ctx context.Context, _ string) {
				if ctx.Err() != nil {
					t.Fatal("failure delivery inherited canceled run context")
				}
			}}
			calls := 0
			h := &SlackEventHandler{installations: conversationInstallations{}, history: store, client: sender}
			if !tc.nilRuntime {
				h.runtime = agentFunc(func(_ context.Context, request service.AgentRequest) (service.AgentResult, error) {
					calls++
					if request.ThreadID != "old-thread" {
						t.Fatalf("wrong resume ID: %s", request.ThreadID)
					}
					request.History.Turns[0].Reply = "unshown"
					request.History.Articles[0].Title = "unshown"
					request.History.RSS.ShownKeys[0] = "unshown"
					request.History.AgentTokenUsage.Total = 999
					return service.AgentResult{ThreadID: "unsafe-thread", Text: tc.text}, tc.err
				})
			}
			h.replyWithSummary(conversationEvent("<@B123> merhaba"))
			old.AgentThreadID = ""
			old.AgentTokenUsage = nil
			got, err := store.Load(context.Background(), key)
			if err != nil || !reflect.DeepEqual(got, old) {
				t.Fatalf("failed turn polluted delivered history: %+v %v", got, err)
			}
			if !tc.nilRuntime && calls != 1 {
				t.Fatalf("runtime failure triggered fallback/retry: calls=%d", calls)
			}
			if tc.sendFails {
				if len(sender.messages) != 0 {
					t.Fatalf("unexpected delivery: %v", sender.messages)
				}
			} else if len(sender.messages) != 1 || sender.messages[0] != agentFailureMessage {
				t.Fatalf("unsafe failure response: %v", sender.messages)
			}
			sender.fail = false
			bootstrapped := false
			runtime := agentFunc(func(_ context.Context, request service.AgentRequest) (service.AgentResult, error) {
				bootstrapped = true
				if request.ThreadID != "" || !reflect.DeepEqual(request.History, old) {
					t.Fatalf("new thread did not bootstrap delivered history: %+v", request)
				}
				return service.AgentResult{ThreadID: "fresh-thread", Text: "Delivered"}, nil
			})
			// Reconstruct the handler to prove reset is persistent, not process-local.
			h = NewSlackEventHandler("", conversationInstallations{}, nil, nil, runtime, nil, store, sender, nil)
			event := conversationEvent("yeniden dene")
			event.Event.TS, event.Event.ThreadTS = "2.0", "1.0"
			h.replyWithSummary(event)
			got, err = store.Load(context.Background(), key)
			if !bootstrapped || err != nil || got.AgentThreadID != "fresh-thread" || len(got.Turns) != 2 || got.Turns[1].Reply != "Delivered" {
				t.Fatalf("fresh thread not persisted: %+v %v", got, err)
			}
		})
	}
}
func TestAgentAcceptsSlackCharacterLimitBoundary(t *testing.T) {
	sender := &conversationSender{}
	text := strings.Repeat("ş", 35000)
	h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: sender,
		runtime: agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
			return service.AgentResult{ThreadID: "thread", Text: text}, nil
		}),
	}
	h.replyWithSummary(conversationEvent("uzun yanıt"))
	if len(sender.messages) != 1 || sender.messages[0] != text {
		t.Fatal("valid Unicode character limit rejected")
	}
}
