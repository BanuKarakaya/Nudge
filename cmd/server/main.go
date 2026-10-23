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

	links, err := secure.NewLinkSigner(os.Getenv("NUDGE_LINK_SIGNING_KEY"))
	if err != nil {
		log.Fatalf("account link signing is not configured: %v", err)
	}
	mux := http.NewServeMux()
	privateMux := http.NewServeMux()
	mux.Handle("/api/v1/", handler.RequireAPIToken(os.Getenv("NUDGE_API_TOKEN"), privateMux))
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
	var textSummarizer service.TextSummarizer
	var rssRecommender service.RSSRecommender
	var dialogue service.ConversationAssistant
	var webAssistant service.WebAssistant
	if apiKey := os.Getenv("OPENROUTER_API_KEY"); apiKey != "" {
		model := os.Getenv("OPENROUTER_MODEL")
		if model == "" {
			model = "openrouter/free"
		}
		baseURL := os.Getenv("OPENROUTER_BASE_URL")
		if baseURL == "" {
			baseURL = "https://openrouter.ai/api/v1"
		}
		complexModel := os.Getenv("OPENROUTER_COMPLEX_MODEL")
		if complexModel == "" {
			complexModel = "deepseek/deepseek-v4.1-flash"
		}
		log.Printf("OpenRouter AI enabled (model=%s, complex_model=%s, base_url=%s)", model, complexModel, baseURL)
		openRouterClient := ai.OpenAICompatibleClient{
			APIKey:     apiKey,
			Model:      model,
			BaseURL:    baseURL,
			HTTPClient: &http.Client{Timeout: 90 * time.Second},
		}
		complexClient := ai.OpenAICompatibleClient{
			APIKey:     apiKey,
			Model:      complexModel,
			BaseURL:    baseURL,
			HTTPClient: &http.Client{Timeout: 90 * time.Second},
		}
		modelRouter := ai.OpenRouterModelRouter{Free: openRouterClient, DeepSeek: complexClient}
		textSummarizer = modelRouter
		rssRecommender = modelRouter
		dialogue = modelRouter
	} else if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		model := os.Getenv("OPENAI_MODEL")
		if model == "" {
			model = "gpt-6-luna"
		}
		baseURL := os.Getenv("OPENAI_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		log.Printf("OpenAI AI enabled (model=%s, base_url=%s)", model, baseURL)
		openAIClient := ai.OpenAICompatibleClient{
			APIKey:     apiKey,
			Model:      model,
			BaseURL:    baseURL,
			HTTPClient: &http.Client{Timeout: 90 * time.Second},
		}
		textSummarizer = openAIClient
		rssRecommender = openAIClient
		dialogue = openAIClient
	} else if apiKey := os.Getenv("OPENCODE_API_KEY"); apiKey != "" {
		model := os.Getenv("OPENCODE_MODEL")
		if model == "" {
			model = "deepseek-v4.1-flash-free"
		}
		baseURL := os.Getenv("OPENCODE_BASE_URL")
		if baseURL == "" {
			baseURL = "https://opencode.ai/inference/v1"
		}
		log.Printf("OpenCode AI enabled (model=%s, base_url=%s)", model, baseURL)
		openCodeClient := ai.OpenAICompatibleClient{
			APIKey:     apiKey,
			Model:      model,
			BaseURL:    baseURL,
			HTTPClient: &http.Client{Timeout: 90 * time.Second},
		}
		textSummarizer = openCodeClient
		rssRecommender = openCodeClient
		dialogue = openCodeClient
	} else if apiKey := os.Getenv("DEEPSEEK_API_KEY"); apiKey != "" {
		// Optional fallback for direct DeepSeek API usage.
		model := os.Getenv("DEEPSEEK_MODEL")
		if model == "" {
			model = "deepseek-flash"
		}
		log.Printf("Direct DeepSeek summaries enabled (model=%s)", model)
		deepSeekClient := ai.OpenAICompatibleClient{
			APIKey:     apiKey,
			Model:      model,
			HTTPClient: &http.Client{Timeout: 90 * time.Second},
		}
		textSummarizer = deepSeekClient
		rssRecommender = deepSeekClient
		dialogue = deepSeekClient
	} else {
		log.Printf("AI summaries disabled: OPENCODE_API_KEY is not configured")
	}

	// Google Search grounding remains a separate provider; OpenRouter models do
	// not provide Google's live Search grounding as a capability here.
	if apiKey := os.Getenv("GEMINI_API_KEY"); apiKey != "" {
		log.Printf("Google web search enabled separately (model=%s)", os.Getenv("GEMINI_MODEL"))
		geminiClient := ai.GeminiClient{
			APIKey:     apiKey,
			Model:      os.Getenv("GEMINI_MODEL"),
			HTTPClient: &http.Client{Timeout: 60 * time.Second},
		}
		webAssistant = geminiClient
	} else {
		log.Printf("Google web search disabled: GEMINI_API_KEY is not configured")
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
	feedbinDigestHandler.RegisterRoutes(privateMux)
	onboardingService := service.NewOnboardingService(
		installationRepository,
		slackAPIClient,
		os.Getenv("NUDGE_PUBLIC_URL"),
		links,
	)
	onboardingHandler := handler.NewOnboardingHandler(onboardingService, os.Getenv("NUDGE_TEST_TOKEN"))
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
	dashboardHandler := handler.NewDashboardHandler(analyticsRepository, os.Getenv("NUDGE_DASHBOARD_TOKEN"))
	dashboardHandler.RegisterRoutes(mux)
	slackEventHandler := handler.NewSlackEventHandler(
		os.Getenv("SLACK_SIGNING_SECRET"),
		installationRepository,
		bookmarkService,
		feedbinDigestService,
		textSummarizer,
		rssRecommender,
		dialogue,
		webAssistant,
		conversationRepository,
		slackAPIClient,
		appTimezone,
		analyticsRepository,
	)
	slackEventHandler.RegisterRoutes(mux)

	notificationCtx, notificationCancel := context.WithTimeout(context.Background(), 15*time.Second)
	notificationRepository, err := postgres.NewNotificationRepository(notificationCtx, db)
	notificationCancel()
	if err != nil {
		log.Fatalf("initialize notification deliveries: %v", err)
	}
	worker := scheduler.DeliveryWorker{Notifications: notificationRepository, Installations: installationRepository, Client: slackAPIClient}
	go worker.Run(context.Background())
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
		go dailyReminder.Run(context.Background())
		feedbinReminder := scheduler.NewFeedbinReminder(
			reminderUserID,
			reminderTimezone,
			installationRepository,
			feedbinRepository,
			feedbinDigestService,
			textSummarizer,
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
	log.Printf("Nudgy server listening on %s", addr)
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
