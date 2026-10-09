package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"nudge/internal/ai"
	"nudge/internal/database"
	"nudge/internal/feedbin"
	"nudge/internal/handler"
	"nudge/internal/raindrop"
	"nudge/internal/repository/postgres"
	"nudge/internal/scheduler"
	"nudge/internal/secure"
	"nudge/internal/service"
	"nudge/internal/slack"
)

func main() {
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

	mux := http.NewServeMux()
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
	bookmarkHandler.RegisterRoutes(mux)

	installationRepository := postgres.NewInstallationRepository(db)
	slackHandler := handler.NewSlackHandler(slack.OAuthClient{
		ClientID:     os.Getenv("SLACK_CLIENT_ID"),
		ClientSecret: os.Getenv("SLACK_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("SLACK_REDIRECT_URL"),
	}, installationRepository)
	slackHandler.RegisterRoutes(mux)

	slackAPIClient := slack.APIClient{HTTPClient: &http.Client{Timeout: 15 * time.Second}}
	slackMessageService := service.NewSlackMessagingService(installationRepository, slackAPIClient)
	slackMessageHandler := handler.NewSlackMessageHandler(slackMessageService)
	slackMessageHandler.RegisterRoutes(mux)

	slackBookmarkService := service.NewSlackBookmarkMessagingService(
		installationRepository,
		bookmarkService,
		slackAPIClient,
	)
	slackBookmarkHandler := handler.NewSlackBookmarkHandler(slackBookmarkService)
	slackBookmarkHandler.RegisterRoutes(mux)

	slackInteractionHandler := handler.NewSlackInteractionHandler(
		os.Getenv("SLACK_SIGNING_SECRET"),
		bookmarkService,
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
	raindropHandler := handler.NewRaindropHandler(raindropOAuth, raindropService)
	raindropHandler.RegisterRoutes(mux)

	raindropSyncService := service.NewRaindropSyncService(
		raindropRepository,
		bookmarkRepository,
		raindropOAuth,
	)
	raindropSyncHandler := handler.NewRaindropSyncHandler(raindropSyncService)
	raindropSyncHandler.RegisterRoutes(mux)
	feedbinCipher, err := secure.NewCipherFromBase64(os.Getenv("FEEDBIN_ENCRYPTION_KEY"))
	if err != nil {
		log.Fatalf("Feedbin encryption is not configured: %v", err)
	}
	feedbinRepository := postgres.NewFeedbinConnectionRepository(db, feedbinCipher)
	feedbinService := service.NewFeedbinService(feedbinRepository, feedbin.Client{
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	})
	feedbinHandler := handler.NewFeedbinHandler(feedbinService)
	feedbinHandler.RegisterRoutes(mux)
	feedbinDigestService := service.NewFeedbinDigestService(feedbinRepository, feedbin.Client{
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	})
	var textSummarizer service.TextSummarizer
	var rssClassifier service.RSSIntentClassifier
	if apiKey := os.Getenv("GEMINI_API_KEY"); apiKey != "" {
		log.Printf("Gemini summaries enabled (model=%s)", os.Getenv("GEMINI_MODEL"))
		geminiClient := ai.GeminiClient{
			APIKey:     apiKey,
			Model:      os.Getenv("GEMINI_MODEL"),
			HTTPClient: &http.Client{Timeout: 60 * time.Second},
		}
		textSummarizer = geminiClient
		rssClassifier = geminiClient
	} else {
		log.Printf("Gemini summaries disabled: GEMINI_API_KEY is not configured")
	}
	_, feedbinTimezone := reminderConfig()
	feedbinDigestHandler := handler.NewFeedbinDigestHandler(
		installationRepository,
		feedbinDigestService,
		textSummarizer,
		slackAPIClient,
		feedbinTimezone,
		os.Getenv("NUDGE_TEST_TOKEN"),
	)
	feedbinDigestHandler.RegisterRoutes(mux)
	onboardingService := service.NewOnboardingService(
		installationRepository,
		slackAPIClient,
		os.Getenv("NUDGE_PUBLIC_URL"),
	)
	onboardingHandler := handler.NewOnboardingHandler(onboardingService, os.Getenv("NUDGE_TEST_TOKEN"))
	onboardingHandler.RegisterRoutes(mux)
	_, appTimezone := reminderConfig()
	slackEventHandler := handler.NewSlackEventHandler(
		os.Getenv("SLACK_SIGNING_SECRET"),
		installationRepository,
		bookmarkService,
		feedbinDigestService,
		textSummarizer,
		rssClassifier,
		slackAPIClient,
		appTimezone,
	)
	slackEventHandler.RegisterRoutes(mux)

	if reminderUserID, reminderTimezone := reminderConfig(); reminderUserID > 0 {
		notificationRepository := postgres.NewNotificationRepository(db)
		dailyReminder := scheduler.NewDailyReminder(
			reminderUserID,
			os.Getenv("NUDGE_PUBLIC_URL"),
			reminderTimezone,
			installationRepository,
			raindropRepository,
			feedbinRepository,
			raindropSyncService,
			bookmarkService,
			slackMessageService,
			slackBookmarkService,
			notificationRepository,
			slackAPIClient,
		)
		go dailyReminder.Run(context.Background())
		feedbinReminder := scheduler.NewFeedbinReminder(
			reminderUserID,
			reminderTimezone,
			installationRepository,
			feedbinRepository,
			feedbinDigestService,
			textSummarizer,
			slackMessageService,
			notificationRepository,
			slackAPIClient,
		)
		go feedbinReminder.Run(context.Background())
		log.Printf("onboarding enabled at 13:45, Raindrop reminder at 22:00 (%s)", reminderTimezone)
		log.Printf("Feedbin reminder enabled at 23:00 (%s)", reminderTimezone)
	} else {
		log.Printf("daily reminder disabled: set NUDGE_REMINDER_USER_ID")
	}

	addr := ":" + port
	log.Printf("Nudge server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
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
