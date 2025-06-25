package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/klimeurt/heimdall/internal/cloner"
	"github.com/klimeurt/heimdall/internal/config"
)

func main() {
	// Load configuration
	cfg, err := config.LoadClonerConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create cloner
	cloner, err := cloner.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create cloner: %v", err)
	}
	defer cloner.Close()

	// Create context that will be cancelled on interrupt signal
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start cloner in a goroutine
	go func() {
		log.Printf("Starting heimdall-cloner with %d concurrent workers", cfg.MaxConcurrentClones)
		if err := cloner.Start(ctx); err != nil {
			log.Printf("Cloner stopped with error: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-sigChan
	log.Println("Received shutdown signal, stopping cloner...")
	cancel()

	log.Println("heimdall-cloner stopped")
}
