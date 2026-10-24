package scheduler

import (
	"context"
	"log"
	"time"
)

// Reconcile existing installations on startup and recover failed OAuth enqueue
// attempts. Durable daily discovery keys and per-service invitation keys dedupe.
func RunOnboarding(ctx context.Context, seeds interface {
	ListWorkspaceSeeds(context.Context) ([]int64, error)
}, onboarding interface {
	EnqueueWorkspace(context.Context, int64) error
}) {
	for ctx.Err() == nil {
		scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		ids, err := seeds.ListWorkspaceSeeds(scanCtx)
		if err != nil {
			log.Print("onboarding stage=workspace_scan_failed")
		}
		for _, id := range ids {
			if err := onboarding.EnqueueWorkspace(scanCtx, id); err != nil {
				log.Printf("onboarding stage=enqueue_failed user_id=%d", id)
			}
		}
		cancel()
		if !waitFor(ctx, 5*time.Minute) {
			return
		}
	}
}
