package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	stripeintegration "github.com/arpitkuriyal/business-drift/internal/integrations/stripe"
	"github.com/arpitkuriyal/business-drift/internal/platform/config"
	"github.com/arpitkuriyal/business-drift/internal/platform/database"
	"github.com/arpitkuriyal/business-drift/internal/platform/encryption"
	"github.com/arpitkuriyal/business-drift/internal/platform/httpserver"
	"github.com/arpitkuriyal/business-drift/internal/platform/logging"
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
	stripeService := stripeintegration.NewService(resources.Postgres, resources.Redis, secretCipher, logger)

	shutdownSignal, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go stripeService.RunWorker(shutdownSignal)

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           httpserver.NewRouter(logger, resources, cfg.Environment, stripeService),
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
