package ai

import (
	"context"
	"fmt"
	"log"

	"nudge/internal/service"
)

// OpenRouterModelRouter uses a free model for ordinary requests and routes
// model-declared high-complexity work to a specifically configured model.
// Complexity is emitted as part of the free model's existing structured
// conversation response, so basic requests do not require a second AI call.
type OpenRouterModelRouter struct {
	Free     OpenAICompatibleClient
	DeepSeek OpenAICompatibleClient
}

func (r OpenRouterModelRouter) Converse(ctx context.Context, state service.ConversationState, request string) (service.ConversationDecision, error) {
	return routeConversation(ctx, state, request, r.Free, r.DeepSeek, r.DeepSeek.APIKey != "" && r.DeepSeek.Model != "")
}

func routeConversation(ctx context.Context, state service.ConversationState, request string, free, deep service.ConversationAssistant, complexModelConfigured bool) (service.ConversationDecision, error) {
	decision, err := free.Converse(ctx, state, request)
	if err != nil || !decision.NeedsDeepSeek {
		return decision, err
	}
	if !complexModelConfigured {
		return service.ConversationDecision{}, fmt.Errorf("complex-task model is not configured")
	}
	log.Printf("AI complexity route=deepseek action=%s", decision.Action)
	deepDecision, err := deep.Converse(ctx, state, request)
	if err != nil {
		return service.ConversationDecision{}, err
	}
	// Keep the selected tier on the decision so data-backed RSS work triggered
	// by the same request is analyzed by the same model tier too.
	deepDecision.NeedsDeepSeek = true
	return deepDecision, nil
}

func (r OpenRouterModelRouter) Summarize(ctx context.Context, input string) (string, error) {
	return r.Free.Summarize(ctx, input)
}

func (r OpenRouterModelRouter) Recommend(ctx context.Context, input string) (string, error) {
	return r.Free.Recommend(ctx, input)
}

func (r OpenRouterModelRouter) SummarizerFor(complex bool) service.TextSummarizer {
	if complex {
		return r.DeepSeek
	}
	return r.Free
}

func (r OpenRouterModelRouter) RecommenderFor(complex bool) service.RSSRecommender {
	if complex {
		return r.DeepSeek
	}
	return r.Free
}
