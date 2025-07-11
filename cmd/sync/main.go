package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/klimeurt/heimdall/internal/config"
	"github.com/klimeurt/heimdall/internal/logging"
	"github.com/klimeurt/heimdall/internal/sync"
	"github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"
)

func main() {
	// Initialize logger
	logConfig := logging.DefaultConfig("sync")
	logger := logging.NewLogger(logConfig)
	slog.SetDefault(logger)

	cfg := config.SyncConfig{
		RedisURL:              getEnv("REDIS_URL", "redis://localhost:6379"),
		GitHubOrg:             os.Getenv("GITHUB_ORG"),
		GitHubToken:           os.Getenv("GITHUB_TOKEN"),
		FetchSchedule:         getEnv("FETCH_SCHEDULE", "0 0 * * 0"), // Weekly on Sunday
		FetchOnStartup:        getEnvBool("FETCH_ON_STARTUP", true),
		MaxConcurrentSyncs:    getEnvInt("MAX_CONCURRENT_SYNCS", 3),
		SyncTimeoutMinutes:    getEnvInt("SYNC_TIMEOUT_MINUTES", 30),
		SharedVolumePath:      getEnv("SHARED_VOLUME_PATH", "/shared/heimdall-repos"),
		TruffleHogQueueName:   getEnv("TRUFFLEHOG_QUEUE_NAME", "trufflehog_queue"),
		OSVQueueName:          getEnv("OSV_QUEUE_NAME", "osv_queue"),
		GitHubAPIDelayMs:      getEnvInt("GITHUB_API_DELAY_MS", 100),
		QueueBatchSize:        getEnvInt("QUEUE_BATCH_SIZE", 10),
		EnableScannerQueues:   getEnvBool("ENABLE_SCANNER_QUEUES", true),
		EnableTruffleHogQueue: getEnvBool("ENABLE_TRUFFLEHOG_QUEUE", true),
		EnableOSVQueue:        getEnvBool("ENABLE_OSV_QUEUE", true),
	}

	if cfg.GitHubOrg == "" {
		logger.Error("GITHUB_ORG environment variable is required")
		os.Exit(1)
	}

	logger.Info("starting Heimdall sync service",
		slog.String("org", cfg.GitHubOrg),
		slog.String("schedule", cfg.FetchSchedule),
		slog.Bool("fetch_on_startup", cfg.FetchOnStartup),
		slog.Int("max_concurrent_syncs", cfg.MaxConcurrentSyncs),
		slog.String("shared_volume", cfg.SharedVolumePath),
		slog.Bool("enable_scanner_queues", cfg.EnableScannerQueues),
		slog.Bool("enable_trufflehog_queue", cfg.EnableTruffleHogQueue),
		slog.Bool("enable_osv_queue", cfg.EnableOSVQueue))

	// Parse Redis URL
	opt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Error("failed to parse Redis URL", slog.String("error", err.Error()))
		os.Exit(1)
	}

	rdb := redis.NewClient(opt)
	defer rdb.Close()

	// Test Redis connection
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Error("failed to connect to Redis", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("successfully connected to Redis")

	// Create sync service
	svc := sync.NewService(cfg, rdb, logger)

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create a context that can be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start sync service
	go func() {
		if err := svc.Start(ctx); err != nil {
			logger.Error("sync service failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	// Fetch on startup if configured
	if cfg.FetchOnStartup {
		logger.Info("running initial sync on startup")
		go func() {
			if err := svc.SyncOrganization(ctx); err != nil {
				logger.Error("initial sync failed", slog.String("error", err.Error()))
			}
		}()
	}

	// Setup cron scheduler
	c := cron.New()
	if cfg.FetchSchedule != "" {
		_, err := c.AddFunc(cfg.FetchSchedule, func() {
			logger.Info("running scheduled sync")
			if err := svc.SyncOrganization(ctx); err != nil {
				logger.Error("scheduled sync failed", slog.String("error", err.Error()))
			}
		})
		if err != nil {
			logger.Error("failed to schedule sync", slog.String("error", err.Error()))
			os.Exit(1)
		}
		c.Start()
		defer c.Stop()
		logger.Info("scheduled sync", slog.String("cron_expression", cfg.FetchSchedule))
	}

	// Wait for shutdown signal
	<-sigChan
	logger.Info("shutting down sync service")
	cancel()
	time.Sleep(2 * time.Second) // Give workers time to finish
	logger.Info("sync service stopped")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		var intValue int
		if _, err := fmt.Sscanf(value, "%d", &intValue); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		value = strings.ToLower(value)
		return value == "true" || value == "1" || value == "yes"
	}
	return defaultValue
}