package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/klimeurt/heimdall/internal/config"
	"github.com/klimeurt/heimdall/internal/coordinator"
)

var Version = "dev"

func main() {
	log.Printf("Starting Heimdall Coordinator %s", Version)

	// Load configuration
	cfg, err := config.LoadCoordinatorConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create coordinator
	coord, err := coordinator.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create coordinator: %v", err)
	}
	defer coord.Close()

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

	// Start the coordinator
	if err := coord.Start(ctx); err != nil {
		log.Fatalf("Coordinator failed: %v", err)
	}

	log.Println("Coordinator stopped")
}
