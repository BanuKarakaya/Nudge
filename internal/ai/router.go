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
	if err != nil {
		if !complexModelConfigured {
			return service.ConversationDecision{}, err
		}
		log.Printf("AI complexity route=deepseek reason=free-request-failed")
		deepDecision, deepErr := deep.Converse(ctx, state, request)
		if deepErr == nil {
			deepDecision.NeedsDeepSeek = true
			return deepDecision, nil
		}
		log.Printf("AI complexity route fallback=free reason=deepseek-recovery-failed")
		fallbackDecision, fallbackErr := free.Converse(ctx, state, request)
		if fallbackErr != nil {
			return service.ConversationDecision{}, fmt.Errorf("free model, DeepSeek recovery, and free retry failed: %w", fallbackErr)
		}
		fallbackDecision.NeedsDeepSeek = false
		return fallbackDecision, nil
	}
	if !decision.NeedsDeepSeek {
		return decision, err
	}
	if !complexModelConfigured {
		log.Printf("AI complexity route fallback=free reason=complex-model-not-configured action=%s", decision.Action)
		return free.Converse(ctx, state, request)
	}
	log.Printf("AI complexity route=deepseek action=%s", decision.Action)
	deepDecision, err := deep.Converse(ctx, state, request)
	if err != nil {
		log.Printf("AI complexity route fallback=free reason=deepseek-request-failed action=%s", decision.Action)
		fallbackDecision, fallbackErr := free.Converse(ctx, state, request)
		if fallbackErr != nil {
			return service.ConversationDecision{}, fmt.Errorf("DeepSeek and free model fallback both failed: %w", fallbackErr)
		}
		fallbackDecision.NeedsDeepSeek = false
		return fallbackDecision, nil
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
		if r.DeepSeek.APIKey == "" || r.DeepSeek.Model == "" {
			return r.Free
		}
		return fallbackSummarizer{primary: r.DeepSeek, fallback: r.Free}
	}
	return r.Free
}

func (r OpenRouterModelRouter) RecommenderFor(complex bool) service.RSSRecommender {
	if complex {
		if r.DeepSeek.APIKey == "" || r.DeepSeek.Model == "" {
			return r.Free
		}
		return fallbackRecommender{primary: r.DeepSeek, fallback: r.Free}
	}
	return r.Free
}

type fallbackSummarizer struct {
	primary  service.TextSummarizer
	fallback service.TextSummarizer
}

func (s fallbackSummarizer) Summarize(ctx context.Context, input string) (string, error) {
	result, err := s.primary.Summarize(ctx, input)
	if err == nil {
		return result, nil
	}
	log.Printf("AI complexity route fallback=free task=rss_summary")
	result, fallbackErr := s.fallback.Summarize(ctx, input)
	if fallbackErr != nil {
		return "", fmt.Errorf("DeepSeek and free RSS summary both failed: %w", fallbackErr)
	}
	return result, nil
}

type fallbackRecommender struct {
	primary  service.RSSRecommender
	fallback service.RSSRecommender
}

func (r fallbackRecommender) Recommend(ctx context.Context, input string) (string, error) {
	result, err := r.primary.Recommend(ctx, input)
	if err == nil {
		return result, nil
	}
	log.Printf("AI complexity route fallback=free task=rss_recommendation")
	result, fallbackErr := r.fallback.Recommend(ctx, input)
	if fallbackErr != nil {
		return "", fmt.Errorf("DeepSeek and free RSS recommendation both failed: %w", fallbackErr)
	}
	return result, nil
}
