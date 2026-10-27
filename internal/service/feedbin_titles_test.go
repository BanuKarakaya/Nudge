package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"nudge/internal/feedbin"
)

func TestFeedbinTitlesUsesBoundAccountAndExactWindowWithoutAI(t *testing.T) {
	loc := time.FixedZone("Istanbul", 3*60*60)
	until := time.Date(2026, 10, 27, 23, 0, 0, 0, loc)
	since := until.AddDate(0, 0, -1)
	connections := &digestConnections{}
	source := &digestSource{tagErr: errors.New("categories unavailable"), entries: []feedbin.Entry{
		{ID: 3, Title: "Son yazı", URL: "https://example.com/3", CreatedAt: until.Add(-time.Nanosecond)},
		{ID: 1, Title: "Önceki gün", CreatedAt: since.Add(-time.Nanosecond)},
		{ID: 2, Title: "Başlangıç & <@everyone> | haber", URL: "https://example.com/2?a=1&b=2", CreatedAt: since},
		{ID: 2, Title: "Yinelenen", CreatedAt: since},
		{ID: 4, Title: "Yarın", CreatedAt: until},
	}}
	text, err := NewFeedbinTitles(NewFeedbinDigestService(connections, source)).Generate(context.Background(), 42, since, until)
	if err != nil {
		t.Fatal(err)
	}
	if len(connections.users) != 1 || connections.users[0] != 42 || source.calls != 1 {
		t.Fatalf("wrong account/fetches: %+v %d", connections.users, source.calls)
	}
	for _, unwanted := range []string{"Önceki gün", "Yinelenen", "Yarın", "<@everyone>"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("unexpected %q in %s", unwanted, text)
		}
	}
	if !strings.Contains(text, "26.10.2026 23:00 – 27.10.2026 23:00") || !strings.Contains(text, "<https://example.com/2?a=1&amp;b=2|Başlangıç &amp; &lt;@everyone&gt; ｜ haber>") || strings.Index(text, "Başlangıç") > strings.Index(text, "Son yazı") {
		t.Fatal(text)
	}
}

func TestFeedbinTitlesKeepsLargeInventoryAndDoesNotHideSourceFailure(t *testing.T) {
	since := time.Date(2026, 10, 26, 20, 0, 0, 0, time.UTC)
	source := &digestSource{}
	for i := 0; i < 1000; i++ {
		source.entries = append(source.entries, feedbin.Entry{ID: int64(i + 1), Title: fmt.Sprintf("Türkçe başlık %04d", i), URL: fmt.Sprintf("https://example.com/%d", i), CreatedAt: since})
	}
	generator := NewFeedbinTitles(NewFeedbinDigestService(&digestConnections{}, source))
	text, err := generator.Generate(context.Background(), 42, since, since.Add(24*time.Hour))
	if err != nil || strings.Count(text, "• <") != 1000 || !strings.Contains(text, "Türkçe başlık 0999") {
		t.Fatalf("lost titles: %d, %v", len(text), err)
	}
	source.err = errors.New("Feedbin unavailable")
	if text, err = generator.Generate(context.Background(), 42, since, since.Add(24*time.Hour)); err == nil || text != "" {
		t.Fatalf("provider error hidden: %q %v", text, err)
	}
	source.err = nil
	source.entries = nil
	if text, err = generator.Generate(context.Background(), 42, since, since.Add(24*time.Hour)); err != nil || !strings.Contains(text, "okunmamış RSS yazın yok") {
		t.Fatalf("empty inventory: %q %v", text, err)
	}
}

func TestFeedbinTitlesRejectsInvalidJobs(t *testing.T) {
	since := time.Now()
	source := &digestSource{}
	generator := NewFeedbinTitles(NewFeedbinDigestService(&digestConnections{}, source))
	for _, job := range []struct {
		user       int64
		start, end time.Time
	}{{0, since, since.Add(time.Hour)}, {1, time.Time{}, since}, {1, since, since}, {1, since, since.Add(-time.Hour)}} {
		if _, err := generator.Generate(context.Background(), job.user, job.start, job.end); err == nil {
			t.Fatal("accepted invalid job")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := generator.Generate(ctx, 1, since, since.Add(time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if source.calls != 0 {
		t.Fatal("invalid job fetched RSS")
	}
}
