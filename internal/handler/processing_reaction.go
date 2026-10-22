package handler

import (
	"context"
	"log"
	"sync"
	"time"
)

const (
	processingReactionDelay      = 250 * time.Millisecond
	processingReactionMinVisible = 700 * time.Millisecond
	processingReactionTimeout    = 3 * time.Second
)

type processingReactionIndicator struct {
	client    slackReactionClient
	token     string
	channel   string
	timestamp string
	requestID string
	done      chan struct{}
	stopped   chan struct{}
	once      sync.Once
	showDelay time.Duration
	minShown  time.Duration
}

func newProcessingReactionIndicator(client slackReactionClient, token, channel, timestamp, requestID string) *processingReactionIndicator {
	return &processingReactionIndicator{
		client: client, token: token, channel: channel, timestamp: timestamp, requestID: requestID,
		done: make(chan struct{}), stopped: make(chan struct{}),
		showDelay: processingReactionDelay, minShown: processingReactionMinVisible,
	}
}

func (i *processingReactionIndicator) finish() {
	i.once.Do(func() { close(i.done) })
	<-i.stopped
}

func (i *processingReactionIndicator) run() {
	defer close(i.stopped)
	showTimer := time.NewTimer(i.showDelay)
	defer showTimer.Stop()
	select {
	case <-i.done:
		return
	case <-showTimer.C:
	}

	addCtx, cancelAdd := context.WithTimeout(context.Background(), processingReactionTimeout)
	addFinished := make(chan struct{})
	go func() {
		select {
		case <-i.done:
			cancelAdd()
		case <-addFinished:
		}
	}()
	addStarted := time.Now()
	err := i.client.AddReaction(addCtx, i.token, i.channel, i.timestamp, processingReaction)
	close(addFinished)
	cancelAdd()
	if err != nil {
		select {
		case <-i.done:
			return // Work finished while Slack was still processing the add.
		default:
			log.Printf("slack_request=%s stage=processing_reaction_failed duration_ms=%d error=%q", i.requestID, time.Since(addStarted).Milliseconds(), err)
			return
		}
	}
	addedAt := time.Now()
	log.Printf("slack_request=%s stage=processing_reaction_added elapsed_ms=%d", i.requestID, time.Since(addStarted).Milliseconds())

	<-i.done
	if remaining := i.minShown - time.Since(addedAt); remaining > 0 {
		grace := time.NewTimer(remaining)
		<-grace.C
	}
	removeCtx, cancelRemove := context.WithTimeout(context.Background(), processingReactionTimeout)
	defer cancelRemove()
	if err := i.client.RemoveReaction(removeCtx, i.token, i.channel, i.timestamp, processingReaction); err != nil {
		log.Printf("slack_request=%s stage=processing_reaction_remove_failed error=%q", i.requestID, err)
	} else {
		log.Printf("slack_request=%s stage=processing_reaction_removed", i.requestID)
	}
}
