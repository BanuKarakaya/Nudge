package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"nudge/internal/repository"
	"nudge/internal/service"
)

const dailyNotificationType = "daily_bookmarks"

type DailyReminder struct {
	userID        int64
	channel       string
	timezone      *time.Location
	sync          *service.RaindropSyncService
	bookmarks     service.BookmarkService
	messages      *service.SlackMessagingService
	bookmarkMsgs  *service.SlackBookmarkMessagingService
	notifications repository.NotificationRepository
}

func NewDailyReminder(
	userID int64,
	channel string,
	timezone *time.Location,
	sync *service.RaindropSyncService,
	bookmarks service.BookmarkService,
	messages *service.SlackMessagingService,
	bookmarkMsgs *service.SlackBookmarkMessagingService,
	notifications repository.NotificationRepository,
) *DailyReminder {
	return &DailyReminder{
		userID: userID, channel: channel, timezone: timezone,
		sync: sync, bookmarks: bookmarks, messages: messages,
		bookmarkMsgs: bookmarkMsgs, notifications: notifications,
	}
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
	localNow := now.In(r.timezone)
	periodKey := localNow.Format("2006-01-02")
	claimed, err := r.notifications.Claim(ctx, r.userID, dailyNotificationType, periodKey)
	if err != nil || !claimed {
		return err
	}

	if _, err := r.sync.SyncLatest(ctx, r.userID); err != nil {
		return fmt.Errorf("sync Raindrop bookmarks: %w", err)
	}
	from := localNow.Add(-24 * time.Hour).UTC()
	to := localNow.UTC()
	bookmarks, err := r.bookmarks.ListByDate(ctx, r.userID, from, to)
	if err != nil {
		return fmt.Errorf("list daily bookmarks: %w", err)
	}
	if len(bookmarks) == 0 {
		return r.messages.SendMessage(ctx, r.userID, r.channel, "📚 *Bugün kaydettikleriniz*\n\nBugün kaydettiğiniz bir şey yok.")
	}
	for _, bookmark := range bookmarks {
		if err := r.bookmarkMsgs.SendBookmark(ctx, r.userID, bookmark.ID, r.channel); err != nil {
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
