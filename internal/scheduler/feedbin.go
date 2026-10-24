package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"nudge/internal/repository"
	"nudge/internal/service"
)

const feedbinNotificationType = "daily_feedbin"

type FeedbinReminder struct {
	seedUserID    int64
	timezone      *time.Location
	installations repository.InstallationRepository
	connections   repository.FeedbinConnectionRepository
	generator     service.DigestGenerator
	notifications repository.NotificationRepository
	slack         SlackWorkspaceClient
}

func NewFeedbinReminder(seedUserID int64, timezone *time.Location, installations repository.InstallationRepository, connections repository.FeedbinConnectionRepository, generator service.DigestGenerator, notifications repository.NotificationRepository, slack SlackWorkspaceClient) *FeedbinReminder {
	return &FeedbinReminder{seedUserID: seedUserID, timezone: timezone, installations: installations, connections: connections, generator: generator, notifications: notifications, slack: slack}
}

func (r *FeedbinReminder) Run(ctx context.Context) {
	for {
		next := r.nextEvent(time.Now())
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			prepareWithRetry(ctx, func(jobCtx context.Context) error { return r.RunOnce(jobCtx, next) })
		}
	}
}

func (r *FeedbinReminder) RunOnce(ctx context.Context, now time.Time) error {
	installation, err := r.installations.GetByUserID(ctx, r.seedUserID)
	if err != nil {
		return fmt.Errorf("get workspace installation: %w", err)
	}
	members, err := r.slack.ListUsers(ctx, installation.BotToken)
	if err != nil {
		return fmt.Errorf("list workspace members: %w", err)
	}
	since, until := feedbinWindow(now, r.timezone)
	periodKey := until.Format("2006-01-02")
	var failures []error
	for _, member := range members {
		if member.ID == "" || member.Deleted || member.IsBot {
			continue
		}
		if err := r.processMember(ctx, installation.TeamID, installation.BotToken, member.ID, since, until, periodKey); err != nil {
			failures = append(failures, fmt.Errorf("Feedbin reminder user %s: %w", member.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (r *FeedbinReminder) processMember(ctx context.Context, teamID, botToken, slackUserID string, since, until time.Time, periodKey string) error {
	userID, err := r.installations.UpsertUser(ctx, teamID, slackUserID)
	if err != nil {
		return fmt.Errorf("save workspace user: %w", err)
	}
	if _, err := r.connections.GetByUserID(ctx, userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("check Feedbin connection: %w", err)
	}
	exists, err := r.notifications.Exists(ctx, userID, feedbinNotificationType, periodKey)
	if err != nil || exists {
		return err
	}
	digest, err := r.generator.Generate(ctx, userID, since, until)
	if err != nil {
		return fmt.Errorf("build Feedbin digest: %w", err)
	}
	dmChannel, err := r.slack.OpenDirectMessage(ctx, botToken, slackUserID)
	if err != nil {
		return fmt.Errorf("open direct message: %w", err)
	}
	return r.notifications.Enqueue(ctx, userID, feedbinNotificationType, periodKey, repository.NotificationMessage{Channel: dmChannel, Text: digest})
}

func (r *FeedbinReminder) nextEvent(now time.Time) time.Time {
	localNow := now.In(r.timezone)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 0, 0, 0, r.timezone)
	if !next.After(localNow) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// Most recent completed 23:00-to-23:00 window, in the configured timezone.
func feedbinWindow(now time.Time, location *time.Location) (time.Time, time.Time) {
	localNow := now.In(location)
	end := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 0, 0, 0, location)
	if localNow.Before(end) {
		end = end.AddDate(0, 0, -1)
	}
	return end.AddDate(0, 0, -1), end
}
