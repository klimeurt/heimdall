package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/klimeurt/heimdall/internal/config"
	"github.com/klimeurt/heimdall/internal/scanner"
)

func main() {
	log.Println("Starting heimdall-scanner...")

	// Load configuration
	cfg, err := config.LoadScannerConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create scanner instance
	scannerService, err := scanner.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create scanner: %v", err)
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
		log.Println("Received shutdown signal")
		cancel()
	case err := <-errChan:
		if err != nil {
			log.Fatalf("Scanner error: %v", err)
		}
	}

	log.Println("heimdall-scanner shutdown complete")
}
