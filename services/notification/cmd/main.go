package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"encoding/json"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/openlankapay/openlankapay/pkg/database"
	"github.com/openlankapay/openlankapay/pkg/messaging"
	"github.com/openlankapay/openlankapay/pkg/notification"
	"github.com/openlankapay/openlankapay/pkg/observability"
	pgadapter "github.com/openlankapay/openlankapay/services/notification/internal/adapter/postgres"
	smtpadapter "github.com/openlankapay/openlankapay/services/notification/internal/adapter/smtp"
	"github.com/openlankapay/openlankapay/services/notification/internal/domain"
	"github.com/openlankapay/openlankapay/services/notification/internal/handler"
	"github.com/openlankapay/openlankapay/services/notification/internal/service"
)

func main() {
	logger := observability.NewLogger("notification", getEnv("LOG_LEVEL", "info"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbURL := database.URLFromEnv("notification")
	pool, err := database.NewPool(ctx, database.DefaultConfig(dbURL), logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	jwtSecret := getEnv("JWT_SECRET", "dev-jwt-secret-change-in-production-min32chars")

	// SMTP sender
	smtpSender := smtpadapter.NewSender(smtpadapter.Config{
		Host:     getEnv("SMTP_HOST", "localhost"),
		Port:     getEnv("SMTP_PORT", "1025"),
		Username: getEnv("SMTP_USERNAME", ""),
		Password: getEnv("SMTP_PASSWORD", ""),
		From:     getEnv("SMTP_FROM", "noreply@openlankapay.dev"),
	})

	notifRepo := pgadapter.NewNotificationRepository(pool)
	templateRepo := pgadapter.NewEmailTemplateRepository(pool)
	svc := service.NewNotificationService(notifRepo, smtpSender)

	// Start NATS consumer for async notification processing
	natsURL := getEnv("NATS_URL", "nats://localhost:4222")
	natsClient, natsErr := messaging.NewClient(natsURL)
	if natsErr != nil {
		logger.Warn().Err(natsErr).Msg("NATS not available, async notifications disabled")
	} else {
		logger.Info().Msg("NATS consumer connected")
		if err := natsClient.EnsureStream(ctx, notification.StreamName, []string{"notifications.>"}); err != nil {
			logger.Warn().Err(err).Msg("failed to ensure NATS stream")
		} else {
			err := natsClient.Subscribe(ctx, notification.StreamName, "notification-worker", notification.EmailSubject, func(msg jetstream.Msg) {
				var input notification.SendEmailInput
				if err := json.Unmarshal(msg.Data(), &input); err != nil {
					logger.Error().Err(err).Msg("failed to unmarshal NATS notification")
					_ = msg.Nak()
					return
				}

				_, sendErr := svc.Send(context.Background(), domain.NotificationInput{
					MerchantID: input.MerchantID,
					Channel:    domain.ChannelEmail,
					Recipient:  input.Recipient,
					Subject:    input.Subject,
					Body:       input.Body,
					EventType:  input.EventType,
				})
				if sendErr != nil {
					logger.Error().Err(sendErr).Str("event", input.EventType).Msg("NATS notification failed")
					_ = msg.Nak()
					return
				}

				_ = msg.Ack()
				logger.Info().Str("event", input.EventType).Str("to", input.Recipient).Msg("NATS notification processed")
			})
			if err != nil {
				logger.Warn().Err(err).Msg("failed to subscribe to NATS notifications")
			}
		}
		defer natsClient.Close()
	}

	h := handler.NewNotificationHandler(svc, templateRepo)
	router := handler.NewRouter(h, jwtSecret)

	port := getEnv("PORT", "8087")
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
		logger.Info().Msg("shutting down notification service...")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info().Str("port", port).Msg("notification service started")
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
