package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/klimeurt/heimdall/internal/cleaner"
	"github.com/klimeurt/heimdall/internal/config"
)

var Version = "dev"

func main() {
	log.SetOutput(os.Stdout)
	log.Printf("Starting Heimdall Cleaner %s", Version)

	// Load configuration
	cfg, err := config.LoadCleanerConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create cleaner
	cleanerService, err := cleaner.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create cleaner: %v", err)
	}
	defer cleanerService.Close()

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		log.Printf("Received signal: %v. Shutting down gracefully...", sig)
		cancel()
	}()

	// Start cleaner
	if err := cleanerService.Start(ctx); err != nil {
		log.Fatalf("Cleaner error: %v", err)
	}

	log.Println("Cleaner shutdown complete")
}