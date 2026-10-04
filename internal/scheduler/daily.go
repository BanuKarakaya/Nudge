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
	"nudge/internal/service"
)

const dailyNotificationType = "daily_bookmarks"

type SlackWorkspaceClient interface {
	ListUsers(ctx context.Context, token string) ([]domain.SlackMember, error)
	OpenDirectMessage(ctx context.Context, token, slackUserID string) (string, error)
}

type DailyReminder struct {
	seedUserID    int64
	publicURL     string
	timezone      *time.Location
	installations repository.InstallationRepository
	connections   repository.RaindropConnectionRepository
	sync          *service.RaindropSyncService
	bookmarks     service.BookmarkService
	messages      *service.SlackMessagingService
	bookmarkMsgs  *service.SlackBookmarkMessagingService
	notifications repository.NotificationRepository
	slack         SlackWorkspaceClient
}

func NewDailyReminder(seedUserID int64, publicURL string, timezone *time.Location,
	installations repository.InstallationRepository, connections repository.RaindropConnectionRepository,
	sync *service.RaindropSyncService, bookmarks service.BookmarkService,
	messages *service.SlackMessagingService, bookmarkMsgs *service.SlackBookmarkMessagingService,
	notifications repository.NotificationRepository, slackClient SlackWorkspaceClient) *DailyReminder {
	return &DailyReminder{seedUserID: seedUserID, publicURL: publicURL, timezone: timezone,
		installations: installations, connections: connections, sync: sync, bookmarks: bookmarks,
		messages: messages, bookmarkMsgs: bookmarkMsgs, notifications: notifications, slack: slackClient}
}

func (r *DailyReminder) Run(ctx context.Context) {
	for {
		wait := time.Until(r.nextRun(time.Now()))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if err := r.RunOnce(ctx, time.Now()); err != nil {
				log.Printf("daily reminder failed: %v", err)
			}
		}
	}
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
	for _, member := range members {
		if member.ID == "" || member.Deleted || member.IsBot {
			continue
		}
		if err := r.processMember(ctx, installation.TeamID, installation.BotToken, member.ID, from, to, periodKey); err != nil {
			log.Printf("daily reminder for Slack user %s failed: %v", member.ID, err)
		}
	}
	return nil
}

func (r *DailyReminder) processMember(ctx context.Context, teamID, botToken, slackUserID string, from, to time.Time, periodKey string) error {
	userID, err := r.installations.UpsertUser(ctx, teamID, slackUserID)
	if err != nil {
		return fmt.Errorf("save workspace user: %w", err)
	}
	dmChannel, err := r.slack.OpenDirectMessage(ctx, botToken, slackUserID)
	if err != nil {
		return fmt.Errorf("open direct message: %w", err)
	}
	if _, err := r.connections.GetByUserID(ctx, userID); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check Raindrop connection: %w", err)
		}
		claimed, claimErr := r.notifications.Claim(ctx, userID, "raindrop_connection", "initial")
		if claimErr != nil || !claimed {
			return claimErr
		}
		link := fmt.Sprintf("%s/raindrop/install?user_id=%d", r.publicURL, userID)
		return r.messages.SendMessage(ctx, userID, dmChannel, "Nudge’yi kullanmak için önce Raindrop hesabını bağla:\n"+link)
	}
	if _, err := r.sync.SyncLatest(ctx, userID); err != nil {
		return fmt.Errorf("sync Raindrop bookmarks: %w", err)
	}
	bookmarks, err := r.bookmarks.ListByDate(ctx, userID, from, to)
	if err != nil {
		return fmt.Errorf("list daily bookmarks: %w", err)
	}
	claimed, err := r.notifications.Claim(ctx, userID, dailyNotificationType, periodKey)
	if err != nil || !claimed {
		return err
	}
	if len(bookmarks) == 0 {
		return r.messages.SendMessage(ctx, userID, dmChannel, "📚 *Bugün kaydettikleriniz*\n\nBugün kaydettiğiniz bir şey yok.")
	}
	for _, bookmark := range bookmarks {
		if err := r.bookmarkMsgs.SendBookmark(ctx, userID, bookmark.ID, dmChannel); err != nil {
			return fmt.Errorf("send bookmark %d: %w", bookmark.ID, err)
		}
	}
	return nil
}

func (r *DailyReminder) nextRun(now time.Time) time.Time {
	localNow := now.In(r.timezone)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 22, 0, 0, 0, r.timezone)
	if !next.After(localNow) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
