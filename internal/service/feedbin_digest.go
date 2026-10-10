package service

import (
	"context"
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
