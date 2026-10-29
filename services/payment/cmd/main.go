package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/openlankapay/openlankapay/pkg/audit"
	"github.com/openlankapay/openlankapay/pkg/database"
	"github.com/openlankapay/openlankapay/pkg/fraud"
	"github.com/openlankapay/openlankapay/pkg/observability"
	exchangeclient "github.com/openlankapay/openlankapay/services/payment/internal/adapter/exchange"
	merchantclient "github.com/openlankapay/openlankapay/services/payment/internal/adapter/merchant"
	pgadapter "github.com/openlankapay/openlankapay/services/payment/internal/adapter/postgres"
	"github.com/openlankapay/openlankapay/services/payment/internal/adapter/provider"
	settlementclient "github.com/openlankapay/openlankapay/services/payment/internal/adapter/settlement"
	"github.com/openlankapay/openlankapay/services/payment/internal/adapter/wallet"
	"github.com/openlankapay/openlankapay/services/payment/internal/domain"
	"github.com/openlankapay/openlankapay/services/payment/internal/handler"
	"github.com/openlankapay/openlankapay/services/payment/internal/service"
	"github.com/openlankapay/openlankapay/services/payment/internal/watcher"
)

func main() {
	logger := observability.NewLogger("payment", getEnv("LOG_LEVEL", "info"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Database
	dbURL := database.URLFromEnv("payment")
	pool, err := database.NewPool(ctx, database.DefaultConfig(dbURL), logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	// JWT secret
	jwtSecret := getEnv("JWT_SECRET", "dev-jwt-secret-change-in-production-min32chars")

	// Providers
	mockProv := provider.NewMockProvider()
	providers := map[string]domain.PaymentProvider{
		"TEST": mockProv,
	}
	logger.Info().Msg("registered provider: TEST")

	if apiKey := os.Getenv("BYBIT_API_KEY"); apiKey != "" {
		bp := provider.NewBybitProvider(provider.BybitConfig{
			BaseURL:   getEnv("BYBIT_BASE_URL", "https://api-testnet.bybit.com"),
			APIKey:    apiKey,
			APISecret: os.Getenv("BYBIT_API_SECRET"),
		})
		providers["BYBIT"] = bp
		logger.Info().Msg("registered provider: BYBIT")
	}

	if apiKey := os.Getenv("BINANCE_API_KEY"); apiKey != "" {
		bp := provider.NewBinanceProvider(provider.BinanceConfig{
			BaseURL:   getEnv("BINANCE_BASE_URL", "https://bpay.binanceapi.com"),
			APIKey:    apiKey,
			APISecret: os.Getenv("BINANCE_API_SECRET"),
		})
		providers["BINANCE"] = bp
		logger.Info().Msg("registered provider: BINANCE")
	}

	if apiKey := os.Getenv("KUCOIN_API_KEY"); apiKey != "" {
		kp := provider.NewKuCoinProvider(provider.KuCoinConfig{
			BaseURL:    getEnv("KUCOIN_BASE_URL", "https://api.kucoin.com"),
			APIKey:     apiKey,
			APISecret:  os.Getenv("KUCOIN_API_SECRET"),
			Passphrase: os.Getenv("KUCOIN_PASSPHRASE"),
		})
		providers["KUCOIN"] = kp
		logger.Info().Msg("registered provider: KUCOIN")
	}

	// Repository
	paymentRepo := pgadapter.NewPaymentRepository(pool)

	// Exchange client
	exchangeURL := getEnv("EXCHANGE_SERVICE_URL", "http://localhost:8085")
	exchClient := exchangeclient.NewClient(exchangeURL)

	// Event publisher (noop for now)
	eventPub := &noopPublisher{}

	// Merchant client (for incrementing payment link usage)
	merchantServiceURL := getEnv("MERCHANT_SERVICE_URL", "http://localhost:8082")
	merchClient := merchantclient.NewClient(merchantServiceURL)

	// Audit client
	adminServiceURL := getEnv("ADMIN_SERVICE_URL", "http://localhost:8088")
	auditClient := audit.NewClient(adminServiceURL)

	// Fraud engine
	fraudEngine := fraud.NewEngine()
	logger.Info().Msg("fraud detection engine initialized")

	// Service
	svc := service.NewPaymentService(paymentRepo, providers, exchClient, eventPub, merchClient)
	svc.SetFraudEngine(fraudEngine)

	// On-chain "scan-and-send" payments (BSC testnet). Enabled only when a
	// deposit master mnemonic is configured.
	if mnemonic := os.Getenv("DEPOSIT_MASTER_MNEMONIC"); mnemonic != "" {
		hw, werr := wallet.NewHDWallet(mnemonic)
		if werr != nil {
			logger.Fatal().Err(werr).Msg("invalid DEPOSIT_MASTER_MNEMONIC")
		}

		chainID := getEnvInt("CHAIN_ID", 97)
		usdcAddr := getEnv("USDC_ADDRESS", "0x6c904e10631e199134891158B97311ed088A6a16")
		usdtAddr := getEnv("USDT_ADDRESS", "0x5fB234eceBE3475E51F2A76bCE4a5d446D37CAB1")
		tokens := []provider.TokenConfig{
			{Symbol: "USDC", Address: usdcAddr, Decimals: 6},
			{Symbol: "USDT", Address: usdtAddr, Decimals: 6},
		}

		// Currency → token address resolver for the repo's pending-deposit query.
		paymentRepo.SetTokenResolver(func(currency string) string {
			switch currency {
			case "USDC":
				return usdcAddr
			case "USDT":
				return usdtAddr
			default:
				return ""
			}
		})

		store := watcher.NewStore()
		onchain := provider.NewOnchainProvider(hw, chainID, tokens, store, paymentRepo.NextDepositIndex)
		providers["ONCHAIN"] = onchain
		logger.Info().Msg("registered provider: ONCHAIN")

		// Settlement client for crediting merchants on confirmation.
		settlementURL := getEnv("SETTLEMENT_SERVICE_URL", "http://localhost:8083")
		svc.SetSettlementClient(settlementclient.NewClient(settlementURL))

		// Watcher goroutine.
		rpcURL := getEnv("RPC_URL", "https://bsc-testnet-rpc.publicnode.com")
		rpc := watcher.NewRPCClient(rpcURL)
		w := watcher.New(rpc, paymentRepo, store, svc.HandleProviderCallback, watcher.Config{
			Tokens:        []string{usdcAddr, usdtAddr},
			Confirmations: int64(getEnvInt("PAYMENT_CONFIRMATIONS", 3)),
			PollInterval:  getEnvDuration("WATCHER_POLL_INTERVAL", 15*time.Second),
		}, zerologAdapter{l: logger})
		go w.Run(ctx)
		logger.Info().Str("rpc", rpcURL).Int64("chainId", chainID).Msg("on-chain watcher started")
	}

	// HTTP Handler
	h := handler.NewPaymentHandler(svc, mockProv, auditClient)
	router := handler.NewRouter(h, jwtSecret)

	// Server
	port := getEnv("PORT", "8081")
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
		logger.Info().Msg("shutting down payment service...")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info().Str("port", port).Msg("payment service started")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal().Err(err).Msg("server error")
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

func getEnvInt(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

// zerologAdapter adapts a zerolog.Logger to the watcher.Logger interface.
type zerologAdapter struct{ l zerolog.Logger }

func (z zerologAdapter) kv(e *zerolog.Event, args []any) *zerolog.Event {
	for i := 0; i+1 < len(args); i += 2 {
		key, _ := args[i].(string)
		e = e.Interface(key, args[i+1])
	}
	return e
}
func (z zerologAdapter) Info(msg string, args ...any)  { z.kv(z.l.Info(), args).Msg(msg) }
func (z zerologAdapter) Warn(msg string, args ...any)  { z.kv(z.l.Warn(), args).Msg(msg) }
func (z zerologAdapter) Error(msg string, args ...any) { z.kv(z.l.Error(), args).Msg(msg) }
