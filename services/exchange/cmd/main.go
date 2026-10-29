package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shopspring/decimal"

	"github.com/openlankapay/openlankapay/pkg/database"
	"github.com/openlankapay/openlankapay/pkg/observability"
	pgadapter "github.com/openlankapay/openlankapay/services/exchange/internal/adapter/postgres"
	"github.com/openlankapay/openlankapay/services/exchange/internal/fetcher"
	"github.com/openlankapay/openlankapay/services/exchange/internal/handler"
	"github.com/openlankapay/openlankapay/services/exchange/internal/service"
)

func main() {
	logger := observability.NewLogger("exchange", getEnv("LOG_LEVEL", "info"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Database
	dbURL := database.URLFromEnv("exchange")
	pool, err := database.NewPool(ctx, database.DefaultConfig(dbURL), logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	// Repository
	rateRepo := pgadapter.NewRateRepository(pool)

	// Event publisher (noop for now)
	eventPub := &noopPublisher{}

	// Service
	svc := service.NewExchangeService(rateRepo, eventPub)

	// Seed default USDT/LKR rate if none exists (fallback for when CoinGecko is unreachable)
	seedDefaultRate(ctx, svc)

	// Start background rate fetcher (fetches from CoinGecko every 5 minutes)
	fetchInterval := 5 * time.Minute
	rateFetcher := fetcher.NewCoinGeckoFetcher(svc, logger.With().Str("component", "rate-fetcher").Logger(), fetchInterval)
	go rateFetcher.Start(ctx)

	// HTTP Handler
	h := handler.NewExchangeHandler(svc)

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// Health check (used by the container HEALTHCHECK and the gateway)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	h.RegisterRoutes(r)

	// Server
	port := getEnv("PORT", "8085")
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info().Msg("shutting down exchange service...")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info().Str("port", port).Msg("exchange service started")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal().Err(err).Msg("server error")
	}
}

func seedDefaultRate(ctx context.Context, svc *service.ExchangeService) {
	_, err := svc.GetActiveRate(ctx, "USDT", "LKR")
	if err != nil {
		// No active rate — seed one as fallback
		rate := decimal.NewFromInt(325)
		if err := svc.UpdateRate(ctx, "USDT", "LKR", rate, "SEED"); err != nil {
			return
		}
	}
}

type noopPublisher struct{}

func (p *noopPublisher) Publish(_ context.Context, _ string, _ any) error {
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
