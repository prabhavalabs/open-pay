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
	pgadapter "github.com/openlankapay/openlankapay/services/settlement/internal/adapter/postgres"
	"github.com/openlankapay/openlankapay/services/settlement/internal/handler"
	"github.com/openlankapay/openlankapay/services/settlement/internal/service"
)

func main() {
	logger := observability.NewLogger("settlement", getEnv("LOG_LEVEL", "info"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Database
	dbURL := database.URLFromEnv("settlement")
	pool, err := database.NewPool(ctx, database.DefaultConfig(dbURL), logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	// JWT secret
	jwtSecret := getEnv("JWT_SECRET", "dev-jwt-secret-change-in-production-min32chars")

	// Repositories
	balanceRepo := pgadapter.NewBalanceRepository(pool)
	withdrawalRepo := pgadapter.NewWithdrawalRepository(pool)

	refundRepo := pgadapter.NewRefundRepository(pool)

	// Service
	svc := service.NewSettlementService(balanceRepo, withdrawalRepo)
	svc.SetRefundRepository(refundRepo)

	// HTTP Handler
	h := handler.NewSettlementHandler(svc)
	router := handler.NewRouter(h, jwtSecret)

	// Server
	port := getEnv("PORT", "8083")
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info().Msg("shutting down settlement service...")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info().Str("port", port).Msg("settlement service started")
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
