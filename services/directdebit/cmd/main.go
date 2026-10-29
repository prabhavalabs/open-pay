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
	pgadapter "github.com/openlankapay/openlankapay/services/directdebit/internal/adapter/postgres"
	"github.com/openlankapay/openlankapay/services/directdebit/internal/handler"
	"github.com/openlankapay/openlankapay/services/directdebit/internal/service"
)

func main() {
	logger := observability.NewLogger("directdebit", getEnv("LOG_LEVEL", "info"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbURL := database.URLFromEnv("directdebit")
	pool, err := database.NewPool(ctx, database.DefaultConfig(dbURL), logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	jwtSecret := getEnv("JWT_SECRET", "dev-jwt-secret-change-in-production-min32chars")

	scenarioRepo := pgadapter.NewScenarioRepository(pool)
	contractRepo := pgadapter.NewContractRepository(pool)
	paymentRepo := pgadapter.NewPaymentRepository(pool)

	svc := service.NewDirectDebitService(scenarioRepo, contractRepo, paymentRepo)
	h := handler.NewDirectDebitHandler(svc)
	router := handler.NewRouter(h, jwtSecret)

	port := getEnv("PORT", "8089")
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info().Msg("shutting down direct debit service...")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info().Str("port", port).Msg("direct debit service started")
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
