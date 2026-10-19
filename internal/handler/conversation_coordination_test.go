package handler

import (
	"context"
	"sync"
	"testing"
	"time"

	"nudge/internal/service"
)

func TestConversationLocksIsolateThreadsAndSerializeSameHistory(t *testing.T) {
	var locks conversationLocks
	key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1", User: "U"}
	release := locks.lock(key)
	sameDone := make(chan struct{})
	go func() { unlock := locks.lock(key); unlock(); close(sameDone) }()
	select {
	case <-sameDone:
		t.Fatal("same history was not serialized")
	case <-time.After(20 * time.Millisecond):
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			other := key
			other.Thread = time.Unix(int64(i+2), 0).String()
			unlock := locks.lock(other)
			unlock()
		}(i)
	}
	// More than 64 independent threads must finish while key is held.
	othersDone := make(chan struct{})
	go func() { wg.Wait(); close(othersDone) }()
	select {
	case <-othersDone:
	case <-time.After(3 * time.Second):
		release()
		t.Fatal("unrelated conversations blocked each other")
	}
	release()
	select {
	case <-sameDone:
	case <-time.After(3 * time.Second):
		t.Fatal("queued conversation did not resume")
	}
	if len(locks.entries) != 0 {
		t.Fatal("idle locks leaked")
	}
}

func TestSlackReplayDoesNotRepeatAIOrSuppressNewUserMessage(t *testing.T) {
	calls := 0
	sender := &conversationSender{}
	h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: sender,
		dialogue: dialogueFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
			calls++
			return service.ConversationDecision{Action: service.ConversationReply, Reply: "Merhaba!"}, nil
		}),
	}
	event := slackEventEnvelope{TeamID: "T", EventID: "Ev1"}
	event.Event.Channel, event.Event.User, event.Event.TS, event.Event.Text = "C", "U", "1.0", "selam"
	h.replyWithSummary(event)
	h.replyWithSummary(event)
	event.EventID, event.Event.TS, event.Event.ThreadTS = "Ev2", "2.0", "1.0"
	h.replyWithSummary(event)
	if calls != 2 || len(sender.messages) != 2 {
		t.Fatalf("calls=%d messages=%v", calls, sender.messages)
	}
}

func TestEventReplayActiveExpiryAndTenantIsolation(t *testing.T) {
	var d eventDeduplicator
	event := slackEventEnvelope{TeamID: "T", EventID: "E"}
	now := time.Now()
	finish, fresh := d.begin(event, now)
	if !fresh {
		t.Fatal("first event rejected")
	}
	if _, fresh := d.begin(event, now.Add(time.Hour)); fresh {
		t.Fatal("active event expired")
	}
	other := event
	other.TeamID = "T2"
	finishOther, fresh := d.begin(other, now)
	if !fresh {
		t.Fatal("cross-team event suppressed")
	}
	finishOther()
	finish()
	if _, fresh := d.begin(event, now); fresh {
		t.Fatal("completed replay accepted")
	}
	finishAgain, fresh := d.begin(event, time.Now().Add(16*time.Minute))
	if !fresh {
		t.Fatal("expired event leaked")
	}
	finishAgain()
}
