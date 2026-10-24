package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"nudge/internal/domain"
	"nudge/internal/repository"
)

type NotificationClient interface {
	PostMessage(context.Context, string, string, string) error
	PostBookmarkMessage(context.Context, string, string, domain.Bookmark) error
}
type DeliveryWorker struct {
	Onboarding interface {
		DiscoverWorkspace(context.Context, int64) error
		DeliverInvitation(context.Context, int64, string) error
	}
	Notifications repository.NotificationRepository
	Installations repository.InstallationRepository
	Client        NotificationClient
}

func (w DeliveryWorker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		delivered, err := w.DeliverNext(ctx)
		if err != nil {
			log.Printf("notification delivery failed: %v", err)
		}
		if !delivered || err != nil {
			if !waitFor(ctx, 15*time.Second) {
				return
			}
		}
	}
}
func (w DeliveryWorker) DeliverNext(ctx context.Context) (bool, error) {
	claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	d, err := w.Notifications.ClaimNext(claimCtx)
	cancel()
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sendCtx, sendCancel := context.WithTimeout(ctx, 30*time.Second)
	if d.Message.WorkspaceOnboarding || d.Message.Connection != "" {
		if w.Onboarding == nil {
			err = errors.New("onboarding delivery unavailable")
		} else if d.Message.WorkspaceOnboarding {
			err = w.Onboarding.DiscoverWorkspace(sendCtx, d.UserID)
		} else {
			err = w.Onboarding.DeliverInvitation(sendCtx, d.UserID, d.Message.Connection)
		}
	} else {
		installation, installErr := w.Installations.GetByUserID(sendCtx, d.UserID)
		err = installErr
		if err == nil {
			if d.Message.Bookmark != nil {
				err = w.Client.PostBookmarkMessage(sendCtx, installation.BotToken, d.Message.Channel, *d.Message.Bookmark)
			} else {
				err = w.Client.PostMessage(sendCtx, installation.BotToken, d.Message.Channel, d.Message.Text)
			}
		}
	}
	sendCancel()
	// Persist retry/completion even after cancellation of the sending context.
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	if err != nil {
		retryErr := w.Notifications.Retry(finishCtx, d, retryDelay(d.Attempts))
		return true, errors.Join(fmt.Errorf("notification %d send failed: %w", d.ID, err), retryErr)
	}
	return true, w.Notifications.Complete(finishCtx, d)
}
func retryDelay(attempts int) time.Duration {
	return min(30*time.Second*time.Duration(1<<min(max(attempts-1, 0), 7)), time.Hour)
}
func waitFor(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Preparation retries retain the scheduled instant, so the period cannot drift.
// Previously enqueued messages are deduplicated by the persistent outbox.
func prepareWithRetry(ctx context.Context, job func(context.Context) error) {
	for attempt := 1; attempt <= 5; attempt++ {
		jobCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		err := job(jobCtx)
		cancel()
		if err == nil || ctx.Err() != nil {
			return
		}
		log.Printf("notification preparation failed attempt=%d: %v", attempt, err)
		if attempt < 5 && !waitFor(ctx, retryDelay(attempt)) {
			return
		}
	}
}
