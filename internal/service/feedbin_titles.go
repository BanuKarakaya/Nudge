package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"nudge/internal/feedbin"
)

// Scheduled titles do not depend on a model, article content or category lookup.
// The outbox splits large lists into independently retryable Slack messages.
type feedbinTitles struct{ digest *FeedbinDigestService }

func NewFeedbinTitles(digest *FeedbinDigestService) DigestGenerator {
	return &feedbinTitles{digest: digest}
}

func (g *feedbinTitles) Generate(ctx context.Context, userID int64, since, until time.Time) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if userID <= 0 || since.IsZero() || !since.Before(until) {
		return "", errors.New("invalid RSS identity or window")
	}
	if g.digest == nil || g.digest.connections == nil || g.digest.client == nil {
		return "", errors.New("RSS titles unavailable")
	}
	connection, err := g.digest.connections.GetByUserID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("load Feedbin connection: %w", err)
	}
	entries, err := g.digest.client.ListUnreadSince(ctx, connection.Email, connection.Password, since)
	if err != nil {
		return "", fmt.Errorf("load RSS titles: %w", err)
	}
	var items []feedbin.Entry
	seen := make(map[int64]bool)
	for _, entry := range entries {
		if entry.CreatedAt.Before(since) || !entry.CreatedAt.Before(until) || seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		items = append(items, entry)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	var text strings.Builder
	fmt.Fprintf(&text, "📰 *Günlük RSS başlıkların*\n%s – %s\n\n", since.Format("02.01.2006 15:04"), until.Format("02.01.2006 15:04"))
	if len(items) == 0 {
		text.WriteString("Bu zaman aralığında okunmamış RSS yazın yok.")
	}
	for _, entry := range items {
		title := strings.Join(strings.Fields(entry.Title), " ")
		if title == "" {
			title = "Başlıksız yazı"
		}
		// Feed titles cannot introduce Slack mentions or replace a link's label.
		title = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "|", "｜").Replace(title)
		u, parseErr := url.Parse(strings.TrimSpace(entry.URL))
		if parseErr == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
			link := strings.NewReplacer("|", "%7C", "<", "%3C", ">", "%3E", "&", "&amp;").Replace(u.String())
			fmt.Fprintf(&text, "• <%s|%s>\n", link, title)
		} else {
			fmt.Fprintf(&text, "• %s (bağlantı bulunamadı)\n", title)
		}
	}
	return text.String(), nil
}
