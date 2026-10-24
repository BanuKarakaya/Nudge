package handler

import (
	"context"
	"sync"
	"time"

	"nudge/internal/service"
)

// Only requests for the exact same history key wait for one another. Entries
// remain until the owner AND all waiters have left, then are reclaimed.
type conversationLocks struct {
	mu      sync.Mutex
	entries map[service.ConversationKey]*conversationLock
}

type conversationLock struct {
	gate chan struct{}
	refs int
}

func (l *conversationLocks) lock(ctx context.Context, key service.ConversationKey) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[service.ConversationKey]*conversationLock)
	}
	entry := l.entries[key]
	if entry == nil {
		entry = &conversationLock{gate: make(chan struct{}, 1)}
		l.entries[key] = entry
	}
	entry.refs++
	l.mu.Unlock()
	releaseRef := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.entries, key)
		}
	}
	select {
	case entry.gate <- struct{}{}:
		// Cancellation may race with a released gate; never start canceled work.
		if err := ctx.Err(); err != nil {
			<-entry.gate
			releaseRef()
			return nil, err
		}
		return func() {
			<-entry.gate
			releaseRef()
		}, nil
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	}
}

type eventKey struct{ team, id, channel, ts string }

// Process-local replay protection: active events never expire; completed events
// are retained for 15 minutes. A new user message has a different event ID/TS.
// This is intentionally not a response cache or cross-replica delivery guarantee.
type eventDeduplicator struct {
	mu     sync.Mutex
	events map[eventKey]time.Time
}

func (d *eventDeduplicator) begin(event slackEventEnvelope, now time.Time) (func(), bool) {
	key := eventKey{team: event.TeamID, id: event.EventID}
	if key.id == "" {
		if event.Event.TS == "" {
			return func() {}, true
		}
		key.channel, key.ts = event.Event.Channel, event.Event.TS
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.events == nil {
		d.events = make(map[eventKey]time.Time)
	}
	for k, expiry := range d.events {
		if !expiry.IsZero() && !now.Before(expiry) {
			delete(d.events, k)
		}
	}
	if _, exists := d.events[key]; exists {
		return func() {}, false
	}
	d.events[key] = time.Time{}
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.events[key] = time.Now().Add(15 * time.Minute)
	}, true
}
