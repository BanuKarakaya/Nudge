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
	refreshConnection := func(c *domain.RaindropConnection) error {
		if c.ExpiresAt == nil || time.Now().Add(time.Minute).Before(*c.ExpiresAt) {
			return nil
		}
		refresh, ok := s.client.(interface {
			Refresh(context.Context, string) (raindrop.TokenResponse, error)
		})
		if !ok || c.RefreshToken == "" {
			return fmt.Errorf("Raindrop yeniden bağlanmalı")
		}
		tokens, err := refresh.Refresh(ctx, c.RefreshToken)
		if err != nil {
			return err
		}
		c.AccessToken = tokens.AccessToken
		c.RefreshToken = tokens.RefreshToken
		c.ExpiresAt = raindrop.ExpiresAt(tokens.ExpiresIn)
		return nil
	}
	if connection.ExpiresAt != nil && time.Now().Add(time.Minute).After(*connection.ExpiresAt) {
		if locked, ok := s.connections.(interface {
			RefreshConnection(context.Context, int64, func(*domain.RaindropConnection) error) (domain.RaindropConnection, error)
		}); ok {
			connection, err = locked.RefreshConnection(ctx, userID, refreshConnection)
		} else {
			err = refreshConnection(&connection)
			if err == nil {
				err = s.connections.Save(ctx, connection)
			}
		}
		if err != nil {
			return 0, err
		}
	}

	synced := 0
	for page := 0; page < 10000; page++ {
		items, err := s.client.ListRaindrops(ctx, connection.AccessToken, page, 50)
		if err != nil {
			return synced, err
		}
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
		if len(items) < 50 {
			return synced, nil
		}
	}
	return synced, fmt.Errorf("Raindrop pagination limit reached")
}
