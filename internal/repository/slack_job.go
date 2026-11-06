package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// The lease exceeds the entire worker deadline, including delivery and cleanup.
// A crashed process is recoverable within this interval, without resetting live
// workers' claims when another instance starts.
const SlackJobLease = 4 * time.Minute
const SlackJobTimeout = 3 * time.Minute

var ErrSlackJobLeaseLost = errors.New("Slack job lease lost")

type SlackJobKey struct{ Team, Channel, Thread, User string }

type SlackJob struct {
	ID         int64
	Key        SlackJobKey
	EventKey   string
	Payload    json.RawMessage
	ClaimToken string
	Attempts   int
	Result     json.RawMessage
	Delivered  bool
}

type SlackJobRepository interface {
	Enqueue(context.Context, SlackJob) error
	ClaimNext(context.Context) (SlackJob, error)
	CheckLease(context.Context, SlackJob) error
	SaveResult(context.Context, SlackJob, json.RawMessage) error
	MarkDelivered(context.Context, SlackJob) error
	// Complete atomically saves delivered history, analytics and terminal status.
	Complete(context.Context, SlackJob, json.RawMessage, InteractionRecord, bool) error
	Retry(context.Context, SlackJob, time.Duration) error
}
