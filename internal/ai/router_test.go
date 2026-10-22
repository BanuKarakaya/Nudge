package ai

import (
	"context"
	"errors"
	"testing"

	"nudge/internal/service"
)

type conversationAssistantFunc func(context.Context, service.ConversationState, string) (service.ConversationDecision, error)

func (f conversationAssistantFunc) Converse(ctx context.Context, state service.ConversationState, request string) (service.ConversationDecision, error) {
	return f(ctx, state, request)
}

func TestOpenRouterModelRouterKeepsBasicRequestsOnFreeModel(t *testing.T) {
	freeCalls, deepCalls := 0, 0
	free := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		freeCalls++
		return service.ConversationDecision{Action: service.ConversationReply, Reply: "İyiyim!"}, nil
	})
	deep := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		deepCalls++
		return service.ConversationDecision{Action: service.ConversationReply, Reply: "DeepSeek"}, nil
	})
	decision, err := routeConversation(context.Background(), service.ConversationState{}, "nasılsın", free, deep, true)
	if err != nil || decision.Reply != "İyiyim!" || freeCalls != 1 || deepCalls != 0 {
		t.Fatalf("basic request was escalated: %+v err=%v free=%d deep=%d", decision, err, freeCalls, deepCalls)
	}
}

func TestOpenRouterModelRouterEscalatesComplexRequests(t *testing.T) {
	freeCalls, deepCalls := 0, 0
	free := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		freeCalls++
		return service.ConversationDecision{Action: service.ConversationReply, NeedsDeepSeek: true}, nil
	})
	deep := conversationAssistantFunc(func(_ context.Context, _ service.ConversationState, request string) (service.ConversationDecision, error) {
		deepCalls++
		if request != "Karşılaştır ve gerekçelendir" {
			t.Fatalf("wrong request passed to DeepSeek: %q", request)
		}
		return service.ConversationDecision{Action: service.ConversationReply, Reply: "Detaylı değerlendirme."}, nil
	})
	decision, err := routeConversation(context.Background(), service.ConversationState{}, "Karşılaştır ve gerekçelendir", free, deep, true)
	if err != nil || decision.Reply != "Detaylı değerlendirme." || !decision.NeedsDeepSeek || freeCalls != 1 || deepCalls != 1 {
		t.Fatalf("complex request was not escalated: %+v err=%v free=%d deep=%d", decision, err, freeCalls, deepCalls)
	}
}

func TestOpenRouterModelRouterDoesNotSilentlyDowngradeComplexRequest(t *testing.T) {
	free := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		return service.ConversationDecision{Action: service.ConversationReply, Reply: "Free answer", NeedsDeepSeek: true}, nil
	})
	deepErr := errors.New("DeepSeek unavailable")
	deep := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		return service.ConversationDecision{}, deepErr
	})
	_, err := routeConversation(context.Background(), service.ConversationState{}, "complex", free, deep, true)
	if !errors.Is(err, deepErr) {
		t.Fatalf("expected DeepSeek failure rather than free downgrade, got %v", err)
	}
}

func TestOpenRouterModelRouterSelectsMatchingRSSModelTier(t *testing.T) {
	router := OpenRouterModelRouter{
		Free:     OpenAICompatibleClient{Model: "openrouter/free"},
		DeepSeek: OpenAICompatibleClient{Model: "deepseek/deepseek-v4.1-flash"},
	}
	if got := router.SummarizerFor(false).(OpenAICompatibleClient).Model; got != "openrouter/free" {
		t.Fatalf("basic summary used %q", got)
	}
	if got := router.SummarizerFor(true).(OpenAICompatibleClient).Model; got != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("complex summary used %q", got)
	}
	if got := router.RecommenderFor(false).(OpenAICompatibleClient).Model; got != "openrouter/free" {
		t.Fatalf("basic recommendation used %q", got)
	}
	if got := router.RecommenderFor(true).(OpenAICompatibleClient).Model; got != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("complex recommendation used %q", got)
	}
}
