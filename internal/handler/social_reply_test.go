package handler

import (
	"context"
	"errors"
	"nudge/internal/repository"
	"nudge/internal/service"
	"reflect"
	"testing"
)

type unavailableSocialHistory struct{ saved bool }

func (*unavailableSocialHistory) Load(context.Context, service.ConversationKey) (service.ConversationState, error) {
	return service.ConversationState{AgentThreadID: "must-not-use-partial-state"}, errors.New("history unavailable")
}
func (s *unavailableSocialHistory) Save(context.Context, service.ConversationKey, service.ConversationState) error {
	s.saved = true
	return nil
}

type socialAnalytics struct {
	repository.AnalyticsRepository
	record repository.InteractionRecord
}

func (a *socialAnalytics) RecordInteraction(_ context.Context, record repository.InteractionRecord) error {
	a.record = record
	return nil
}

func TestPlainGreetingsAndThanksUseNoModelTokens(t *testing.T) {
	for _, input := range []string{"merhaba", "selam", "nasılsın", "teşekkürler", "teşekkür ederim", "thanks", " NASILSIN?! ", "ÇOK TEŞEKKÜR EDERİM!", "selam, nasılsın?", "tesekkurler", "sağ ol", "hello", "how are you?"} {
		t.Run(input, func(t *testing.T) {
			sender := &conversationSender{}
			calls := 0
			store := make(conversationTestStore)
			a := &socialAnalytics{}
			h := &SlackEventHandler{installations: conversationInstallations{}, history: store, client: sender, analytics: a,
				runtime: agentFunc(func(_ context.Context, request service.AgentRequest) (service.AgentResult, error) {
					calls++
					return service.AgentResult{ThreadID: "agent-thread", Text: "Harness response"}, nil
				}),
			}
			h.replyWithSummary(conversationEvent("<@B123> " + input))
			if calls != 0 || len(sender.messages) != 1 || sender.messages[0] != localSocialReply(input) {
				t.Fatalf("expected local reply: calls=%d messages=%v", calls, sender.messages)
			}
			if a.record.Action != "SOCIAL_REPLY" || a.record.Status != "success" || len(a.record.Usage) != 0 || len(store) != 0 {
				t.Fatalf("local exchange must not incur usage or enter model history: %+v store=%v", a.record, store)
			}
		})
	}
}

func TestMixedOrQuotedSocialRequestsReachAgent(t *testing.T) {
	for _, input := range []string{"teşekkürler, RSS özetimi ver", "merhaba bugün kaç RSS var?", "nasılsın ve başlıklarımı getir", "teşekkür ederim mesajını İngilizceye çevir", "\"teşekkürler\"", "teşekkürler ama yanlış cevap verdin", "selam\nRSS getir", "<https://example.com|thanks>"} {
		t.Run(input, func(t *testing.T) {
			calls := 0
			sender := &conversationSender{}
			h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: sender,
				runtime: agentFunc(func(_ context.Context, request service.AgentRequest) (service.AgentResult, error) {
					calls++
					assertAgentInput(t, request.Input, input, "UTC")
					return service.AgentResult{Text: "Agent reply"}, nil
				}),
			}
			h.replyWithSummary(conversationEvent(input))
			if calls != 1 || len(sender.messages) != 1 || sender.messages[0] != "Agent reply" {
				t.Fatalf("task was swallowed by local matcher: calls=%d messages=%v", calls, sender.messages)
			}
		})
	}
}

func TestLocalReplyPreservesNativeContextEvenWhenDeliveryFails(t *testing.T) {
	for _, fail := range []bool{false, true} {
		store := make(conversationTestStore)
		key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1.0", User: "U"}
		state := deliveredState()
		state.AgentThreadID = "native-thread"
		state.AgentTokenUsage = &service.AgentTokenUsage{Input: 100, Output: 20, Total: 120}
		if err := store.Save(context.Background(), key, state); err != nil {
			t.Fatal(err)
		}
		// No runtime is configured: local replies must still work.
		sender := &conversationSender{fail: fail}
		h := &SlackEventHandler{installations: conversationInstallations{}, history: store, client: sender}
		h.replyWithSummary(conversationEvent("teşekkür ederim"))
		got, err := store.Load(context.Background(), key)
		if err != nil || !reflect.DeepEqual(got, state) {
			t.Fatalf("local reply changed model context (delivery fail=%t): %+v %v", fail, got, err)
		}
		if !fail && len(sender.messages) != 1 {
			t.Fatal("local reply requires an unavailable runtime")
		}
	}
}
func TestMissingHistoryFailsClosedEvenForThanks(t *testing.T) {
	unavailable := &unavailableSocialHistory{}
	for _, store := range []service.ConversationStore{nil, unavailable} {
		sender := &conversationSender{}
		h := &SlackEventHandler{installations: conversationInstallations{}, history: store, client: sender,
			runtime: agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
				t.Fatal("runtime must not run without trustworthy delivered history")
				return service.AgentResult{}, nil
			}),
		}
		h.replyWithSummary(conversationEvent("<@B123> teşekkürler"))
		if len(sender.messages) != 1 || sender.messages[0] != agentFailureMessage {
			t.Fatalf("missing safe history failure: %v", sender.messages)
		}
	}
	if unavailable.saved {
		t.Fatal("failed history load overwrote existing conversation")
	}
}

func TestLocalReplyKeepsReactionLifecycleAndDeduplication(t *testing.T) {
	sender := &reactionSender{events: make(chan string, 8)}
	h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: sender}
	event := conversationEvent("nasılsın?")
	h.replyWithSummary(event)
	h.replyWithSummary(event)
	for _, want := range []string{"add:1.0", "send:1.0", "remove:1.0"} {
		select {
		case got := <-sender.events:
			if got != want {
				t.Fatalf("got %s, want %s", got, want)
			}
		default:
			t.Fatalf("missing %s", want)
		}
	}
	if len(sender.events) != 0 {
		t.Fatal("duplicate event repeated a local reply/reaction")
	}
}
