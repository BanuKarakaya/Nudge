package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/secure"
	"nudge/internal/service"
)

const dailyNotificationType = "daily_bookmarks"

type SlackWorkspaceClient interface {
	ListUsers(ctx context.Context, token string) ([]domain.SlackMember, error)
	OpenDirectMessage(ctx context.Context, token, slackUserID string) (string, error)
}

type DailyReminder struct {
	onboarding interface {
		EnqueueWorkspace(context.Context, int64) error
	}
	seedUserID    int64
	publicURL     string
	timezone      *time.Location
	installations repository.InstallationRepository
	connections   repository.RaindropConnectionRepository
	feedbin       repository.FeedbinConnectionRepository
	sync          *service.RaindropSyncService
	bookmarks     service.BookmarkService
	links         secure.LinkSigner
	notifications repository.NotificationRepository
	slack         SlackWorkspaceClient
}

func (r *DailyReminder) WithOnboarding(s interface {
	EnqueueWorkspace(context.Context, int64) error
}) *DailyReminder { r.onboarding = s; return r }

func NewDailyReminder(seedUserID int64, publicURL string, timezone *time.Location,
	installations repository.InstallationRepository, connections repository.RaindropConnectionRepository,
	feedbin repository.FeedbinConnectionRepository,
	sync *service.RaindropSyncService, bookmarks service.BookmarkService,
	links secure.LinkSigner,
	notifications repository.NotificationRepository, slackClient SlackWorkspaceClient) *DailyReminder {
	return &DailyReminder{seedUserID: seedUserID, publicURL: publicURL, timezone: timezone,
		installations: installations, connections: connections, feedbin: feedbin, sync: sync, bookmarks: bookmarks,
		links: links, notifications: notifications, slack: slackClient}
}

func (r *DailyReminder) Run(ctx context.Context) {
	for {
		next, onboarding := r.nextEvent(time.Now())
		wait := time.Until(next)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			prepareWithRetry(ctx, func(jobCtx context.Context) error {
				if onboarding {
					return r.SendOnboardingOnce(jobCtx)
				}
				return r.RunOnce(jobCtx, next)
			})
		}
	}
}

func (r *DailyReminder) SendOnboardingOnce(ctx context.Context) error {
	if r.onboarding != nil {
		return r.onboarding.EnqueueWorkspace(ctx, r.seedUserID)
	}
	installation, err := r.installations.GetByUserID(ctx, r.seedUserID)
	if err != nil {
		return fmt.Errorf("get workspace installation: %w", err)
	}
	members, err := r.slack.ListUsers(ctx, installation.BotToken)
	if err != nil {
		return fmt.Errorf("list workspace members: %w", err)
	}
	var failures []error
	for _, member := range members {
		if member.ID == "" || member.Deleted || member.IsBot {
			continue
		}
		if err := r.processOnboardingMember(ctx, installation.TeamID, installation.BotToken, member.ID); err != nil {
			failures = append(failures, fmt.Errorf("onboarding user %s: %w", member.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (r *DailyReminder) processOnboardingMember(ctx context.Context, teamID, botToken, slackUserID string) error {
	userID, err := r.installations.UpsertUser(ctx, teamID, slackUserID)
	if err != nil {
		return fmt.Errorf("save workspace user: %w", err)
	}
	_, raindropErr := r.connections.GetByUserID(ctx, userID)
	if raindropErr != nil && !errors.Is(raindropErr, pgx.ErrNoRows) {
		return fmt.Errorf("check Raindrop connection: %w", raindropErr)
	}
	_, feedbinErr := r.feedbin.GetByUserID(ctx, userID)
	if feedbinErr != nil && !errors.Is(feedbinErr, pgx.ErrNoRows) {
		return fmt.Errorf("check Feedbin connection: %w", feedbinErr)
	}
	if raindropErr == nil && feedbinErr == nil {
		return nil
	}
	dmChannel, err := r.slack.OpenDirectMessage(ctx, botToken, slackUserID)
	if err != nil {
		return fmt.Errorf("open direct message: %w", err)
	}
	// Refresh expired connection invitations weekly until the account is linked.
	year, week := time.Now().In(r.timezone).ISOWeek()
	period := fmt.Sprintf("%d-W%02d", year, week)
	for _, account := range []struct {
		purpose string
		missing bool
	}{
		{"raindrop", raindropErr != nil}, {"feedbin", feedbinErr != nil},
	} {
		if !account.missing {
			continue
		}
		link, err := r.links.URL(r.publicURL, userID, account.purpose, time.Now())
		if err != nil {
			return err
		}
		message := "Nudge’yi kullanmak için hesabını bağla:\n\n• " + account.purpose + ": " + link
		if err := r.notifications.Enqueue(ctx, userID, account.purpose+"_connection", period,
			repository.NotificationMessage{Channel: dmChannel, Text: message}); err != nil {
			return err
		}
	}
	return nil
}

func (r *DailyReminder) RunOnce(ctx context.Context, now time.Time) error {
	installation, err := r.installations.GetByUserID(ctx, r.seedUserID)
	if err != nil {
		return fmt.Errorf("get workspace installation: %w", err)
	}
	members, err := r.slack.ListUsers(ctx, installation.BotToken)
	if err != nil {
		return fmt.Errorf("list workspace members: %w", err)
	}
	localNow := now.In(r.timezone)
	from, to := localNow.Add(-24*time.Hour).UTC(), localNow.UTC()
	periodKey := localNow.Format("2006-01-02")
	var failures []error
	for _, member := range members {
		if member.ID == "" || member.Deleted || member.IsBot {
			continue
		}
		if err := r.processMember(ctx, installation.TeamID, installation.BotToken, member.ID, from, to, periodKey); err != nil {
			failures = append(failures, fmt.Errorf("daily reminder user %s: %w", member.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (r *DailyReminder) processMember(ctx context.Context, teamID, botToken, slackUserID string, from, to time.Time, periodKey string) error {
	userID, err := r.installations.UpsertUser(ctx, teamID, slackUserID)
	if err != nil {
		return fmt.Errorf("save workspace user: %w", err)
	}
	exists, err := r.notifications.Exists(ctx, userID, dailyNotificationType, periodKey)
	if err != nil || exists {
		return err
	}
	dmChannel, err := r.slack.OpenDirectMessage(ctx, botToken, slackUserID)
	if err != nil {
		return fmt.Errorf("open direct message: %w", err)
	}
	if _, err := r.connections.GetByUserID(ctx, userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("check Raindrop connection: %w", err)
	}
	if _, err := r.sync.SyncLatest(ctx, userID); err != nil {
		return fmt.Errorf("sync Raindrop bookmarks: %w", err)
	}
	bookmarks, err := r.bookmarks.ListByDate(ctx, userID, from, to)
	if err != nil {
		return fmt.Errorf("list daily bookmarks: %w", err)
	}
	if len(bookmarks) == 0 {
		return r.notifications.Enqueue(ctx, userID, dailyNotificationType, periodKey,
			repository.NotificationMessage{Channel: dmChannel, Text: "📚 *Bugün kaydettikleriniz*\n\nBugün kaydettiğiniz bir şey yok."})
	}
	// Each bookmark is its own delivery. A partial Slack failure never replays
	// the successfully delivered part of the batch.
	for _, bookmark := range bookmarks {
		key := fmt.Sprintf("%s/%d", periodKey, bookmark.ID)
		if err := r.notifications.Enqueue(ctx, userID, "daily_bookmark", key,
			repository.NotificationMessage{Channel: dmChannel, Bookmark: &bookmark}); err != nil {
			return err
		}
	}
	return nil
}

func (r *DailyReminder) nextEvent(now time.Time) (time.Time, bool) {
	localNow := now.In(r.timezone)
	onboarding := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 13, 45, 0, 0, r.timezone)
	reminder := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 22, 0, 0, 0, r.timezone)
	if !onboarding.After(localNow) {
		onboarding = onboarding.AddDate(0, 0, 1)
	}
	if !reminder.After(localNow) {
		reminder = reminder.AddDate(0, 0, 1)
	}
	if onboarding.Before(reminder) {
		return onboarding, true
	}
	return reminder, false
}
