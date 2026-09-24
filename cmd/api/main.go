package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/arpitkuriyal/business-drift/internal/auth"
	hubspotintegration "github.com/arpitkuriyal/business-drift/internal/integrations/hubspot"
	"github.com/arpitkuriyal/business-drift/internal/integrations/jobs"
	stripeintegration "github.com/arpitkuriyal/business-drift/internal/integrations/stripe"
	"github.com/arpitkuriyal/business-drift/internal/platform/config"
	"github.com/arpitkuriyal/business-drift/internal/platform/database"
	"github.com/arpitkuriyal/business-drift/internal/platform/encryption"
	"github.com/arpitkuriyal/business-drift/internal/platform/httpserver"
	"github.com/arpitkuriyal/business-drift/internal/platform/logging"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	logger, err := logging.New(cfg.Environment)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = logger.Sync() }()

	resources, err := database.Open(context.Background(), cfg)
	if err != nil {
		logger.Fatal("open application resources", zap.Error(err))
	}
	defer resources.Close()

	secretCipher, err := encryption.New(cfg.EncryptionKey)
	if err != nil {
		logger.Fatal("configure secret encryption", zap.Error(err))
	}
	stripeService := stripeintegration.NewService(resources.Postgres, secretCipher)
	hubSpotService := hubspotintegration.NewService(resources.Postgres, secretCipher, logger)

	shutdownSignal, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	workerCtx, stopWorker := context.WithCancel(shutdownSignal)
	workerDone := make(chan struct{})

	go func() {
		defer close(workerDone)
		jobs.Run(workerCtx, resources.Postgres, logger, func(ctx context.Context, tx pgx.Tx, job jobs.Work) error {
			switch job.Kind {
			case "stripe_event":
				return stripeService.ProcessWebhook(ctx, tx, job.OrganizationID, job.IntegrationID, job.Payload)
			case "stripe_sync":
				_, err := stripeService.Sync(ctx, auth.Identity{OrganizationID: job.OrganizationID})
				return err
			case "hubspot_sync":
				_, err := hubSpotService.Sync(ctx, auth.Identity{OrganizationID: job.OrganizationID})
				return err
			case "hubspot_event":
				_, err := hubSpotService.Sync(ctx, auth.Identity{OrganizationID: job.OrganizationID})
				return err
			default:
				return errors.New("unknown integration job")
			}
		})
	}()
	defer func() { stopWorker(); <-workerDone }()

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           httpserver.NewRouter(logger, resources, stripeService, hubSpotService),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverError := make(chan error, 1)
	go func() {
		logger.Info("HTTP server starting", zap.String("address", cfg.HTTPAddress))
		serverError <- server.ListenAndServe()
	}()

	select {
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("HTTP server failed", zap.Error(err))
		}
		return
	case <-shutdownSignal.Done():
		logger.Info("shutdown signal received")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("graceful shutdown failed", zap.Error(err))
		_ = server.Close()
	}

	logger.Info("HTTP server stopped")
}
