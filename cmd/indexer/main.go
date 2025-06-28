package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/klimeurt/heimdall/internal/config"
	"github.com/klimeurt/heimdall/internal/indexer"
)

// Version is set at build time
var Version = "dev"

func main() {
	log.Printf("Starting heimdall-indexer %s...", Version)

	// Load configuration
	cfg, err := config.LoadIndexerConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create indexer instance
	indexerService, err := indexer.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create indexer: %v", err)
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
		log.Println("Received shutdown signal")
		cancel()
	case err := <-errChan:
		if err != nil {
			log.Fatalf("Indexer error: %v", err)
		}
	}

	log.Println("heimdall-indexer shutdown complete")
}
