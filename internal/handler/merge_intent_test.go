package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"nudge/internal/coding"
	"nudge/internal/service"
)

type mergeHandlerProvider struct {
	codingHandlerProvider
	coding.ChangeRequestProvider // denied intent must never reach these methods
}

type mergeHandlerRuntime struct {
	agentFunc
	resolve func(context.Context, service.MergeIntentRequest) (coding.MergeIntent, error)
}

func (r mergeHandlerRuntime) ResolveMergeIntent(ctx context.Context, req service.MergeIntentRequest) (coding.MergeIntent, error) {
	return r.resolve(ctx, req)
}

func TestSlackMergeResolverReceivesMatchingEventAndDeliveredHistory(t *testing.T) {
	for _, eventID := range []string{"", "Ev123"} {
		t.Run(eventID, func(t *testing.T) {
			ch, cs, _, _ := codingHandlerFixture(t)
			ch.service, _ = coding.New(cs, mergeHandlerProvider{})
			key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1.0", User: "U"}
			store := make(conversationTestStore)
			state := service.ConversationState{Turns: []service.ConversationExchange{{Request: "merge et", Reply: "https://github.com/developer/private/pull/12 merge başarısız"}}}
			_ = store.Save(context.Background(), key, state)
			calls := 0
			runtime := mergeHandlerRuntime{
				resolve: func(_ context.Context, req service.MergeIntentRequest) (coding.MergeIntent, error) {
					calls++
					if req.Key != key || req.Current != "mergeleme işlemini tekrar dener misin" || len(req.Turns) != 1 || req.Turns[0] != state.Turns[0] {
						t.Fatal("lost current user or delivered context", req)
					}
					return coding.MergeIntent{Action: "clarify"}, nil
				},
				agentFunc: func(ctx context.Context, req service.AgentRequest) (service.AgentResult, error) {
					_, err := req.Tools.Call(ctx, "repository_change_request_merge", json.RawMessage(`{"repository":"developer/private","number":12}`))
					if !errors.Is(err, coding.ErrMergeRequest) {
						t.Fatal("host event identity or intent not passed through", err)
					}
					return service.AgentResult{Text: "Hangi PR?"}, nil
				},
			}
			h := (&SlackEventHandler{installations: conversationInstallations{}, history: store, client: &conversationSender{}, runtime: runtime}).WithCoding(ch)
			event := conversationEvent("mergeleme işlemini tekrar dener misin")
			event.EventID = eventID
			h.replyWithSummary(event)
			if calls != 1 {
				t.Fatal("resolver not wired into Slack", calls)
			}
		})
	}
}
