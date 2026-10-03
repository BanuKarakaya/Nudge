package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"nudge/internal/database"
	"nudge/internal/handler"
	"nudge/internal/raindrop"
	"nudge/internal/repository/postgres"
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

	addr := ":" + port
	log.Printf("Nudge server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
