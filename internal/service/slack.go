package service

import (
	"context"
	"errors"

	"nudge/internal/repository"
)

type SlackMessageClient interface {
	PostMessage(ctx context.Context, token, channel, text string) error
}

type SlackMessagingService struct {
	installations repository.InstallationRepository
	client        SlackMessageClient
}

func NewSlackMessagingService(installations repository.InstallationRepository, client SlackMessageClient) *SlackMessagingService {
	return &SlackMessagingService{installations: installations, client: client}
}

func (s *SlackMessagingService) SendMessage(ctx context.Context, userID int64, channel, text string) error {
	if userID <= 0 || channel == "" || text == "" {
		return errors.New("user ID, channel, and text are required")
	}

	installation, err := s.installations.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}
	return s.client.PostMessage(ctx, installation.BotToken, channel, text)
}
