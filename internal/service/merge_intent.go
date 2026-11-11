package service

import (
	"context"
	"encoding/json"
	"sync"

	"nudge/internal/coding"
)

type MergeIntentRequest struct {
	UserID  int64
	Key     ConversationKey
	Current string
	Turns   []ConversationExchange
}

type MergeIntentResolver interface {
	ResolveMergeIntent(context.Context, MergeIntentRequest) (coding.MergeIntent, error)
}

type mergeIntentTools struct {
	AgentTools
	resolver MergeIntentResolver
	request  MergeIntentRequest
	identity coding.Identity
	once     sync.Once
	intent   coding.MergeIntent
	err      error
}

// Keep the resolver outside both providers' tools, so one decision is shared by
// all merge attempts in this event. Only delivered owner-bound history is used;
// fetched Slack messages, repository text and executing-agent arguments are not.
func WithMergeIntentTools(base AgentTools, resolver MergeIntentResolver, key ConversationKey, identity coding.Identity, history ConversationState) AgentTools {
	return &mergeIntentTools{AgentTools: base, resolver: resolver, identity: identity,
		request: MergeIntentRequest{UserID: identity.UserID, Key: key, Current: identity.Request, Turns: append([]ConversationExchange(nil), history.Turns...)}}
}

func (t *mergeIntentTools) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	return t.AgentTools.Call(coding.WithMergeIntentResolver(ctx, t.resolve), name, args)
}

func (t *mergeIntentTools) resolve(ctx context.Context, identity coding.Identity) (coding.MergeIntent, error) {
	if identity != t.identity || t.resolver == nil {
		return coding.MergeIntent{}, coding.ErrMergeIntentUnavailable
	}
	t.once.Do(func() {
		t.intent, t.err = t.resolver.ResolveMergeIntent(ctx, t.request)
	})
	return t.intent, t.err
}
