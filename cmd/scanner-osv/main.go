package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/klimeurt/heimdall/internal/config"
	"github.com/klimeurt/heimdall/internal/logging"
	osvscanner "github.com/klimeurt/heimdall/internal/scanner-osv"
)

var Version = "dev"

func main() {
	// Initialize logger
	logConfig := logging.DefaultConfig("scanner-osv")
	logConfig.Version = Version
	logger := logging.NewLogger(logConfig)
	slog.SetDefault(logger)

	logger.Info("starting Heimdall Scanner-OSV", slog.String("version", Version))

	// Load configuration
	cfg, err := config.LoadOSVScannerConfig()
	if err != nil {
		logger.Error("failed to load configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Create scanner
	scanner, err := osvscanner.New(cfg, logger)
	if err != nil {
		logger.Error("failed to create scanner", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer scanner.Close()

	// Create context that can be cancelled
	ctx, cancel := context.WithCancel(context.Background())

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Info("received shutdown signal, gracefully stopping")
		cancel()
	}()

	// Start the scanner
	if err := scanner.Start(ctx); err != nil {
		logger.Error("scanner failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("Scanner-OSV stopped")
}
