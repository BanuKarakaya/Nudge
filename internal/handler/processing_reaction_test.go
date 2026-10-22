package handler

import (
	"context"
	"errors"
	"testing"
	"time"
)

type reactionRecorder struct {
	events chan string
}

func (r reactionRecorder) AddReaction(context.Context, string, string, string, string) error {
	r.events <- "add"
	return nil
}

func (r reactionRecorder) RemoveReaction(context.Context, string, string, string, string) error {
	r.events <- "remove"
	return nil
}

func TestProcessingReactionSkipsFastReplies(t *testing.T) {
	recorder := reactionRecorder{events: make(chan string, 2)}
	indicator := newProcessingReactionIndicator(recorder, "token", "channel", "ts", "req")
	indicator.showDelay = 40 * time.Millisecond
	go indicator.run()
	indicator.finish()
	select {
	case event := <-recorder.events:
		t.Fatalf("fast reply unexpectedly emitted reaction %q", event)
	case <-time.After(60 * time.Millisecond):
	}
}

func TestProcessingReactionStaysVisibleBrieflyAndIsRemoved(t *testing.T) {
	recorder := reactionRecorder{events: make(chan string, 2)}
	indicator := newProcessingReactionIndicator(recorder, "token", "channel", "ts", "req")
	indicator.showDelay = 5 * time.Millisecond
	indicator.minShown = 40 * time.Millisecond
	go indicator.run()
	select {
	case event := <-recorder.events:
		if event != "add" {
			t.Fatalf("first event = %q, want add", event)
		}
	case <-time.After(time.Second):
		t.Fatal("processing reaction was not added")
	}
	finishedAt := time.Now()
	indicator.finish()
	select {
	case event := <-recorder.events:
		if event != "remove" {
			t.Fatalf("second event = %q, want remove", event)
		}
	case <-time.After(time.Second):
		t.Fatal("processing reaction was not removed")
	}
	if elapsed := time.Since(finishedAt); elapsed < 30*time.Millisecond {
		t.Fatalf("reaction was removed too soon after reply: %s", elapsed)
	}
}

type blockingReactionClient struct{ started chan struct{} }

func (c blockingReactionClient) AddReaction(ctx context.Context, _, _, _, _ string) error {
	close(c.started)
	<-ctx.Done()
	return ctx.Err()
}

func (blockingReactionClient) RemoveReaction(context.Context, string, string, string, string) error {
	return errors.New("should not remove a reaction that was never added")
}

func TestProcessingReactionCancelsPendingAddWhenReplyIsReady(t *testing.T) {
	client := blockingReactionClient{started: make(chan struct{})}
	indicator := newProcessingReactionIndicator(client, "token", "channel", "ts", "req")
	indicator.showDelay = 0
	go indicator.run()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("reaction add did not start")
	}
	indicator.finish()
}
