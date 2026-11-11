package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nudge/internal/repository"
	"nudge/internal/service"
)

type testJobQueue struct {
	repository.SlackJobRepository
	job                                        repository.SlackJob
	enqueueErr, saveErr, completeErr, leaseErr error
	enqueued                                   int
	completed                                  bool
	success                                    bool
	state                                      *service.ConversationState
}

func (q *testJobQueue) Enqueue(ctx context.Context, j repository.SlackJob) error {
	if q.enqueueErr != nil {
		return q.enqueueErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if q.enqueued == 0 {
		q.job = j
		q.job.ID = 1
		q.job.Attempts = 1
		q.job.ClaimToken = "first"
	}
	q.enqueued++
	return nil
}
func (q *testJobQueue) CheckLease(ctx context.Context, _ repository.SlackJob) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return q.leaseErr
}
func (q *testJobQueue) SaveResult(ctx context.Context, _ repository.SlackJob, result json.RawMessage) error {
	if q.saveErr != nil {
		return q.saveErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if q.leaseErr != nil {
		return q.leaseErr
	}
	q.job.Result = append(json.RawMessage(nil), result...)
	return nil
}

func TestQueuedRuntimeToolsCheckOwnershipBeforeExecution(t *testing.T) {
	q := &testJobQueue{}
	queuedEvent(t, q, conversationEvent("RSS listesini getir"))
	sender := &conversationSender{}
	h := NewSlackEventHandler("", conversationInstallations{}, nil, nil,
		agentFunc(func(ctx context.Context, r service.AgentRequest) (service.AgentResult, error) {
			q.leaseErr = repository.ErrSlackJobLeaseLost
			_, err := r.Tools.Call(ctx, "rss_inventory", json.RawMessage(`{}`))
			if !errors.Is(err, repository.ErrSlackJobLeaseLost) {
				t.Fatalf("runtime tool bypassed ownership check: %v", err)
			}
			return service.AgentResult{Text: "must not send"}, nil
		}), nil, make(conversationTestStore), sender, nil).WithJobQueue(q)
	if err := h.ProcessJob(context.Background(), q.job); !errors.Is(err, repository.ErrSlackJobLeaseLost) {
		t.Fatalf("lost ownership: %v", err)
	}
	if len(sender.messages) != 0 || q.completed {
		t.Fatal("stale worker sent or completed")
	}
}
func (q *testJobQueue) MarkDelivered(_ context.Context, _ repository.SlackJob) error {
	q.job.Delivered = true
	return nil
}
func (q *testJobQueue) Complete(_ context.Context, _ repository.SlackJob, state json.RawMessage, _ repository.InteractionRecord, success bool) error {
	if q.completeErr != nil {
		return q.completeErr
	}
	q.completed = true
	q.success = success
	if len(state) > 0 {
		q.state = &service.ConversationState{}
		return json.Unmarshal(state, q.state)
	}
	return nil
}

func queuedEvent(t *testing.T, q *testJobQueue, event slackEventEnvelope) {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(context.Background(), repository.SlackJob{Key: eventConversationKey(event), EventKey: slackEventKey(event), Payload: raw}); err != nil {
		t.Fatal(err)
	}
}

func TestSlackJobIngressPersistsBeforeAcknowledgment(t *testing.T) {
	body := `{"type":"event_callback","team_id":"T","event_id":"E","event":{"type":"app_mention","user":"U","channel":"C","ts":"1.0","text":"do work"}}`
	for _, mode := range []string{"ok", "db failure", "missing queue", "unsigned", "missing identity"} {
		t.Run(mode, func(t *testing.T) {
			q := &testJobQueue{}
			h := (&SlackEventHandler{signingSecret: "signing"}).WithJobQueue(q)
			want := 200
			input := body
			switch mode {
			case "db failure":
				q.enqueueErr = errors.New("offline")
				want = 503
			case "missing queue":
				h.jobs = nil
				want = 503
			case "unsigned":
				want = 401
			case "missing identity":
				input = strings.Replace(body, `"team_id":"T"`, `"team_id":""`, 1)
				want = 400
			}
			r := httptest.NewRequest("POST", "/slack/events", strings.NewReader(input))
			if mode != "unsigned" {
				signSlackRequest(r, input)
			}
			w := httptest.NewRecorder()
			h.handle(w, r)
			if w.Code != want {
				t.Fatalf("status=%d want=%d", w.Code, want)
			}
			if want == http.StatusOK && (q.enqueued != 1 || len(q.job.Payload) == 0) {
				t.Fatal("ack without persisted event")
			}
			if want != http.StatusOK && q.enqueued != 0 {
				t.Fatal("invalid event enqueued")
			}
		})
	}
}

func TestSlackJobRestartRetriesDeliveryWithoutRerunningAgent(t *testing.T) {
	for _, failure := range []string{"send", "completion"} {
		t.Run(failure, func(t *testing.T) {
			q := &testJobQueue{}
			queuedEvent(t, q, conversationEvent("bu kodu incele"))
			sender := &conversationSender{fail: failure == "send"}
			if failure == "completion" {
				q.completeErr = errors.New("database interrupted")
			}
			calls := 0
			runtime := agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
				calls++
				return service.AgentResult{Text: "saved answer", ThreadID: "native"}, nil
			})
			h := NewSlackEventHandler("", conversationInstallations{}, nil, nil, runtime, nil, make(conversationTestStore), sender, nil).WithJobQueue(q)
			if err := h.ProcessJob(context.Background(), q.job); err == nil {
				t.Fatal("expected interruption")
			}
			if len(q.job.Result) == 0 || q.completed {
				t.Fatal("result checkpoint missing")
			}
			sender.fail = false
			q.completeErr = nil
			q.job.Attempts++
			// A new handler simulates restart; replay must never need a runtime.
			restarted := NewSlackEventHandler("", conversationInstallations{}, nil, nil, nil, nil, nil, sender, nil).WithJobQueue(q)
			if err := restarted.ProcessJob(context.Background(), q.job); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || len(sender.messages) != 1 || sender.messages[0] != "saved answer" || !q.completed || !q.success {
				t.Fatalf("calls=%d messages=%v completed=%v", calls, sender.messages, q.completed)
			}
			if q.state == nil || len(q.state.Turns) != 1 || q.state.AgentThreadID != "native" {
				t.Fatal("delivered history not restored")
			}
		})
	}
}

func TestSlackJobDoesNotSendUnpersistedResultOrRunWithLostLease(t *testing.T) {
	for _, lostLease := range []bool{false, true} {
		q := &testJobQueue{saveErr: errors.New("database offline")}
		queuedEvent(t, q, conversationEvent("incele"))
		if lostLease {
			q.leaseErr = repository.ErrSlackJobLeaseLost
		}
		calls := 0
		sender := &conversationSender{}
		h := NewSlackEventHandler("", conversationInstallations{}, nil, nil, agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
			calls++
			return service.AgentResult{Text: "reply"}, nil
		}), nil, make(conversationTestStore), sender, nil).WithJobQueue(q)
		if err := h.ProcessJob(context.Background(), q.job); err == nil {
			t.Fatal("failure swallowed")
		}
		if len(sender.messages) != 0 || q.completed || (lostLease && calls != 0) {
			t.Fatal("unowned or unpersisted work delivered")
		}
	}
}

func TestInterruptedSlackJobRestartsFromDeliveredHistory(t *testing.T) {
	q := &testJobQueue{}
	event := conversationEvent("kodu düzelt")
	queuedEvent(t, q, event)
	store := make(conversationTestStore)
	key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1.0", User: "U"}
	original := deliveredState()
	original.AgentThreadID = "old-native"
	if err := store.Save(context.Background(), key, original); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sender := &conversationSender{}
	h := NewSlackEventHandler("", conversationInstallations{}, nil, nil, agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
		cancel()
		return service.AgentResult{}, context.Canceled
	}), nil, store, sender, nil).WithJobQueue(q)
	if err := h.ProcessJob(ctx, q.job); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown=%v", err)
	}
	if len(q.job.Result) != 0 || len(sender.messages) != 0 {
		t.Fatal("shutdown delivered terminal failure")
	}
	q.job.Attempts++
	h.runtime = agentFunc(func(_ context.Context, r service.AgentRequest) (service.AgentResult, error) {
		if r.ThreadID != "" || r.History.AgentThreadID != "" || len(r.History.Turns) != 1 || !strings.Contains(r.Input, "Host recovery") {
			t.Fatalf("unsafe recovery context: %+v", r)
		}
		return service.AgentResult{Text: "recovered", ThreadID: "new-native"}, nil
	})
	if err := h.ProcessJob(context.Background(), q.job); err != nil {
		t.Fatal(err)
	}
	if !q.completed || len(sender.messages) != 1 || len(q.state.Turns) != 2 {
		t.Fatal("interrupted work not recovered")
	}
}

func TestQueuedAgentFailureIsDeliveredAndTerminal(t *testing.T) {
	q := &testJobQueue{}
	queuedEvent(t, q, conversationEvent("bir iş yap"))
	sender := &conversationSender{}
	h := NewSlackEventHandler("", conversationInstallations{}, nil, nil, agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
		return service.AgentResult{}, context.DeadlineExceeded
	}), nil, make(conversationTestStore), sender, time.UTC).WithJobQueue(q)
	if err := h.ProcessJob(context.Background(), q.job); err != nil {
		t.Fatal(err)
	}
	if !q.completed || q.success || len(sender.messages) != 1 || sender.messages[0] != agentTimeoutMessage {
		t.Fatal("failure lost or retried forever")
	}
}
