package handler

import (
	"context"
	"errors"
	"nudge/internal/service"
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
func TestPlainGreetingsAndThanksAlwaysReachAgentRuntime(t *testing.T) {
	for _, input := range []string{"merhaba", "selam", "nasılsın", "teşekkürler", "teşekkür ederim", "thanks"} {
		t.Run(input, func(t *testing.T) {
			sender := &conversationSender{}
			calls := 0
			h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: sender,
				runtime: agentFunc(func(_ context.Context, request service.AgentRequest) (service.AgentResult, error) {
					calls++
					assertAgentInput(t, request.Input, input, "UTC")
					return service.AgentResult{ThreadID: "agent-thread", Text: "Harness response"}, nil
				}),
			}
			h.replyWithSummary(conversationEvent("<@B123> " + input))
			if calls != 1 || len(sender.messages) != 1 || sender.messages[0] != "Harness response" {
				t.Fatalf("greeting bypassed harness: calls=%d messages=%v", calls, sender.messages)
			}
		})
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
