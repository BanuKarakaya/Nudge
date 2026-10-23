package analytics

import (
	"context"
	"sync"
)

// AIUsage is emitted by each provider call that contributes to a user turn.
type AIUsage struct {
	Provider         string
	Model            string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	LatencyMS        int64
}

type UsageTracker struct {
	mu    sync.Mutex
	calls []AIUsage
}

func NewUsageTracker() *UsageTracker {
	return &UsageTracker{}
}

func (t *UsageTracker) Add(call AIUsage) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.calls = append(t.calls, call)
	t.mu.Unlock()
}

func (t *UsageTracker) Snapshot() []AIUsage {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]AIUsage(nil), t.calls...)
}

type usageTrackerContextKey struct{}

func WithUsageTracker(ctx context.Context, tracker *UsageTracker) context.Context {
	return context.WithValue(ctx, usageTrackerContextKey{}, tracker)
}

func UsageTrackerFromContext(ctx context.Context) *UsageTracker {
	if ctx == nil {
		return nil
	}
	tracker, _ := ctx.Value(usageTrackerContextKey{}).(*UsageTracker)
	return tracker
}
