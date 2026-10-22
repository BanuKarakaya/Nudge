package ai

import (
	"context"
	"errors"
	"testing"

	"nudge/internal/service"
)

type conversationAssistantFunc func(context.Context, service.ConversationState, string) (service.ConversationDecision, error)
type summarizerFunc func(context.Context, string) (string, error)
type recommenderFunc func(context.Context, string) (string, error)

func (f conversationAssistantFunc) Converse(ctx context.Context, state service.ConversationState, request string) (service.ConversationDecision, error) {
	return f(ctx, state, request)
}

func (f summarizerFunc) Summarize(ctx context.Context, input string) (string, error) {
	return f(ctx, input)
}

func (f recommenderFunc) Recommend(ctx context.Context, input string) (string, error) {
	return f(ctx, input)
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

func TestOpenRouterModelRouterUsesDeepSeekWhenFreeModelFails(t *testing.T) {
	freeCalls, deepCalls := 0, 0
	freeErr := errors.New("free model returned invalid JSON")
	free := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		freeCalls++
		return service.ConversationDecision{}, freeErr
	})
	deep := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		deepCalls++
		return service.ConversationDecision{Action: service.ConversationReply, Reply: "İyiyim!"}, nil
	})
	decision, err := routeConversation(context.Background(), service.ConversationState{}, "nasılsın", free, deep, true)
	if err != nil || decision.Reply != "İyiyim!" || !decision.NeedsDeepSeek || freeCalls != 1 || deepCalls != 1 {
		t.Fatalf("DeepSeek recovery failed: %+v err=%v free=%d deep=%d", decision, err, freeCalls, deepCalls)
	}
}

func TestOpenRouterModelRouterRetriesFreeIfRecoveryModelFails(t *testing.T) {
	freeCalls, deepCalls := 0, 0
	free := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		freeCalls++
		if freeCalls == 1 {
			return service.ConversationDecision{}, errors.New("temporary free-model failure")
		}
		return service.ConversationDecision{Action: service.ConversationReply, Reply: "Tekrar denedim."}, nil
	})
	deep := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		deepCalls++
		return service.ConversationDecision{}, errors.New("DeepSeek unavailable")
	})
	decision, err := routeConversation(context.Background(), service.ConversationState{}, "nasılsın", free, deep, true)
	if err != nil || decision.Reply != "Tekrar denedim." || decision.NeedsDeepSeek || freeCalls != 2 || deepCalls != 1 {
		t.Fatalf("free retry failed: %+v err=%v free=%d deep=%d", decision, err, freeCalls, deepCalls)
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

func TestOpenRouterModelRouterFallsBackToFreeWhenDeepSeekFails(t *testing.T) {
	freeCalls := 0
	free := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		freeCalls++
		if freeCalls == 1 {
			return service.ConversationDecision{Action: service.ConversationReply, NeedsDeepSeek: true}, nil
		}
		return service.ConversationDecision{Action: service.ConversationReply, Reply: "Ücretsiz model yanıtı.", NeedsDeepSeek: true}, nil
	})
	deepErr := errors.New("DeepSeek unavailable")
	deep := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		return service.ConversationDecision{}, deepErr
	})
	decision, err := routeConversation(context.Background(), service.ConversationState{}, "complex", free, deep, true)
	if err != nil || decision.Reply != "Ücretsiz model yanıtı." || decision.NeedsDeepSeek || freeCalls != 2 {
		t.Fatalf("free fallback not returned: %+v err=%v freeCalls=%d", decision, err, freeCalls)
	}
}

func TestOpenRouterModelRouterFallsBackToFreeWhenComplexModelIsUnconfigured(t *testing.T) {
	freeCalls := 0
	free := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		freeCalls++
		if freeCalls == 1 {
			return service.ConversationDecision{Action: service.ConversationReply, NeedsDeepSeek: true}, nil
		}
		return service.ConversationDecision{Action: service.ConversationReply, Reply: "Ücretsiz yanıt."}, nil
	})
	deep := conversationAssistantFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		t.Fatal("unconfigured DeepSeek model should not be called")
		return service.ConversationDecision{}, nil
	})
	decision, err := routeConversation(context.Background(), service.ConversationState{}, "complex", free, deep, false)
	if err != nil || decision.Reply != "Ücretsiz yanıt." || freeCalls != 2 {
		t.Fatalf("free fallback not returned: %+v err=%v freeCalls=%d", decision, err, freeCalls)
	}
}

func TestOpenRouterModelRouterSelectsMatchingRSSModelTier(t *testing.T) {
	router := OpenRouterModelRouter{
		Free:     OpenAICompatibleClient{APIKey: "test", Model: "openrouter/free"},
		DeepSeek: OpenAICompatibleClient{APIKey: "test", Model: "deepseek/deepseek-v4.1-flash"},
	}
	if got := router.SummarizerFor(false).(OpenAICompatibleClient).Model; got != "openrouter/free" {
		t.Fatalf("basic summary used %q", got)
	}
	if got := router.SummarizerFor(true).(fallbackSummarizer).primary.(OpenAICompatibleClient).Model; got != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("complex summary used %q", got)
	}
	if got := router.RecommenderFor(false).(OpenAICompatibleClient).Model; got != "openrouter/free" {
		t.Fatalf("basic recommendation used %q", got)
	}
	if got := router.RecommenderFor(true).(fallbackRecommender).primary.(OpenAICompatibleClient).Model; got != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("complex recommendation used %q", got)
	}
}

func TestComplexRSSWorkFallsBackToFreeOnDeepSeekError(t *testing.T) {
	primaryErr := errors.New("DeepSeek unavailable")
	summary := fallbackSummarizer{
		primary: summarizerFunc(func(context.Context, string) (string, error) { return "", primaryErr }),
		fallback: summarizerFunc(func(_ context.Context, input string) (string, error) {
			return "free summary: " + input, nil
		}),
	}
	if got, err := summary.Summarize(context.Background(), "RSS data"); err != nil || got != "free summary: RSS data" {
		t.Fatalf("summary fallback: %q, %v", got, err)
	}
	recommender := fallbackRecommender{
		primary: recommenderFunc(func(context.Context, string) (string, error) { return "", primaryErr }),
		fallback: recommenderFunc(func(_ context.Context, input string) (string, error) {
			return "free recommendation: " + input, nil
		}),
	}
	if got, err := recommender.Recommend(context.Background(), "RSS data"); err != nil || got != "free recommendation: RSS data" {
		t.Fatalf("recommendation fallback: %q, %v", got, err)
	}
}
