package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	minio "github.com/minio/minio-go/v7"
	"github.com/openlankapay/openlankapay/pkg/audit"
	"github.com/openlankapay/openlankapay/pkg/database"
	"github.com/openlankapay/openlankapay/pkg/messaging"
	"github.com/openlankapay/openlankapay/pkg/notification"
	"github.com/openlankapay/openlankapay/pkg/observability"
	pgadapter "github.com/openlankapay/openlankapay/services/merchant/internal/adapter/postgres"
	"github.com/openlankapay/openlankapay/services/merchant/internal/handler"
	"github.com/openlankapay/openlankapay/services/merchant/internal/service"
)

func main() {
	logger := observability.NewLogger("merchant", getEnv("LOG_LEVEL", "info"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Database
	dbURL := database.URLFromEnv("merchant")
	pool, err := database.NewPool(ctx, database.DefaultConfig(dbURL), logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	// JWT secret
	jwtSecret := getEnv("JWT_SECRET", "dev-jwt-secret-change-in-production-min32chars")

	// Repositories
	merchantRepo := pgadapter.NewMerchantRepository(pool)
	apiKeyRepo := pgadapter.NewAPIKeyRepository(pool)
	userRepo := pgadapter.NewUserRepository(pool)
	branchRepo := pgadapter.NewBranchRepository(pool)
	paymentLinkRepo := pgadapter.NewPaymentLinkRepository(pool)
	directorRepo := pgadapter.NewDirectorRepository(pool)

	// Event publisher (noop for now)
	eventPub := &noopPublisher{}

	// Notification client — prefer NATS, fallback to HTTP
	var notifier notification.Notifier
	natsURL := getEnv("NATS_URL", "nats://localhost:4222")
	if natsClient, natsErr := messaging.NewClient(natsURL); natsErr == nil {
		if nc, err := notification.NewNATSClient(natsClient); err == nil {
			notifier = nc
			logger.Info().Msg("using NATS for notifications")
		}
	}
	if notifier == nil {
		notifServiceURL := getEnv("NOTIFICATION_SERVICE_URL", "http://localhost:8087")
		notifier = notification.NewClient(notifServiceURL)
		logger.Info().Msg("using HTTP for notifications (NATS unavailable)")
	}

	// Service
	adminEmail := getEnv("ADMIN_NOTIFICATION_EMAIL", "admin@openlankapay.lk")
	svc := service.NewMerchantService(merchantRepo, apiKeyRepo, userRepo, eventPub, jwtSecret, notifier, adminEmail, directorRepo)

	// Document repository
	docRepo := pgadapter.NewDocumentRepository(pool)

	// File upload handler (MinIO)
	minioEndpoint := getEnv("MINIO_ENDPOINT", "localhost:9000")
	minioAccessKey := getEnv("MINIO_ACCESS_KEY", "minioadmin")
	minioSecretKey := getEnv("MINIO_SECRET_KEY", "minioadmin123")
	minioBucket := getEnv("MINIO_BUCKET", "kyc-documents")
	var uploadHandler *handler.FileUploadHandler
	uploadHandler, err = handler.NewFileUploadHandler(handler.FileUploadConfig{
		Endpoint:  minioEndpoint,
		AccessKey: minioAccessKey,
		SecretKey: minioSecretKey,
		Bucket:    minioBucket,
		UseSSL:    false,
	}, docRepo)
	if err != nil {
		logger.Warn().Err(err).Msg("MinIO not available, file uploads disabled")
		uploadHandler = nil
	} else {
		logger.Info().Msg("file upload handler initialized with MinIO")
	}

	// Audit log client
	adminServiceURL := getEnv("ADMIN_SERVICE_URL", "http://localhost:8088")
	auditClient := audit.NewClient(adminServiceURL)

	// Extract MinIO client from upload handler for director document uploads
	var minioClient *minio.Client
	var minioBucketName string
	if uploadHandler != nil {
		minioClient = uploadHandler.MinioClient()
		minioBucketName = uploadHandler.BucketName()
	}

	// HTTP Handler
	h := handler.NewMerchantHandler(svc, jwtSecret, auditClient, docRepo, minioClient, minioBucketName)
	router := handler.NewRouter(h, branchRepo, paymentLinkRepo, uploadHandler)

	// Internal routes (service-to-service, no auth)
	internalHandler := handler.NewInternalHandler(apiKeyRepo)
	handler.RegisterInternalRoutes(router.(*chi.Mux), internalHandler)

	// Server
	port := getEnv("PORT", "8082")
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
		logger.Info().Msg("shutting down...")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info().Str("port", port).Msg("merchant service started")
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
