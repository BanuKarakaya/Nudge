package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"nudge/internal/repository"
)

type workerJobStore struct {
	repository.SlackJobRepository
	job      repository.SlackJob
	claimErr error
	retries  int
	delay    time.Duration
}

func (q *workerJobStore) ClaimNext(context.Context) (repository.SlackJob, error) {
	return q.job, q.claimErr
}
func (q *workerJobStore) Retry(ctx context.Context, _ repository.SlackJob, delay time.Duration) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	q.retries++
	q.delay = delay
	return nil
}

func TestSlackWorkerRecoversInterruptedAttempts(t *testing.T) {
	for _, mode := range []string{"success", "interruption", "shutdown", "panic", "empty", "claim failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			q := &workerJobStore{job: repository.SlackJob{ID: 1, Attempts: 2}}
			if mode == "empty" {
				q.claimErr = pgx.ErrNoRows
			}
			if mode == "claim failure" {
				q.claimErr = errors.New("database offline")
			}
			calls := 0
			w := SlackJobWorker{Jobs: q, Process: func(run context.Context, _ repository.SlackJob) error {
				calls++
				deadline, ok := run.Deadline()
				if !ok || time.Until(deadline) > repository.SlackJobTimeout || repository.SlackJobTimeout >= repository.SlackJobLease {
					t.Fatal("worker is not bounded below lease")
				}
				switch mode {
				case "interruption":
					return errors.New("delivery failed")
				case "shutdown":
					cancel()
					return run.Err()
				case "panic":
					panic("sensitive details must not enter logs")
				}
				return nil
			}}
			worked, err := w.ProcessNext(ctx)
			if mode == "empty" || mode == "claim failure" {
				if worked || calls != 0 || q.retries != 0 || (mode == "empty" && err != nil) || (mode == "claim failure" && err == nil) {
					t.Fatal("claim failure ran work")
				}
				return
			}
			if !worked || calls != 1 {
				t.Fatal("job not processed")
			}
			if mode == "success" {
				if err != nil || q.retries != 0 {
					t.Fatal("completed work retried")
				}
				return
			}
			if err == nil || q.retries != 1 {
				t.Fatal("interrupted job not retained")
			}
			if mode == "shutdown" && q.delay != 0 {
				t.Fatal("graceful shutdown delayed recovery")
			}
			if mode != "shutdown" && q.delay != 10*time.Second {
				t.Fatal("retry backoff incorrect")
			}
		})
	}
}
