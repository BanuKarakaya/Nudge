package service

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
	"time"

	"nudge/internal/feedbin"
	"nudge/internal/repository"
)

type FeedbinDigestClient interface {
	ListUnreadSince(ctx context.Context, email, password string, since time.Time) ([]feedbin.Entry, error)
	ListTaggings(ctx context.Context, email, password string) ([]feedbin.Tagging, error)
}

type FeedbinDigestService struct {
	connections repository.FeedbinConnectionRepository
	client      FeedbinDigestClient
}

func NewFeedbinDigestService(connections repository.FeedbinConnectionRepository, client FeedbinDigestClient) *FeedbinDigestService {
	return &FeedbinDigestService{connections: connections, client: client}
}

type feedbinGroups map[string][]feedbin.Entry

func (s *FeedbinDigestService) loadGroups(ctx context.Context, userID int64, since time.Time) (feedbinGroups, error) {
	connection, err := s.connections.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	entries, err := s.client.ListUnreadSince(ctx, connection.Email, connection.Password, since)
	if err != nil {
		return nil, err
	}
	taggings, err := s.client.ListTaggings(ctx, connection.Email, connection.Password)
	if err != nil {
		return nil, err
	}
	foldersByFeed := make(map[int64][]string)
	for _, tagging := range taggings {
		foldersByFeed[tagging.FeedID] = append(foldersByFeed[tagging.FeedID], tagging.Name)
	}
	groups := make(feedbinGroups)
	for _, entry := range entries {
		folders := foldersByFeed[entry.FeedID]
		if len(folders) == 0 {
			folders = []string{"Diğer"}
		}
		for _, folder := range folders {
			groups[folder] = append(groups[folder], entry)
		}
	}
	return groups, nil
}

func (s *FeedbinDigestService) BuildDigest(ctx context.Context, userID int64, since time.Time) (string, error) {
	return s.BuildAnalysisInput(ctx, userID, since)
}

const SummaryUnavailable = "RSS özetini şu anda tamamlayamadım. Biraz sonra tekrar deneyebilir misin?"

// BuildSummary loads one snapshot and never substitutes a title list for a summary.
func (s *FeedbinDigestService) BuildSummary(ctx context.Context, userID int64, since time.Time, ai TextSummarizer) (string, error) {
	groups, err := s.loadGroups(ctx, userID, since)
	if err != nil {
		return "", err
	}
	if len(groups) == 0 {
		return formatFeedbinList(groups), nil
	}
	if ai == nil {
		return "", fmt.Errorf("RSS summarizer unavailable")
	}
	return summarizeFeedbinInput(ctx, ai, formatFeedbinAIInput(groups))
}

func summarizeFeedbinInput(ctx context.Context, ai TextSummarizer, input string) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		request := input
		if attempt > 0 {
			request += "\nÖnceki yanıt doğrulanamadı. Yalnızca kaynakta bulunan kimlikleri kullanarak bütün alanları dolu, geçerli articles JSON nesnesi döndür."
		}
		raw, err := ai.Summarize(ctx, request)
		if err != nil {
			return "", err
		}
		if rendered, ok := RenderFeedbinAISummary(raw, input); ok {
			return rendered, nil
		}
	}
	return "", fmt.Errorf("RSS summary validation failed after retry")
}

func (s *FeedbinDigestService) BuildRecommendation(ctx context.Context, userID int64, since time.Time, ai RSSRecommender) (string, error) {
	groups, err := s.loadGroups(ctx, userID, since)
	if err != nil {
		return "", err
	}
	if len(groups) == 0 {
		return formatFeedbinList(groups), nil
	}
	if ai == nil {
		return "", fmt.Errorf("RSS recommender unavailable")
	}
	input := formatFeedbinAIInput(groups)
	for attempt := 0; attempt < 2; attempt++ {
		raw, recommendErr := ai.Recommend(ctx, input)
		if recommendErr != nil {
			return "", recommendErr
		}
		if rendered, ok := RenderFeedbinRecommendation(raw, input); ok {
			return rendered, nil
		}
		input += "\nYanıtı yalnızca geçerli JSON olarak ve tek bir mevcut RSS kimliğiyle döndür."
	}
	return "", fmt.Errorf("RSS recommendation validation failed after retry")
}

func (s *FeedbinDigestService) BuildAnalysisInput(ctx context.Context, userID int64, since time.Time) (string, error) {
	groups, err := s.loadGroups(ctx, userID, since)
	if err != nil {
		return "", err
	}
	return formatFeedbinList(groups), nil
}

// BuildAIInput includes short article excerpts so the model can explain what
// an article is about instead of guessing from its title alone.
func (s *FeedbinDigestService) BuildAIInput(ctx context.Context, userID int64, since time.Time) (string, error) {
	groups, err := s.loadGroups(ctx, userID, since)
	if err != nil {
		return "", err
	}
	return formatFeedbinAIInput(groups), nil
}

func (s *FeedbinDigestService) BuildUnreadStats(ctx context.Context, userID int64) (string, error) {
	connection, err := s.connections.GetByUserID(ctx, userID)
	if err != nil {
		return "", err
	}
	entries, err := s.client.ListUnreadSince(ctx, connection.Email, connection.Password, time.Time{})
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "📊 *RSS durumun Beybi*\n\nŞu anda okunmamış RSS’in yok.", nil
	}
	oldest := entries[0].CreatedAt
	for _, entry := range entries[1:] {
		if entry.CreatedAt.Before(oldest) {
			oldest = entry.CreatedAt
		}
	}
	return fmt.Sprintf("📊 *RSS durumun Beybi*\n\n• Toplam okunmamış RSS: *%d*\n• Okumadığın dönem: *%s* tarihinden beri", len(entries), oldest.In(time.FixedZone("Europe/Istanbul", 3*60*60)).Format("02.01.2006")), nil
}

func sortedFolders(groups feedbinGroups) []string {
	folders := make([]string, 0, len(groups))
	for folder := range groups {
		folders = append(folders, folder)
	}
	sort.Strings(folders)
	return folders
}

func sortedItems(items []feedbin.Entry) []feedbin.Entry {
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}

func formatFeedbinList(groups feedbinGroups) string {
	if len(groups) == 0 {
		return "📰 *Bugünkü RSS Özetin Beybi*\n\nDün 23.00’den beri okunmamış yeni RSS yok."
	}
	var b strings.Builder
	b.WriteString("📰 *Bugünkü RSS Özetin Beybi*\n")
	for _, folder := range sortedFolders(groups) {
		items := sortedItems(groups[folder])
		fmt.Fprintf(&b, "\n*%s · %d okunmamış*\n", folder, len(items))
		for _, item := range items {
			fmt.Fprintf(&b, "• <%s|%s>\n", item.URL, slackSafeTitle(item.Title))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatFeedbinAIInput(groups feedbinGroups) string {
	if len(groups) == 0 {
		return "📰 *Bugünkü RSS Özetin Beybi*\n\nDün 23.00’den beri okunmamış yeni RSS yok."
	}
	var b strings.Builder
	articleNumber := 0
	for _, folder := range sortedFolders(groups) {
		for _, item := range sortedItems(groups[folder]) {
			articleNumber++
			fmt.Fprintf(&b, "[RSS-%d]\nKlasör: %s\nBaşlık: %s\nURL: %s\nİçerik özeti: %s\n\n", articleNumber, folder, slackSafeTitle(item.Title), item.URL, contentSnippet(item))
		}
	}
	return strings.TrimSpace(b.String())
}

var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

func contentSnippet(item feedbin.Entry) string {
	content := item.Content
	if strings.TrimSpace(content) == "" {
		content = item.Summary
	}
	content = html.UnescapeString(htmlTagPattern.ReplaceAllString(content, " "))
	content = strings.Join(strings.Fields(content), " ")
	if content == "" {
		return "İçerik özeti yok; yalnızca başlığa göre değerlendir."
	}
	if len([]rune(content)) > 1200 {
		content = string([]rune(content)[:1200]) + "…"
	}
	return content
}

func slackSafeTitle(title string) string {
	return strings.ReplaceAll(strings.ReplaceAll(title, "|", "-"), "\n", " ")
}

func RenderFeedbinAISummary(summary, input string) (string, bool) {
	summary = strings.TrimSpace(summary)
	if strings.HasPrefix(summary, "```json\n") && strings.HasSuffix(summary, "```") {
		summary = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(summary, "```json\n"), "```"))
	}
	links := make(map[string]string)
	var currentID, currentTitle, currentURL string
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[RSS-") && strings.HasSuffix(line, "]") {
			currentID = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			currentTitle, currentURL = "", ""
		} else if strings.HasPrefix(line, "Başlık: ") {
			currentTitle = strings.TrimPrefix(line, "Başlık: ")
		} else if strings.HasPrefix(line, "URL: ") {
			currentURL = strings.TrimPrefix(line, "URL: ")
			if currentID != "" {
				links[currentID] = fmt.Sprintf("<%s|%s>", currentURL, slackSafeTitle(currentTitle))
			}
		}
	}

	var result struct {
		Articles []struct {
			ID         string `json:"id"`
			Topic      string `json:"topic"`
			Importance string `json:"importance"`
		} `json:"articles"`
	}
	if json.Unmarshal([]byte(summary), &result) != nil || len(result.Articles) == 0 || len(result.Articles) > 5 {
		return "", false
	}
	if len(links) >= 3 && len(result.Articles) < 3 {
		return "", false
	}
	var output strings.Builder
	output.WriteString("📰 *Bugünkü RSS Özetin Beybi*\n\n🔥 *Öne çıkanlar*\n")
	seen := make(map[string]bool)
	for _, article := range result.Articles {
		link, exists := links[article.ID]
		if !exists || seen[article.ID] || !completeSummarySentence(article.Topic) || !completeSummarySentence(article.Importance) {
			return "", false
		}
		seen[article.ID] = true
		escape := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
		fmt.Fprintf(&output, "\n• %s\nKonu: %s\nNeden önemli: %s\n", link, escape.Replace(article.Topic), escape.Replace(article.Importance))
	}
	return strings.TrimSpace(output.String()), true
}

func RenderFeedbinRecommendation(raw, input string) (string, bool) {
	var result struct {
		ID         string `json:"id"`
		Topic      string `json:"topic"`
		Importance string `json:"importance"`
	}
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "```json"), "```"))
	if json.Unmarshal([]byte(raw), &result) != nil || result.ID == "" || !completeSummarySentence(result.Topic) || !completeSummarySentence(result.Importance) {
		return "", false
	}
	var title, url string
	currentID := ""
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[RSS-") && strings.HasSuffix(line, "]") {
			currentID = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
		} else if currentID == result.ID && strings.HasPrefix(line, "Başlık: ") {
			title = strings.TrimPrefix(line, "Başlık: ")
		} else if currentID == result.ID && strings.HasPrefix(line, "URL: ") {
			url = strings.TrimPrefix(line, "URL: ")
		}
	}
	if title == "" || url == "" {
		return "", false
	}
	escape := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return fmt.Sprintf("🎯 *Bugün bunu okumanı öneriyorum Beybi*\n\n• <%s|%s>\nKonu: %s\nNeden bunu seçtim: %s", url, slackSafeTitle(title), escape.Replace(result.Topic), escape.Replace(result.Importance)), true
}

func completeSummarySentence(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > 600 || strings.Contains(text, "RSS-") || strings.Contains(text, "http") {
		return false
	}
	return strings.HasSuffix(text, ".") || strings.HasSuffix(text, "!") || strings.HasSuffix(text, "?")
}
