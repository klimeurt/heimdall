package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/klimeurt/heimdall/internal/config"
	"github.com/klimeurt/heimdall/internal/logging"
	"github.com/klimeurt/heimdall/internal/scanner-trufflehog"
)

func main() {
	// Initialize logger
	logConfig := logging.DefaultConfig("scanner-trufflehog")
	logger := logging.NewLogger(logConfig)
	slog.SetDefault(logger)

	logger.Info("starting heimdall-scanner-trufflehog")

	// Load configuration
	cfg, err := config.LoadScannerConfig()
	if err != nil {
		logger.Error("failed to load configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Create scanner instance
	scannerService, err := scanner.New(cfg, logger)
	if err != nil {
		logger.Error("failed to create scanner", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer scannerService.Close()

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start scanner in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- scannerService.Start(ctx)
	}()

	// Wait for shutdown signal or error
	select {
	case <-sigChan:
		logger.Info("received shutdown signal")
		cancel()
	case err := <-errChan:
		if err != nil {
			logger.Error("scanner error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}

	logger.Info("heimdall-scanner-trufflehog shutdown complete")
}
