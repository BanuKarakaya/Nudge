package service

import (
	"context"
	"errors"
	"fmt"

	"nudge/internal/repository"
)

type SlackDirectMessageClient interface {
	OpenDirectMessage(ctx context.Context, token, slackUserID string) (string, error)
	PostMessage(ctx context.Context, token, channel, text string) error
}

type OnboardingService struct {
	installations repository.InstallationRepository
	client        SlackDirectMessageClient
	publicURL     string
}

func NewOnboardingService(installations repository.InstallationRepository, client SlackDirectMessageClient, publicURL string) *OnboardingService {
	return &OnboardingService{installations: installations, client: client, publicURL: publicURL}
}

func (s *OnboardingService) Send(ctx context.Context, userID int64) error {
	if userID <= 0 || s.publicURL == "" {
		return errors.New("user ID and public URL are required")
	}
	installation, err := s.installations.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}
	dmChannel, err := s.client.OpenDirectMessage(ctx, installation.BotToken, installation.SlackUserID)
	if err != nil {
		return err
	}
	raindropLink := fmt.Sprintf("%s/raindrop/install?user_id=%d", s.publicURL, userID)
	feedbinLink := fmt.Sprintf("%s/feedbin/install?user_id=%d", s.publicURL, userID)
	message := "Nudge’yi kullanmak için hesaplarını bağla:\n\n" +
		"• Raindrop: " + raindropLink + "\n" +
		"• Feedbin: " + feedbinLink
	return s.client.PostMessage(ctx, installation.BotToken, dmChannel, message)
}
