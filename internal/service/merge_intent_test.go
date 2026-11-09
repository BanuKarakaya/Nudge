package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"nudge/internal/coding"
)

type intentResolverFunc func(context.Context, MergeIntentRequest) (coding.MergeIntent, error)

func (f intentResolverFunc) ResolveMergeIntent(ctx context.Context, r MergeIntentRequest) (coding.MergeIntent, error) {
	return f(ctx, r)
}

func TestMergeIntentToolsBindSignedEventAndCacheAcrossProviders(t *testing.T) {
	s, _ := coding.New(&codingTestStore{}, changeToolProvider{})
	base := NewAgentTools(nil, nil, nil, 7, time.Now(), nil)
	i := coding.Identity{UserID: 7, Team: "T", Channel: "C", Thread: "1", Event: "E", Request: "bu PR'ı henüz merge etme"}
	key := ConversationKey{Team: "T", Channel: "C", Thread: "1", User: "U"}
	history := ConversationState{AgentThreadID: "not-for-resolver", Turns: []ConversationExchange{{Request: "PR aç", Reply: "https://github.com/owner/repo/pull/12"}}}
	inner := NewCodingTools(base, s, i, nil)
	all := GitLabTools(inner, NewCodingTools(inner, s, i, nil))
	calls := 0
	resolve := intentResolverFunc(func(_ context.Context, req MergeIntentRequest) (coding.MergeIntent, error) {
		calls++
		if req.Current != i.Request || req.Key != key || !reflect.DeepEqual(req.Turns, history.Turns) {
			t.Fatal("wrong identity/history provided to resolver", req)
		}
		return coding.MergeIntent{Action: "none"}, nil
	})
	wrapped := WithMergeIntentTools(all, resolve, key, i, history)
	// Non-merge calls do not pay for intent classification.
	_, _ = wrapped.Call(context.Background(), "repositories_list", json.RawMessage(`{}`))
	if calls != 0 {
		t.Fatal("resolved intent for read-only tool")
	}
	for _, prefix := range []string{"", "gitlab_", ""} {
		_, err := wrapped.Call(context.Background(), prefix+"repository_change_request_merge", json.RawMessage(`{"repository":"owner/repo","number":12}`))
		if !errors.Is(err, coding.ErrMergeRequest) {
			t.Fatal("negative decision bypassed", err)
		}
	}
	if calls != 1 {
		t.Fatal("classification was not shared", calls)
	}
	wrong := i
	wrong.Event = "other-event"
	wrapped = WithMergeIntentTools(inner, resolve, key, wrong, history)
	_, err := wrapped.Call(context.Background(), "repository_change_request_merge", json.RawMessage(`{"repository":"owner/repo","number":12}`))
	if !errors.Is(err, coding.ErrMergeIntentUnavailable) || calls != 1 {
		t.Fatal("authorization escaped signed event", err)
	}
}
