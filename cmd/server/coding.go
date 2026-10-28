package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/coding"
	"nudge/internal/handler"
	"nudge/internal/repository"
	"nudge/internal/repository/postgres"
	"nudge/internal/secure"
	"nudge/internal/service"
	"nudge/internal/slack"
)

func configureCoding(db *pgxpool.Pool, public, admin *http.ServeMux, events *handler.SlackEventHandler, interactions *handler.SlackInteractionHandler, installations repository.InstallationRepository, client slack.APIClient, links secure.LinkSigner, onboarding *service.OnboardingService) error {
	if os.Getenv("NUDGY_CODING_ENABLED") != "true" {
		return nil
	}
	appID, err := strconv.ParseInt(os.Getenv("NUDGY_GITHUB_APP_ID"), 10, 64)
	if err != nil {
		return errors.New("NUDGY_GITHUB_APP_ID must be a positive integer")
	}
	base := strings.TrimRight(os.Getenv("NUDGE_PUBLIC_URL"), "/")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("coding requires an HTTPS NUDGE_PUBLIC_URL origin")
	}
	appSlug := strings.TrimSpace(os.Getenv("NUDGY_GITHUB_APP_SLUG"))
	if appSlug == "" {
		appSlug = "nudgy-github"
	}
	provider := coding.GitHub{AppID: appID, AppSlug: appSlug, ClientID: os.Getenv("NUDGY_GITHUB_CLIENT_ID"), ClientSecret: os.Getenv("NUDGY_GITHUB_CLIENT_SECRET"), RedirectURL: base + "/github/oauth/callback"}
	if provider.Validate() != nil {
		return errors.New("GitHub App configuration is incomplete")
	}
	cipher, err := secure.NewCipherFromBase64(os.Getenv("NUDGY_CODING_ENCRYPTION_KEY"))
	if err != nil {
		return errors.New("NUDGY_CODING_ENCRYPTION_KEY must be a separate 32-byte base64 key")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := postgres.NewCodingStore(ctx, db, cipher)
	if err != nil {
		return errors.New("coding database migration failed")
	}
	s, err := coding.New(store, provider)
	if err != nil {
		return errors.New("invalid coding service configuration")
	}
	git, err := coding.NewLocalGit()
	if err != nil {
		return err
	}
	if err := s.EnableEditing(store, git); err != nil {
		return err
	}
	h := handler.NewCodingHandler(s, installations, client, links, base)
	h.RegisterRoutes(public, admin)
	events.WithCoding(h)
	interactions.WithCoding(h)
	onboarding.Register(service.ConnectionInvitation{Purpose: "github", Name: "GitHub", Description: "Nudgy’ye GitHub hesabını bağla; açılan GitHub ekranında hesabını ve bağlamak istediğin repoları seç.", Connected: s.Connected})
	return nil
}
