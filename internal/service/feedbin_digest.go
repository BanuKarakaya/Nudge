package service

import (
	"context"
	"fmt"
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

func (s *FeedbinDigestService) BuildDigest(ctx context.Context, userID int64, since time.Time) (string, error) {
	return s.BuildAnalysisInput(ctx, userID, since)
}

func (s *FeedbinDigestService) BuildAnalysisInput(ctx context.Context, userID int64, since time.Time) (string, error) {
	connection, err := s.connections.GetByUserID(ctx, userID)
	if err != nil {
		return "", err
	}
	entries, err := s.client.ListUnreadSince(ctx, connection.Email, connection.Password, since)
	if err != nil {
		return "", err
	}
	taggings, err := s.client.ListTaggings(ctx, connection.Email, connection.Password)
	if err != nil {
		return "", err
	}

	foldersByFeed := make(map[int64][]string)
	for _, tagging := range taggings {
		foldersByFeed[tagging.FeedID] = append(foldersByFeed[tagging.FeedID], tagging.Name)
	}
	groups := make(map[string][]feedbin.Entry)
	for _, entry := range entries {
		folders := foldersByFeed[entry.FeedID]
		if len(folders) == 0 {
			folders = []string{"Diğer"}
		}
		for _, folder := range folders {
			groups[folder] = append(groups[folder], entry)
		}
	}

	if len(groups) == 0 {
		return "📰 *Bugünkü RSS Özetin Beybi*\n\nDün 23.00’den beri okunmamış yeni RSS yok.", nil
	}
	folders := make([]string, 0, len(groups))
	for folder := range groups {
		folders = append(folders, folder)
	}
	sort.Strings(folders)
	var b strings.Builder
	b.WriteString("📰 *Bugünkü RSS Özetin Beybi*\n")
	for _, folder := range folders {
		items := groups[folder]
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
		fmt.Fprintf(&b, "\n*%s · %d okunmamış*\n", folder, len(items))
		for _, item := range items {
			fmt.Fprintf(&b, "• <%s|%s>\n", item.URL, item.Title)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
