package handler

import (
	"context"
	"time"
)

type loginStateStore interface {
	PutLoginState(context.Context, string, string, int64, time.Time) error
	ConsumeLoginState(context.Context, string, string) (int64, error)
}

func (h *SlackHandler) saveState(ctx context.Context, state string) error {
	if store, ok := h.installRepo.(loginStateStore); ok {
		return store.PutLoginState(ctx, workspaceHash(state), "slack", 0, time.Now().Add(10*time.Minute))
	}
	h.states.add(state, 10*time.Minute)
	return nil
}
func (h *SlackHandler) consumeState(ctx context.Context, state string) bool {
	if store, ok := h.installRepo.(loginStateStore); ok {
		_, err := store.ConsumeLoginState(ctx, workspaceHash(state), "slack")
		return err == nil
	}
	return h.states.consume(state)
}
func (h *RaindropHandler) WithStateStore(store loginStateStore) { h.stateStore = store }
func (h *RaindropHandler) saveState(ctx context.Context, state string, user int64) error {
	if h.stateStore != nil {
		return h.stateStore.PutLoginState(ctx, workspaceHash(state), "raindrop", user, time.Now().Add(10*time.Minute))
	}
	h.states.add(state, user, 10*time.Minute)
	return nil
}
func (h *RaindropHandler) consumeState(ctx context.Context, state string) (int64, bool) {
	if h.stateStore != nil {
		user, err := h.stateStore.ConsumeLoginState(ctx, workspaceHash(state), "raindrop")
		return user, err == nil
	}
	return h.states.consume(state)
}
