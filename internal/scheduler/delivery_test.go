package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"nudge/internal/domain"
	"nudge/internal/feedbin"
	"nudge/internal/repository"
	"nudge/internal/service"
)

type memoryOutbox struct {
	repository.NotificationRepository
	pending  []repository.NotificationDelivery
	known    map[string]bool
	complete []int64
	retries  []time.Duration
}

func (q *memoryOutbox) Exists(_ context.Context, u int64, k, p string) (bool, error) {
	return q.known[fmt.Sprint(u, k, p)], nil
}
func (q *memoryOutbox) Enqueue(_ context.Context, u int64, k, p string, m repository.NotificationMessage) error {
	if q.known == nil {
		q.known = map[string]bool{}
	}
	key := fmt.Sprint(u, k, p)
	if q.known[key] {
		return nil
	}
	q.known[key] = true
	q.pending = append(q.pending, repository.NotificationDelivery{ID: int64(len(q.pending) + 1), UserID: u, Message: m})
	return nil
}
func (q *memoryOutbox) ClaimNext(context.Context) (repository.NotificationDelivery, error) {
	if len(q.pending) == 0 {
		return repository.NotificationDelivery{}, pgx.ErrNoRows
	}
	d := q.pending[0]
	q.pending = q.pending[1:]
	d.Attempts++
	return d, nil
}
func (q *memoryOutbox) Complete(_ context.Context, d repository.NotificationDelivery) error {
	q.complete = append(q.complete, d.ID)
	return nil
}
func (q *memoryOutbox) Retry(_ context.Context, d repository.NotificationDelivery, delay time.Duration) error {
	q.retries = append(q.retries, delay)
	q.pending = append(q.pending, d)
	return nil
}

type installations struct {
	repository.InstallationRepository
}

func (installations) GetByUserID(context.Context, int64) (domain.SlackInstallation, error) {
	return domain.SlackInstallation{BotToken: "token", TeamID: "T"}, nil
}
func (installations) UpsertUser(context.Context, string, string) (int64, error) { return 1, nil }

type flakySlack struct {
	calls  map[string]int
	failed bool
}

func (s *flakySlack) PostMessage(_ context.Context, _, _, text string) error {
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[text]++
	if text == "second" && !s.failed {
		s.failed = true
		return errors.New("temporary failure")
	}
	return nil
}
func (s *flakySlack) PostBookmarkMessage(ctx context.Context, token, channel string, b domain.Bookmark) error {
	return s.PostMessage(ctx, token, channel, b.Title)
}
func TestFailedDeliveryRetriesWithoutReplayingSuccessfulBookmark(t *testing.T) {
	q := &memoryOutbox{}
	ctx := context.Background()
	for i, title := range []string{"first", "second"} {
		b := domain.Bookmark{ID: int64(i + 1), Title: title}
		if err := q.Enqueue(ctx, 1, "daily_bookmark", fmt.Sprint(i), repository.NotificationMessage{Channel: "D", Bookmark: &b}); err != nil {
			t.Fatal(err)
		}
	}
	client := &flakySlack{}
	w := DeliveryWorker{Notifications: q, Installations: installations{}, Client: client}
	if _, err := w.DeliverNext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := w.DeliverNext(ctx); err == nil {
		t.Fatal("expected transient error")
	}
	if len(q.complete) != 1 || len(q.retries) != 1 || q.retries[0] != 30*time.Second {
		t.Fatalf("incorrect delivery state: %+v", q)
	}
	// Recreate the worker while retaining the queue, as on a process restart.
	w = DeliveryWorker{Notifications: q, Installations: installations{}, Client: client}
	if _, err := w.DeliverNext(ctx); err != nil {
		t.Fatal(err)
	}
	if client.calls["first"] != 1 || client.calls["second"] != 2 || len(q.complete) != 2 {
		t.Fatalf("unexpected calls: %v", client.calls)
	}
	if busy, err := w.DeliverNext(ctx); busy || err != nil {
		t.Fatal("queue not drained")
	}
}
func TestFeedbinWindowAtBoundaryAndDelayedRetry(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	for _, stamp := range []string{"2026-10-23T23:00:00+03:00", "2026-10-23T23:00:01+03:00", "2026-10-24T00:10:00+03:00"} {
		now, _ := time.Parse(time.RFC3339, stamp)
		from, to := feedbinWindow(now, loc)
		if from.Format(time.RFC3339) != "2026-10-22T23:00:00+03:00" || to.Format(time.RFC3339) != "2026-10-23T23:00:00+03:00" {
			t.Fatalf("%s gave %s to %s", stamp, from, to)
		}
	}
	// Calendar-day windows also remain at 23:00 across daylight saving changes.
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	from, to := feedbinWindow(time.Date(2026, 10, 25, 23, 0, 0, 0, berlin), berlin)
	if from.Hour() != 23 || to.Hour() != 23 || to.Sub(from) != 25*time.Hour {
		t.Fatalf("DST window: %s to %s", from, to)
	}
}

type feedConnections struct {
	repository.FeedbinConnectionRepository
}

func (feedConnections) GetByUserID(context.Context, int64) (domain.FeedbinConnection, error) {
	return domain.FeedbinConnection{}, nil
}

type feedSource struct {
	since   time.Time
	entries []feedbin.Entry
	calls   int
}

func (s *feedSource) ListUnreadSince(_ context.Context, _, _ string, since time.Time) ([]feedbin.Entry, error) {
	s.since = since
	s.calls++
	return s.entries, nil
}
func (*feedSource) ListTaggings(context.Context, string, string) ([]feedbin.Tagging, error) {
	return nil, nil
}

type summaryFunc func(context.Context, string) (string, error)

func (f summaryFunc) Summarize(c context.Context, s string) (string, error) { return f(c, s) }

type workspaceSlack struct{}

func (workspaceSlack) ListUsers(context.Context, string) ([]domain.SlackMember, error) {
	return []domain.SlackMember{{ID: "U"}}, nil
}
func (workspaceSlack) OpenDirectMessage(context.Context, string, string) (string, error) {
	return "D", nil
}
func TestScheduledDigestIncludesPreviousDayAndExcludesNextWindow(t *testing.T) {
	loc := time.FixedZone("Istanbul", 3*60*60)
	now := time.Date(2026, 10, 23, 23, 0, 0, 0, loc)
	source := &feedSource{entries: []feedbin.Entry{
		{ID: 1, Title: "yesterday", URL: "https://example.test/1", CreatedAt: now.Add(-time.Hour)},
		{ID: 2, Title: "next-window", URL: "https://example.test/2", CreatedAt: now},
	}}
	q := &memoryOutbox{}
	calls := 0
	ai := summaryFunc(func(_ context.Context, input string) (string, error) {
		calls++
		if !strings.Contains(input, "yesterday") || strings.Contains(input, "next-window") {
			t.Fatal(input)
		}
		return `{"articles":[{"id":"RSS-1","topic":"Konu.","importance":"Önemli."}]}`, nil
	})
	r := NewFeedbinReminder(1, loc, installations{}, feedConnections{}, service.NewFeedbinDigestService(feedConnections{}, source), ai, q, workspaceSlack{})
	if err := r.RunOnce(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if !source.since.Equal(now.Add(-24*time.Hour)) || len(q.pending) != 1 {
		t.Fatalf("since=%s queued=%d", source.since, len(q.pending))
	}
	if err := r.RunOnce(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(q.pending) != 1 || calls != 1 || source.calls != 1 {
		t.Fatal("already queued digest was rebuilt")
	}
}
