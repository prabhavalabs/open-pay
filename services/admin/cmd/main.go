package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openlankapay/openlankapay/pkg/database"
	"github.com/openlankapay/openlankapay/pkg/notification"
	"github.com/openlankapay/openlankapay/pkg/observability"
	pgadapter "github.com/openlankapay/openlankapay/services/admin/internal/adapter/postgres"
	"github.com/openlankapay/openlankapay/services/admin/internal/handler"
	"github.com/openlankapay/openlankapay/services/admin/internal/service"
)

func main() {
	logger := observability.NewLogger("admin", getEnv("LOG_LEVEL", "info"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbURL := database.URLFromEnv("admin")
	pool, err := database.NewPool(ctx, database.DefaultConfig(dbURL), logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	jwtSecret := getEnv("JWT_SECRET", "dev-jwt-secret-change-in-production-min32chars")

	auditRepo := pgadapter.NewAuditRepository(pool)
	adminUserRepo := pgadapter.NewAdminUserRepository(pool)
	legalDocRepo := pgadapter.NewLegalDocumentRepository(pool)
	settingsRepo := pgadapter.NewSettingsRepository(pool)

	auditSvc := service.NewAuditService(auditRepo)
	authSvc := service.NewAdminAuthService(adminUserRepo, jwtSecret)

	// Notification client
	notifServiceURL := getEnv("NOTIFICATION_SERVICE_URL", "http://localhost:8087")
	notifClient := notification.NewClient(notifServiceURL)

	h := handler.NewAdminHandler(auditSvc, authSvc, legalDocRepo, settingsRepo, adminUserRepo, notifClient)

	// Admin file upload handler (MinIO)
	var uploadHandler *handler.AdminUploadHandler
	minioEndpoint := getEnv("MINIO_ENDPOINT", "localhost:9000")
	uploadHandler, err = handler.NewAdminUploadHandler(handler.AdminUploadConfig{
		Endpoint:  minioEndpoint,
		AccessKey: getEnv("MINIO_ACCESS_KEY", "minioadmin"),
		SecretKey: getEnv("MINIO_SECRET_KEY", "minioadmin123"),
		Bucket:    getEnv("MINIO_BUCKET", "kyc-documents"),
		UseSSL:    false,
	})
	if err != nil {
		logger.Warn().Err(err).Msg("MinIO not available, admin uploads disabled")
		uploadHandler = nil
	}

	router := handler.NewRouter(h, jwtSecret, uploadHandler)

	port := getEnv("PORT", "8088")
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
		logger.Info().Msg("shutting down admin service...")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info().Str("port", port).Msg("admin service started")
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
