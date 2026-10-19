package handler

import (
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
	mu   sync.Mutex
	refs int
}

func (l *conversationLocks) lock(key service.ConversationKey) func() {
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[service.ConversationKey]*conversationLock)
	}
	entry := l.entries[key]
	if entry == nil {
		entry = &conversationLock{}
		l.entries[key] = entry
	}
	entry.refs++
	l.mu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		l.mu.Lock()
		defer l.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.entries, key)
		}
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
