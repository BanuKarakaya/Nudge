package service

import (
	"context"
	"errors"

	"nudge/internal/domain"
	"nudge/internal/repository"
)

type SlackBookmarkMessageClient interface {
	PostBookmarkMessage(ctx context.Context, token, channel string, bookmark domain.Bookmark) error
}

type SlackBookmarkMessagingService struct {
	installations repository.InstallationRepository
	bookmarks     BookmarkService
	client        SlackBookmarkMessageClient
}

func NewSlackBookmarkMessagingService(
	installations repository.InstallationRepository,
	bookmarks BookmarkService,
	client SlackBookmarkMessageClient,
) *SlackBookmarkMessagingService {
	return &SlackBookmarkMessagingService{
		installations: installations,
		bookmarks:     bookmarks,
		client:        client,
	}
}

func (s *SlackBookmarkMessagingService) SendBookmark(
	ctx context.Context,
	userID, bookmarkID int64,
	channel string,
) error {
	if userID <= 0 || bookmarkID <= 0 || channel == "" {
		return errors.New("user ID, bookmark ID, and channel are required")
	}
	installation, err := s.installations.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}
	bookmark, err := s.bookmarks.GetByID(ctx, userID, bookmarkID)
	if err != nil {
		return err
	}
	return s.client.PostBookmarkMessage(ctx, installation.BotToken, channel, *bookmark)
}
