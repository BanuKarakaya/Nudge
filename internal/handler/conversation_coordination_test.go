package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"nudge/internal/repository"
	"nudge/internal/service"
)

func TestConversationLocksIsolateThreadsAndSerializeSameHistory(t *testing.T) {
	var locks conversationLocks
	key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1", User: "U"}
	release, err := locks.lock(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	sameDone := make(chan struct{})
	go func() {
		unlock, err := locks.lock(context.Background(), key)
		if err != nil {
			t.Error(err)
			return
		}
		unlock()
		close(sameDone)
	}()
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
			unlock, err := locks.lock(context.Background(), other)
			if err != nil {
				t.Error(err)
				return
			}
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

func TestSlackReplayDoesNotRepeatAgentOrSuppressNewUserMessage(t *testing.T) {
	calls := 0
	sender := &conversationSender{}
	h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: sender,
		runtime: agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
			calls++
			return service.AgentResult{ThreadID: "native-thread", Text: "Merhaba!"}, nil
		}),
	}
	event := slackEventEnvelope{TeamID: "T", EventID: "Ev1"}
	event.Event.Channel, event.Event.User, event.Event.TS, event.Event.Text = "C", "U", "1.0", "bana bir fikir ver"
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

func TestConversationLockCancellationReclaimsWaitersWithoutUnlockingOwner(t *testing.T) {
	var locks conversationLocks
	key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1", User: "U"}
	release, err := locks.lock(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		if i == 0 {
			cancel()
		}
		unlock, err := locks.lock(ctx, key)
		cancel()
		if err == nil || unlock != nil {
			t.Fatal("canceled waiter acquired owned lock")
		}
		locks.mu.Lock()
		refs := locks.entries[key].refs
		locks.mu.Unlock()
		if refs != 1 {
			t.Fatalf("canceled waiter leaked refs: %d", refs)
		}
	}
	release()
	if len(locks.entries) != 0 {
		t.Fatal("released lock leaked")
	}
	unlock, err := locks.lock(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

type reactionSender struct {
	events            chan string
	failSend, failAdd bool
}

func (s *reactionSender) AddReaction(ctx context.Context, token, channel, ts, name string) error {
	if ctx.Err() != nil || token != "test" || channel != "C" || name != processingReaction {
		return errors.New("invalid reaction context")
	}
	s.events <- "add:" + ts
	if s.failAdd {
		return errors.New("reaction unavailable")
	}
	return nil
}
func (s *reactionSender) RemoveReaction(ctx context.Context, token, channel, ts, name string) error {
	if ctx.Err() != nil || token != "test" || channel != "C" || name != processingReaction {
		return errors.New("invalid cleanup context")
	}
	s.events <- "remove:" + ts
	return nil
}
func (s *reactionSender) PostMessageInThread(ctx context.Context, token, channel, thread, text string) error {
	if ctx.Err() != nil || token != "test" || channel != "C" {
		return errors.New("invalid send context")
	}
	s.events <- "send:" + thread
	if s.failSend {
		return errors.New("send failed")
	}
	return nil
}

func TestProcessingReactionSurroundsAgentAndDelivery(t *testing.T) {
	for _, outcome := range []string{"success", "generation failure", "delivery failure", "reaction failure", "history failure"} {
		t.Run(outcome, func(t *testing.T) {
			sender := &reactionSender{events: make(chan string, 8), failSend: outcome == "delivery failure", failAdd: outcome == "reaction failure"}
			h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: sender,
				runtime: agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
					sender.events <- "run"
					if outcome == "generation failure" {
						return service.AgentResult{}, context.DeadlineExceeded
					}
					return service.AgentResult{ThreadID: "thread", Text: "reply"}, nil
				}),
			}
			if outcome == "history failure" {
				h.history = nil
			}
			event := conversationEvent("RSS özetimi ver")
			event.Event.ThreadTS = "root"
			h.replyWithSummary(event)
			close(sender.events)
			var got []string
			for stage := range sender.events {
				got = append(got, stage)
			}
			want := []string{"add:1.0", "run", "send:root", "remove:1.0"}
			if outcome == "reaction failure" {
				want = want[:3]
			}
			if outcome == "history failure" {
				want = []string{"add:1.0", "send:root", "remove:1.0"}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("reaction/work order=%v want=%v", got, want)
			}
		})
	}
}

func TestProcessingReactionIsAddedBeforeWaitingForConversationLock(t *testing.T) {
	sender := &reactionSender{events: make(chan string, 8)}
	h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: sender,
		runtime: agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
			sender.events <- "run"
			return service.AgentResult{ThreadID: "thread", Text: "reply"}, nil
		}),
	}
	key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1.0", User: "U"}
	release, err := h.conversationLocks.lock(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { h.replyWithSummary(conversationEvent("RSS özetimi ver")); close(done) }()
	select {
	case stage := <-sender.events:
		if stage != "add:1.0" {
			release()
			t.Fatalf("first stage=%s", stage)
		}
	case <-time.After(time.Second):
		release()
		t.Fatal("reaction blocked behind conversation lock")
	}
	select {
	case stage := <-sender.events:
		release()
		t.Fatalf("work bypassed lock: %s", stage)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("queued request did not resume")
	}
	for _, want := range []string{"run", "send:1.0", "remove:1.0"} {
		select {
		case got := <-sender.events:
			if got != want {
				t.Fatalf("got %s want %s", got, want)
			}
		default:
			t.Fatalf("missing %s", want)
		}
	}
}

func TestQueuedSlackRequestTimesOutWithoutChangingActiveConversation(t *testing.T) {
	store := make(conversationTestStore)
	key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1.0", User: "U"}
	state := deliveredState()
	state.AgentThreadID = "active-thread"
	state.AgentTokenUsage = &service.AgentTokenUsage{Input: 100, Output: 20, Total: 120}
	if err := store.Save(context.Background(), key, state); err != nil {
		t.Fatal(err)
	}
	sender := &reactionSender{events: make(chan string, 8)}
	h := &SlackEventHandler{installations: conversationInstallations{}, history: store, client: sender,
		runtime: agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
			t.Error("queued request bypassed owned conversation lock")
			return service.AgentResult{}, errors.New("unexpected run")
		}),
	}
	release, err := h.conversationLocks.lock(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	done := make(chan struct{})
	go func() { h.replyWithSummary(conversationEvent("queued message")); close(done) }()
	select {
	case <-done:
	case <-time.After(18 * time.Second):
		t.Fatal("conversation lock wait was not bounded")
	}
	got, err := store.Load(context.Background(), key)
	if err != nil || !reflect.DeepEqual(got, state) {
		t.Fatalf("queued timeout changed active native thread/history: %+v %v", got, err)
	}
	for _, want := range []string{"add:1.0", "send:1.0", "remove:1.0"} {
		select {
		case got := <-sender.events:
			if got != want {
				t.Fatalf("got %s want %s", got, want)
			}
		default:
			t.Fatalf("missing %s", want)
		}
	}
	if len(sender.events) != 0 {
		t.Fatal("unexpected queued request work")
	}
}

type completionAnalytics struct {
	repository.AnalyticsRepository
	done chan struct{}
}

func (a completionAnalytics) RecordInteraction(context.Context, repository.InteractionRecord) error {
	close(a.done)
	return nil
}

func TestSignedSlackEventAcknowledgesBeforeAgentCompletes(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	h := NewSlackEventHandler("signing", conversationInstallations{}, nil, nil,
		agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
			close(entered)
			<-release
			return service.AgentResult{ThreadID: "thread", Text: "reply"}, nil
		}), nil, make(conversationTestStore), &conversationSender{}, nil, completionAnalytics{done: done})
	body := `{"type":"event_callback","team_id":"T","event_id":"E","event":{"type":"app_mention","user":"U","channel":"C","ts":"1.0","text":"RSS özetimi ver"}}`
	request := httptest.NewRequest("POST", "/slack/events", strings.NewReader(body))
	signSlackRequest(request, body)
	response := httptest.NewRecorder()
	returned := make(chan struct{})
	go func() { h.handle(response, request); close(returned) }()
	select {
	case <-returned:
		if response.Code != http.StatusOK {
			t.Fatalf("ack status=%d", response.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("Slack acknowledgment blocked on generation")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("signed event did not reach runtime")
	}
	// Closing through the deferred cleanup also releases generation on failures.
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("background request did not finish")
		}
	})
}
