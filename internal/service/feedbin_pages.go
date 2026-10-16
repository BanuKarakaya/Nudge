package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"nudge/internal/feedbin"
)

const rssSummaryPageSize = 5

// BuildSummaryPage loads the requested inventory, but sends only one bounded
// batch to AI. The page size limits message length, never access to the inventory.
// No progress is returned on failure. The caller persists it only after delivery.
func (s *FeedbinDigestService) BuildSummaryPage(ctx context.Context, userID int64, progress RSSSummaryProgress, ai TextSummarizer) (string, RSSSummaryProgress, error) {
	connection, err := s.connections.GetByUserID(ctx, userID)
	if err != nil {
		return "", progress, err
	}
	entries, err := s.client.ListUnreadSince(ctx, connection.Email, connection.Password, progress.Since)
	if err != nil {
		return "", progress, err
	}
	// Stable newest-first pages; deduplicate feeds that publish the same URL.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].CreatedAt.After(entries[j].CreatedAt)
	})
	shown := make(map[string]bool)
	for _, key := range progress.ShownKeys {
		shown[key] = true
	}
	seen := make(map[string]bool)
	var pending []feedbin.Entry
	total := 0
	for _, entry := range entries {
		if entry.CreatedAt.Before(progress.Since) {
			continue
		}
		key := feedbinEntryKey(entry)
		if seen[key] {
			continue
		}
		seen[key] = true
		total++
		if !shown[key] {
			pending = append(pending, entry)
		}
	}
	progress.Total = total
	title := "📰 *Bugünkü RSS Özetin Beybi*"
	scope := "Günlük pencerendeki"
	if progress.Scope == "all_unread" {
		title = "📰 *Okunmamış RSS Özetlerin Beybi*"
		scope = "Tüm okunmamış listendeki"
	}
	if len(pending) == 0 {
		if total == 0 {
			return title + "\n\nBu kapsamda okunmamış RSS bulunmuyor.", progress, nil
		}
		return fmt.Sprintf("%s\n\n%s %d benzersiz kaydın tamamı bu konuşmada gösterildi; şu anda kalan kayıt yok.", title, scope, total), progress, nil
	}
	if ai == nil {
		return "", progress, fmt.Errorf("RSS summarizer unavailable")
	}
	page := pending[:min(rssSummaryPageSize, len(pending))]
	input := formatFeedbinAIInput(feedbinGroups{"RSS": page})
	var rendered string
	for attempt := 0; attempt < 2; attempt++ {
		request := input + "\nBu bölümdeki TÜM kayıtları ayrı ayrı özetle; hiçbirini atlama. Bu bir sayfadır, envanterin tamamı olduğu anlamına gelmez."
		if attempt > 0 {
			request += "\nÖnceki yanıt doğrulanamadı. Her mevcut kimliği tam bir kez kullan; konu ve önem cümlelerini tamamla."
		}
		raw, callErr := ai.Summarize(ctx, request)
		if callErr != nil {
			return "", progress, callErr
		}
		text, valid := RenderFeedbinAISummary(raw, input)
		if valid && len(RecommendedArticles(text)) == len(page) {
			rendered = text
			break
		}
	}
	if rendered == "" {
		return "", progress, fmt.Errorf("RSS page validation failed after retry")
	}
	// Do not mutate a slice shared with the previously persisted state.
	progress.ShownKeys = append([]string(nil), progress.ShownKeys...)
	for _, entry := range page {
		progress.ShownKeys = append(progress.ShownKeys, feedbinEntryKey(entry))
	}
	rendered = strings.Replace(rendered, "📰 *Bugünkü RSS Özetin Beybi*", title, 1)
	rendered = strings.Replace(rendered, "🔥 *Öne çıkanlar*", "*Bu bölümdeki makaleler*", 1)
	remaining := len(pending) - len(page)
	rendered += fmt.Sprintf("\n\n%s %d benzersiz kayıttan bu bölümde %d tanesini özetledim; kalan: %d.", scope, total, len(page), remaining)
	if remaining > 0 {
		rendered += "\nKalanları da bölümler halinde getirebilirim; devam etmemi söylemen yeterli. Bunun için ek erişim izni gerekmiyor."
	}
	return rendered, progress, nil
}

func feedbinEntryKey(entry feedbin.Entry) string {
	if entry.URL != "" {
		return "url:" + entry.URL
	}
	return fmt.Sprintf("entry:%d", entry.ID)
}
