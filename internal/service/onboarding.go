package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/secure"
)

type SlackDirectMessageClient interface {
	OpenDirectMessage(ctx context.Context, token, slackUserID string) (string, error)
	PostMessage(ctx context.Context, token, channel, text string) error
}

type OnboardingService struct {
	queue         repository.NotificationRepository
	invitations   []ConnectionInvitation
	installations repository.InstallationRepository
	client        SlackDirectMessageClient
	publicURL     string
	links         secure.LinkSigner
}

// Register configured integrations before starting HTTP handlers and workers.
type ConnectionInvitation struct {
	Purpose, Name, Description string
	Enabled                    func(string) bool
	Connected                  func(context.Context, int64) (bool, error)
}

func (s *OnboardingService) WithQueue(q repository.NotificationRepository) *OnboardingService {
	s.queue = q
	return s
}
func (s *OnboardingService) Register(inv ConnectionInvitation) {
	s.invitations = append(s.invitations, inv)
}

func NewOnboardingService(installations repository.InstallationRepository, client SlackDirectMessageClient, publicURL string, links secure.LinkSigner) *OnboardingService {
	return &OnboardingService{installations: installations, client: client, publicURL: publicURL, links: links}
}

func (s *OnboardingService) Send(ctx context.Context, userID int64) error {
	return s.enqueueUser(ctx, userID, "v1")
}

// Explicit admin resend replaces expired links; automatic discovery deduplicates.
func (s *OnboardingService) Resend(ctx context.Context, userID int64) error {
	return s.enqueueUser(ctx, userID, fmt.Sprintf("manual/%d", time.Now().UnixNano()))
}

func (s *OnboardingService) enqueueUser(ctx context.Context, userID int64, period string) error {
	if userID <= 0 || s.publicURL == "" || s.queue == nil {
		return errors.New("user ID and public URL are required")
	}
	installation, err := s.installations.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}
	var failures []error
	for _, inv := range s.invitations {
		if inv.Enabled != nil && !inv.Enabled(installation.TeamID) {
			continue
		}
		if err := s.queue.Enqueue(ctx, userID, "connection_invitation", inv.Purpose+"/"+period, repository.NotificationMessage{Connection: inv.Purpose}); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// Each integration/user has a stable delivery key across reinstalls and retries.
// Workspace discovery is daily; already sent invitations are never resent daily.
func (s *OnboardingService) EnqueueWorkspace(ctx context.Context, userID int64) error {
	if s.queue == nil {
		return errors.New("onboarding queue missing")
	}
	installation, err := s.installations.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}
	catalog := ""
	for _, inv := range s.invitations {
		if inv.Enabled == nil || inv.Enabled(installation.TeamID) {
			catalog += "/" + inv.Purpose
		}
	}
	if catalog == "" {
		return nil
	}
	key := time.Now().UTC().Format("2006-01-02") + catalog
	return s.queue.Enqueue(ctx, userID, "workspace_onboarding", key, repository.NotificationMessage{WorkspaceOnboarding: true})
}

func (s *OnboardingService) DiscoverWorkspace(ctx context.Context, userID int64) error {
	installation, err := s.installations.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}
	// Always schedule the installer even if users.list fails temporarily.
	if err := s.Send(ctx, userID); err != nil {
		return err
	}
	client, ok := s.client.(interface {
		ListUsers(context.Context, string) ([]domain.SlackMember, error)
	})
	if !ok {
		return errors.New("workspace member discovery unavailable")
	}
	members, err := client.ListUsers(ctx, installation.BotToken)
	if err != nil {
		return err
	}
	var failures []error
	for _, member := range members {
		if member.ID == "" || member.ID == "USLACKBOT" || member.Deleted || member.IsBot {
			continue
		}
		id, err := s.installations.UpsertUser(ctx, installation.TeamID, member.ID)
		if err == nil {
			err = s.Send(ctx, id)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *OnboardingService) DeliverInvitation(ctx context.Context, userID int64, purpose string) error {
	installation, err := s.installations.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}
	for _, inv := range s.invitations {
		if inv.Purpose != purpose {
			continue
		}
		if inv.Enabled != nil && !inv.Enabled(installation.TeamID) {
			return nil
		}
		connected, err := inv.Connected(ctx, userID)
		if err != nil {
			return err
		}
		if connected {
			return nil
		}
		channel, err := s.client.OpenDirectMessage(ctx, installation.BotToken, installation.SlackUserID)
		if err != nil {
			return err
		}
		// Generate fresh links when sending, so queue delays cannot expire them.
		link, err := s.links.URL(s.publicURL, userID, purpose, time.Now())
		if err != nil {
			return err
		}
		text := fmt.Sprintf("👋 *Nudgy · %s bağlantısı*\n%s\n\n<%s|%s hesabını bağla>\n\nBu bağlantı sana özeldir. Yalnızca kullanmak istediğin servisleri bağlayabilirsin.", inv.Name, inv.Description, link, inv.Name)
		if purpose == "github" {
			text += "\nLink 20 dakika geçerlidir. Süresi dolarsa Nudgy’den yeni GitHub bağlantısı isteyebilirsin."
		}
		return s.client.PostMessage(ctx, installation.BotToken, channel, text)
	}
	return nil // Integration disabled since invitation was queued.
}
