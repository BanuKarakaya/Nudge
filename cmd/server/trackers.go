package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/handler"
	"nudge/internal/repository"
	"nudge/internal/repository/postgres"
	"nudge/internal/secure"
	"nudge/internal/service"
	"nudge/internal/slack"
	"nudge/internal/tracker"
)

func configureTrackers(db *pgxpool.Pool, public, admin *http.ServeMux, events *handler.SlackEventHandler, installations repository.InstallationRepository, client slack.APIClient, links secure.LinkSigner, onboarding *service.OnboardingService) error {
	if os.Getenv("NUDGY_JIRA_ENABLED") != "true" && os.Getenv("NUDGY_TRELLO_ENABLED") != "true" {
		return nil
	}
	base := strings.TrimRight(os.Getenv("NUDGE_PUBLIC_URL"), "/")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("Jira/Trello require an HTTPS NUDGE_PUBLIC_URL origin")
	}
	cipher, err := secure.NewCipherFromBase64(os.Getenv("NUDGY_TRACKER_ENCRYPTION_KEY"))
	if err != nil {
		return fmt.Errorf("NUDGY_TRACKER_ENCRYPTION_KEY must be a 32-byte base64 key")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := postgres.NewTrackerStore(ctx, db, cipher)
	if err != nil {
		return fmt.Errorf("tracker database migration failed")
	}
	for _, kind := range []string{"jira", "trello"} {
		prefix := "NUDGY_" + strings.ToUpper(kind)
		if os.Getenv(prefix+"_ENABLED") != "true" {
			continue
		}
		provider := tracker.Client{Provider: kind, ClientID: os.Getenv(prefix + "_CLIENT_ID"), ClientSecret: os.Getenv(prefix + "_CLIENT_SECRET"), RedirectURL: base + "/" + kind + "/oauth/callback"}
		if provider.Validate() != nil {
			return fmt.Errorf("%s OAuth configuration is incomplete", kind)
		}
		s := tracker.New(store, provider)
		h := handler.NewTrackerHandler(s, installations, client, links, base)
		h.RegisterRoutes(public, admin)
		events.WithTracker(h)
		onboarding.Register(service.ConnectionInvitation{Purpose: kind, Name: s.Name(), Description: s.Name() + " hesabını bağla; Nudgy’nin okuyabileceği proje veya panoları seç.", Connected: s.Connected})
	}
	return nil
}
