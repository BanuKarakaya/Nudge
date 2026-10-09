package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
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
	digest        *service.FeedbinDigestService
	ai            service.TextSummarizer
	messages      *service.SlackMessagingService
	notifications repository.NotificationRepository
	slack         SlackWorkspaceClient
}

func NewFeedbinReminder(seedUserID int64, timezone *time.Location, installations repository.InstallationRepository, connections repository.FeedbinConnectionRepository, digest *service.FeedbinDigestService, ai service.TextSummarizer, messages *service.SlackMessagingService, notifications repository.NotificationRepository, slack SlackWorkspaceClient) *FeedbinReminder {
	return &FeedbinReminder{seedUserID: seedUserID, timezone: timezone, installations: installations, connections: connections, digest: digest, ai: ai, messages: messages, notifications: notifications, slack: slack}
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
			if err := r.RunOnce(ctx, time.Now()); err != nil {
				log.Printf("Feedbin reminder failed: %v", err)
			}
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
	since := lastFeedbinStart(now, r.timezone)
	periodKey := now.In(r.timezone).Format("2006-01-02")
	for _, member := range members {
		if member.ID == "" || member.Deleted || member.IsBot {
			continue
		}
		if err := r.processMember(ctx, installation.TeamID, installation.BotToken, member.ID, since, periodKey); err != nil {
			log.Printf("Feedbin reminder for Slack user %s failed: %v", member.ID, err)
		}
	}
	return nil
}

func (r *FeedbinReminder) processMember(ctx context.Context, teamID, botToken, slackUserID string, since time.Time, periodKey string) error {
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
	digest, err := r.digest.BuildAnalysisInput(ctx, userID, since)
	if err != nil {
		return fmt.Errorf("build Feedbin digest: %w", err)
	}
	if r.ai != nil && !strings.Contains(digest, "yeni RSS yok") {
		if analyzed, aiErr := r.ai.Summarize(ctx, digest); aiErr == nil {
			digest = analyzed
		} else {
			log.Printf("Feedbin AI summary failed for user %d, using titles: %v", userID, aiErr)
		}
	}
	claimed, err := r.notifications.Claim(ctx, userID, feedbinNotificationType, periodKey)
	if err != nil || !claimed {
		return err
	}
	dmChannel, err := r.slack.OpenDirectMessage(ctx, botToken, slackUserID)
	if err != nil {
		return fmt.Errorf("open direct message: %w", err)
	}
	return r.messages.SendMessage(ctx, userID, dmChannel, digest)
}

func (r *FeedbinReminder) nextEvent(now time.Time) time.Time {
	localNow := now.In(r.timezone)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 0, 0, 0, r.timezone)
	if !next.After(localNow) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

func lastFeedbinStart(now time.Time, location *time.Location) time.Time {
	localNow := now.In(location)
	start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 0, 0, 0, location)
	if localNow.Before(start) {
		start = start.Add(-24 * time.Hour)
	}
	return start
}
