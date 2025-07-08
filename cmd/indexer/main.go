package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/klimeurt/heimdall/internal/config"
	"github.com/klimeurt/heimdall/internal/indexer"
	"github.com/klimeurt/heimdall/internal/logging"
)

// Version is set at build time
var Version = "dev"

func main() {
	// Initialize logger
	logConfig := logging.DefaultConfig("indexer")
	logConfig.Version = Version
	logger := logging.NewLogger(logConfig)
	slog.SetDefault(logger)

	logger.Info("starting heimdall-indexer", slog.String("version", Version))

	// Load configuration
	cfg, err := config.LoadIndexerConfig()
	if err != nil {
		logger.Error("failed to load configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Create indexer instance
	indexerService, err := indexer.New(cfg, logger)
	if err != nil {
		logger.Error("failed to create indexer", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer indexerService.Close()

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start indexer in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- indexerService.Start(ctx)
	}()

	// Wait for shutdown signal or error
	select {
	case <-sigChan:
		logger.Info("received shutdown signal")
		cancel()
	case err := <-errChan:
		if err != nil {
			logger.Error("indexer error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}

	logger.Info("heimdall-indexer shutdown complete")
}
