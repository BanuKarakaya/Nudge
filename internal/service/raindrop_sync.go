package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nudge/internal/domain"
	"nudge/internal/raindrop"
	"nudge/internal/repository"
)

type RaindropClient interface {
	ListRaindrops(ctx context.Context, accessToken string, page, perPage int) ([]raindrop.RaindropItem, error)
}

type RaindropSyncService struct {
	connections repository.RaindropConnectionRepository
	bookmarks   repository.BookmarkRepository
	client      RaindropClient
}

func NewRaindropSyncService(
	connections repository.RaindropConnectionRepository,
	bookmarks repository.BookmarkRepository,
	client RaindropClient,
) *RaindropSyncService {
	return &RaindropSyncService{
		connections: connections,
		bookmarks:   bookmarks,
		client:      client,
	}
}

func (s *RaindropSyncService) SyncLatest(ctx context.Context, userID int64) (int, error) {
	connection, err := s.connections.GetByUserID(ctx, userID)
	if err != nil {
		return 0, err
	}
	items, err := s.client.ListRaindrops(ctx, connection.AccessToken, 0, 50)
	if err != nil {
		return 0, err
	}

	synced := 0
	for _, item := range items {
		created, err := time.Parse(time.RFC3339, item.Created)
		if err != nil {
			return synced, fmt.Errorf("parse Raindrop created time for %d: %w", item.ID, err)
		}
		_, err = s.bookmarks.Create(ctx, domain.Bookmark{
			UserID:             userID,
			RaindropBookmarkID: item.ID,
			Title:              strings.TrimSpace(item.Title),
			URL:                strings.TrimSpace(item.Link),
			SavedAt:            created,
		})
		if err != nil {
			return synced, err
		}
		synced++
	}
	return synced, nil
}
