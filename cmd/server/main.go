package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"nudge/internal/codex"
	"nudge/internal/database"
	"nudge/internal/feedbin"
	"nudge/internal/handler"
	"nudge/internal/raindrop"
	"nudge/internal/repository/postgres"
	"nudge/internal/scheduler"
	"nudge/internal/secure"
	"nudge/internal/service"
	"nudge/internal/slack"
	"nudge/internal/workspace"
)

func main() {
	appCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := database.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	links, err := secure.NewLinkSigner(os.Getenv("NUDGE_LINK_SIGNING_KEY"))
	if err != nil {
		log.Fatalf("account link signing is not configured: %v", err)
	}
	mux := http.NewServeMux()
	privateMux := http.NewServeMux()
	// Legacy operator handlers remain unmounted. A global token plus a chosen
	// user ID must not grant access to customer data or actions.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /health/db", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	bookmarkRepository := postgres.NewBookmarkRepository(db)
	bookmarkService := service.NewBookmarkService(bookmarkRepository)
	bookmarkHandler := handler.NewBookmarkHandler(bookmarkService)
	bookmarkHandler.RegisterRoutes(privateMux)

	workspaceCtx, workspaceCancel := context.WithTimeout(appCtx, 15*time.Second)
	workspaceStore, err := postgres.NewWorkspaceStore(workspaceCtx, db)
	workspaceCancel()
	if err != nil {
		log.Fatalf("initialize workspace portal: %v", err)
	}
	installationRepository := postgres.NewInstallationRepository(db)
	workspacePortal := handler.NewWorkspaceHandler(workspaceStore, links, os.Getenv("NUDGE_PUBLIC_URL"), installationRepository, slack.APIClient{HTTPClient: &http.Client{Timeout: 15 * time.Second}})
	if err := workspacePortal.Validate(); err != nil {
		log.Fatal(err)
	}
	workspacePortal.RegisterRoutes(mux)
	slackHandler := handler.NewSlackHandler(slack.OAuthClient{
		ClientID:     os.Getenv("SLACK_CLIENT_ID"),
		ClientSecret: os.Getenv("SLACK_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("SLACK_REDIRECT_URL"),
	}, installationRepository)
	slackHandler.WithWorkspacePortal(workspacePortal)
	slackHandler.RegisterRoutes(mux)

	slackAPIClient := slack.APIClient{HTTPClient: &http.Client{Timeout: 15 * time.Second}}
	slackMessageService := service.NewSlackMessagingService(installationRepository, slackAPIClient)
	slackMessageHandler := handler.NewSlackMessageHandler(slackMessageService)
	slackMessageHandler.RegisterRoutes(privateMux)

	slackBookmarkService := service.NewSlackBookmarkMessagingService(
		installationRepository,
		bookmarkService,
		slackAPIClient,
	)
	slackBookmarkHandler := handler.NewSlackBookmarkHandler(slackBookmarkService)
	slackBookmarkHandler.RegisterRoutes(privateMux)

	slackInteractionHandler := handler.NewSlackInteractionHandler(
		os.Getenv("SLACK_SIGNING_SECRET"),
		bookmarkService,
		installationRepository,
	)
	slackInteractionHandler.RegisterRoutes(mux)

	raindropRepository := postgres.NewRaindropConnectionRepository(db)
	raindropOAuth := raindrop.OAuthClient{
		ClientID:     os.Getenv("RAINDROP_CLIENT_ID"),
		ClientSecret: os.Getenv("RAINDROP_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("RAINDROP_REDIRECT_URL"),
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
	}
	raindropService := service.NewRaindropService(raindropRepository, raindropOAuth)
	raindropHandler := handler.NewRaindropHandler(raindropOAuth, raindropService, links)
	raindropHandler.RegisterRoutes(mux)

	raindropSyncService := service.NewRaindropSyncService(
		raindropRepository,
		bookmarkRepository,
		raindropOAuth,
	)
	raindropSyncHandler := handler.NewRaindropSyncHandler(raindropSyncService)
	raindropSyncHandler.RegisterRoutes(privateMux)
	feedbinCipher, err := secure.NewCipherFromBase64(os.Getenv("FEEDBIN_ENCRYPTION_KEY"))
	if err != nil {
		log.Fatalf("Feedbin encryption is not configured: %v", err)
	}
	feedbinRepository := postgres.NewFeedbinConnectionRepository(db, feedbinCipher)
	feedbinService := service.NewFeedbinService(feedbinRepository, feedbin.Client{
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	})
	feedbinHandler := handler.NewFeedbinHandler(feedbinService, links)
	feedbinHandler.RegisterRoutes(mux)
	feedbinDigestService := service.NewFeedbinDigestService(feedbinRepository, feedbin.Client{
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	})
	stateDir := os.Getenv("NUDGY_CODEX_STATE_DIR")
	if stateDir == "" {
		stateDir = "/var/lib/nudgy/codex"
	}
	aiEncryptionKey := os.Getenv("NUDGY_AI_ENCRYPTION_KEY")
	if aiEncryptionKey == "" {
		aiEncryptionKey = os.Getenv("FEEDBIN_ENCRYPTION_KEY")
	}
	aiCipher, err := secure.NewCipherFromBase64(aiEncryptionKey)
	if err != nil {
		log.Fatal("workspace AI encryption is not configured")
	}
	aiStore, err := postgres.NewWorkspaceAIStore(appCtx, db, aiCipher)
	if err != nil {
		log.Fatal("workspace AI storage could not initialize")
	}
	workspacePortal.WithAI(aiStore, workspace.ProviderCatalog{})
	runtime, err := codex.NewTenantRuntime(aiStore, workspaceStore, os.Getenv("CODEX_BINARY"), stateDir)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("workspace-owned AI credentials enabled; global model and search credentials are not used")
	var webAssistant service.WebAssistant
	_, feedbinTimezone := reminderConfig()
	digestGenerator := service.NewAgentDigest(runtime, feedbinDigestService, feedbinTimezone)
	feedbinDigestHandler := handler.NewFeedbinDigestHandler(
		installationRepository,
		digestGenerator,
		slackAPIClient,
		feedbinTimezone,
		os.Getenv("NUDGE_TEST_TOKEN"),
	)
	feedbinDigestHandler.RegisterRoutes(privateMux)
	onboardingService := service.NewOnboardingService(
		installationRepository,
		slackAPIClient,
		os.Getenv("NUDGE_PUBLIC_URL"),
		links,
	)
	onboardingHandler := handler.NewOnboardingHandler(onboardingService, os.Getenv("NUDGE_TEST_TOKEN"))
	onboardingService.Register(service.ConnectionInvitation{Purpose: "feedbin", Name: "Feedbin", Description: "RSS aboneliklerini, okunmamış yazılarını ve özetlerini Nudgy ile takip etmek için bağlayabilirsin.", Connected: func(ctx context.Context, id int64) (bool, error) {
		_, err := feedbinRepository.GetByUserID(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	}})
	if raindropOAuth.ClientID != "" && raindropOAuth.ClientSecret != "" && raindropOAuth.RedirectURL != "" {
		onboardingService.Register(service.ConnectionInvitation{Purpose: "raindrop", Name: "Raindrop", Description: "Kaydettiğin bağlantıları ve okuma listeni Nudgy ile takip etmek için bağlayabilirsin.", Connected: func(ctx context.Context, id int64) (bool, error) {
			_, err := raindropRepository.GetByUserID(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil
			}
			return err == nil, err
		}})
	}
	onboardingService.WithWorkspacePortal()
	slackHandler.WithOnboarding(onboardingService)
	onboardingHandler.RegisterRoutes(privateMux)
	_, appTimezone := reminderConfig()
	conversationCtx, conversationCancel := context.WithTimeout(context.Background(), 15*time.Second)
	conversationRepository, err := postgres.NewConversationRepository(conversationCtx, db)
	conversationCancel()
	if err != nil {
		log.Fatalf("initialize conversation history: %v", err)
	}
	analyticsRepository, err := postgres.NewAnalyticsRepository(context.Background(), db)
	if err != nil {
		log.Fatalf("initialize analytics: %v", err)
	}
	branchRules, err := postgres.NewBranchRuleStore(context.Background(), db)
	if err != nil {
		log.Fatalf("initialize branch rules: %v", err)
	}
	// The customer portal owns /dashboard. Global analytics and branch-rule APIs
	// are deliberately not registered: an operator token cannot read tenant data.
	slackEventHandler := handler.NewSlackEventHandler(
		os.Getenv("SLACK_SIGNING_SECRET"),
		installationRepository,
		bookmarkService,
		feedbinDigestService,
		runtime,
		webAssistant,
		conversationRepository,
		slackAPIClient,
		appTimezone,
		analyticsRepository,
	)
	if err := configureCoding(db, mux, privateMux, slackEventHandler, slackInteractionHandler, installationRepository, slackAPIClient, links, onboardingService, branchRules, workspacePortal); err != nil {
		log.Fatalf("repository task configuration: %v", err)
	}
	if err := configureTrackers(db, mux, privateMux, slackEventHandler, installationRepository, slackAPIClient, links, onboardingService, workspacePortal); err != nil {
		log.Fatalf("Jira/Trello configuration: %v", err)
	}
	jobCtx, jobCancel := context.WithTimeout(appCtx, 15*time.Second)
	jobRepository, err := postgres.NewSlackJobRepository(jobCtx, db)
	jobCancel()
	if err != nil {
		log.Fatalf("initialize Slack jobs: %v", err)
	}
	slackEventHandler.WithWorkspacePortal(workspacePortal)
	slackEventHandler.WithJobQueue(jobRepository)
	slackEventHandler.RegisterRoutes(mux)

	notificationCtx, notificationCancel := context.WithTimeout(context.Background(), 15*time.Second)
	notificationRepository, err := postgres.NewNotificationRepository(notificationCtx, db)
	notificationCancel()
	if err != nil {
		log.Fatalf("initialize notification deliveries: %v", err)
	}
	onboardingService.WithQueue(notificationRepository)
	worker := scheduler.DeliveryWorker{Notifications: notificationRepository, Installations: installationRepository, Client: slackAPIClient, Onboarding: onboardingService}
	jobWorker := scheduler.SlackJobWorker{Jobs: jobRepository, Process: slackEventHandler.ProcessJob}
	jobsDone := make(chan struct{})
	go func() { defer close(jobsDone); jobWorker.Run(appCtx) }()
	go worker.Run(appCtx)
	go scheduler.RunOnboarding(appCtx, installationRepository, onboardingService)
	log.Print("connection invitations enabled after Slack installation and on startup; per-user delivery deduplication active")
	if reminderUserID, reminderTimezone := reminderConfig(); reminderUserID > 0 {
		dailyReminder := scheduler.NewDailyReminder(
			reminderUserID,
			os.Getenv("NUDGE_PUBLIC_URL"),
			reminderTimezone,
			installationRepository,
			raindropRepository,
			feedbinRepository,
			raindropSyncService,
			bookmarkService,
			links,
			notificationRepository,
			slackAPIClient,
		)
		dailyReminder.WithOnboarding(onboardingService)
		go dailyReminder.Run(appCtx)
		feedbinReminder := scheduler.NewFeedbinReminder(
			reminderUserID,
			reminderTimezone,
			installationRepository,
			feedbinRepository,
			service.NewFeedbinTitles(feedbinDigestService),
			notificationRepository,
			slackAPIClient,
		)
		go feedbinReminder.Run(appCtx)
		log.Printf("onboarding reconciliation at 13:45, Raindrop reminder at 22:00 (%s)", reminderTimezone)
		log.Printf("Feedbin title/link reminder enabled at 23:00 (%s), no AI generation", reminderTimezone)
	} else {
		log.Printf("daily reminder disabled: set NUDGE_REMINDER_USER_ID")
	}

	addr := ":" + port
	log.Printf("Nudgy server listening on %s", addr)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.ListenAndServe() }()
	select {
	case <-appCtx.Done():
	case err := <-serverDone:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server stopped: %v", err)
		}
		stop()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}
	select {
	case <-jobsDone:
	case <-shutdownCtx.Done():
		log.Print("Slack workers still stopping; unfinished jobs will recover after lease expiry")
	}
}

func reminderConfig() (int64, *time.Location) {
	userID, _ := strconv.ParseInt(os.Getenv("NUDGE_REMINDER_USER_ID"), 10, 64)
	timezoneName := os.Getenv("NUDGE_TIMEZONE")
	if timezoneName == "" {
		timezoneName = "Europe/Istanbul"
	}
	timezone, err := time.LoadLocation(timezoneName)
	if err != nil {
		log.Printf("invalid NUDGE_TIMEZONE %q, using UTC: %v", timezoneName, err)
		timezone = time.UTC
	}
	return userID, timezone
}
