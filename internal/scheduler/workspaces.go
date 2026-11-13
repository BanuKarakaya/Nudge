package scheduler

import (
	"context"
	"log/slog"
	"time"
)

type WorkspaceSchedule struct {
	User           int64
	Team, Timezone string
}
type WorkspaceSchedules interface {
	ReminderWorkspaces(context.Context) ([]WorkspaceSchedule, error)
	ClaimSchedule(context.Context, string, string, string) (string, error)
	FinishSchedule(context.Context, string, string, string, string, bool) error
}

// Polling recovers the latest completed daily window after a restart. Persistent
// claims and the notification outbox make multiple scheduler replicas safe.
func RunWorkspaceSchedules(ctx context.Context, store WorkspaceSchedules, run func(context.Context, WorkspaceSchedule, string, time.Time) error) {
	for ctx.Err() == nil {
		scan, cancel := context.WithTimeout(ctx, 10*time.Second)
		workspaces, err := store.ReminderWorkspaces(scan)
		cancel()
		if err != nil {
			slog.Error("reminder_workspace_scan_failed")
		}
		for _, w := range workspaces {
			zone, err := time.LoadLocation(w.Timezone)
			if err != nil {
				continue
			}
			for _, event := range []struct {
				kind string
				hour int
			}{{"bookmarks", 22}, {"feedbin", 23}} {
				now := time.Now().In(zone)
				due := time.Date(now.Year(), now.Month(), now.Day(), event.hour, 0, 0, 0, zone)
				if due.After(now) {
					due = due.AddDate(0, 0, -1)
				}
				period := due.Format("2006-01-02")
				claim, err := store.ClaimSchedule(ctx, w.Team, event.kind, period)
				if err != nil || claim == "" {
					continue
				}
				job, cancel := context.WithTimeout(ctx, 10*time.Minute)
				err = run(job, w, event.kind, due)
				cancel()
				finish, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				saveErr := store.FinishSchedule(finish, w.Team, event.kind, period, claim, err == nil)
				done()
				if err != nil || saveErr != nil {
					slog.Error("reminder_failed", "kind", event.kind)
				}
			}
		}
		if !waitFor(ctx, time.Minute) {
			return
		}
	}
}
