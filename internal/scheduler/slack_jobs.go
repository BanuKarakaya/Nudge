package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"nudge/internal/analytics"
	"nudge/internal/repository"
)

var errWorkerPanic = errors.New("worker panic")

type SlackJobWorker struct {
	Jobs    repository.SlackJobRepository
	Process func(context.Context, repository.SlackJob) error
}

func (w SlackJobWorker) Run(ctx context.Context) {
	// Match the default runtime concurrency. Claims preserve per-conversation
	// ordering while allowing four unrelated conversations to make progress.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				worked, err := w.ProcessNext(ctx)
				if err != nil {
					log.Print("Slack job attempt interrupted; retained for retry")
				}
				if (!worked || err != nil) && !waitFor(ctx, time.Second) {
					return
				}
			}
		}()
	}
	wg.Wait()
}

func (w SlackJobWorker) ProcessNext(ctx context.Context) (bool, error) {
	claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	job, err := w.Jobs.ClaimNext(claimCtx)
	cancel()
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	log.Printf("slack_job=%d stage=claimed attempt=%d response_ready=%t delivered=%t", job.ID, job.Attempts, len(job.Result) > 0, job.Delivered)
	runCtx, runCancel := context.WithTimeout(ctx, repository.SlackJobTimeout)
	err = w.process(runCtx, job)
	runCancel()
	if err == nil {
		log.Printf("slack_job=%d stage=finished attempt=%d", job.ID, job.Attempts)
		return true, nil
	}
	// Release only after processing has stopped. On hard crash the lease expires.
	delay := min(5*time.Second*time.Duration(1<<min(max(job.Attempts-1, 0), 6)), 5*time.Minute)
	if ctx.Err() != nil {
		delay = 0
	}
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer retryCancel()
	if errors.Is(err, errWorkerPanic) {
		if report, ok := w.Jobs.(repository.JobReporter); ok {
			_ = report.ReportJob(retryCtx, job, analytics.LiveEvent{Stage: "attempt_failed", ErrorCode: "worker_panic"})
		}
	}
	retryErr := w.Jobs.Retry(retryCtx, job, delay)
	return true, errors.Join(err, retryErr)
}

func (w SlackJobWorker) process(ctx context.Context, job repository.SlackJob) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("Slack job %d: %w", job.ID, errWorkerPanic)
		}
	}()
	return w.Process(ctx, job)
}
