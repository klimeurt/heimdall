package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/klimeurt/heimdall/internal/config"
	osvscanner "github.com/klimeurt/heimdall/internal/osv-scanner"
)

var Version = "dev"

func main() {
	log.Printf("Starting Heimdall OSV Scanner %s", Version)

	// Load configuration
	cfg, err := config.LoadOSVScannerConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create scanner
	scanner, err := osvscanner.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create scanner: %v", err)
	}
	defer scanner.Close()

	// Create context that can be cancelled
	ctx, cancel := context.WithCancel(context.Background())

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal, gracefully stopping...")
		cancel()
	}()

	// Start the scanner
	if err := scanner.Start(ctx); err != nil {
		log.Fatalf("Scanner failed: %v", err)
	}

	log.Println("OSV Scanner stopped")
}
