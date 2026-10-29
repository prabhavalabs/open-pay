package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openlankapay/openlankapay/pkg/database"
	"github.com/openlankapay/openlankapay/pkg/observability"
	pgadapter "github.com/openlankapay/openlankapay/services/webhook/internal/adapter/postgres"
	"github.com/openlankapay/openlankapay/services/webhook/internal/handler"
	"github.com/openlankapay/openlankapay/services/webhook/internal/service"
)

func main() {
	logger := observability.NewLogger("webhook", getEnv("LOG_LEVEL", "info"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Database
	dbURL := database.URLFromEnv("webhook")
	pool, err := database.NewPool(ctx, database.DefaultConfig(dbURL), logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	// JWT secret
	jwtSecret := getEnv("JWT_SECRET", "dev-jwt-secret-change-in-production-min32chars")

	// Repositories
	configRepo := pgadapter.NewConfigRepository(pool)
	deliveryRepo := pgadapter.NewDeliveryRepository(pool)

	// Service
	svc := service.NewWebhookService(configRepo, deliveryRepo)

	// HTTP Handler
	h := handler.NewWebhookHandler(svc)
	router := handler.NewRouter(h, jwtSecret)

	// Server
	port := getEnv("PORT", "8084")
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Background retry worker — polls for failed deliveries and retries them
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		logger.Info().Msg("webhook retry worker started (30s interval)")

		for {
			select {
			case <-ctx.Done():
				logger.Info().Msg("webhook retry worker stopped")
				return
			case <-ticker.C:
				retried, err := svc.RetryPending(ctx)
				if err != nil {
					logger.Error().Err(err).Msg("retry worker error")
				} else if retried > 0 {
					logger.Info().Int("count", retried).Msg("retried webhook deliveries")
				}
			}
		}
	}()

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info().Msg("shutting down webhook service...")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info().Str("port", port).Msg("webhook service started")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal().Err(err).Msg("server error")
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
