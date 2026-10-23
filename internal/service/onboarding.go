package service

import (
	"context"
	"errors"
	"time"

	"nudge/internal/repository"
	"nudge/internal/secure"
)

type SlackDirectMessageClient interface {
	OpenDirectMessage(ctx context.Context, token, slackUserID string) (string, error)
	PostMessage(ctx context.Context, token, channel, text string) error
}

type OnboardingService struct {
	installations repository.InstallationRepository
	client        SlackDirectMessageClient
	publicURL     string
	links         secure.LinkSigner
}

func NewOnboardingService(installations repository.InstallationRepository, client SlackDirectMessageClient, publicURL string, links secure.LinkSigner) *OnboardingService {
	return &OnboardingService{installations: installations, client: client, publicURL: publicURL, links: links}
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
	raindropLink, err := s.links.URL(s.publicURL, userID, "raindrop", time.Now())
	if err != nil {
		return err
	}
	feedbinLink, err := s.links.URL(s.publicURL, userID, "feedbin", time.Now())
	if err != nil {
		return err
	}
	message := "Nudgy’yi kullanmak için hesaplarını bağla:\n\n" +
		"• Raindrop: " + raindropLink + "\n" +
		"• Feedbin: " + feedbinLink
	return s.client.PostMessage(ctx, installation.BotToken, dmChannel, message)
}
