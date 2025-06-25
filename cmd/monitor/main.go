package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

// MonitorConfig holds the monitor configuration
type MonitorConfig struct {
	RedisHost     string
	RedisPort     string
	RedisPassword string
	RedisDB       int
	RefreshRate   time.Duration
}

// QueueStats holds statistics for a Redis queue
type QueueStats struct {
	Name   string
	Length int64
}

func main() {
	cfg := loadConfig()

	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort),
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer redisClient.Close()

	// Test Redis connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start monitoring
	ticker := time.NewTicker(cfg.RefreshRate)
	defer ticker.Stop()

	fmt.Println("Redis Queue Monitor")
	fmt.Println("==================")
	fmt.Printf("Connected to Redis at %s:%s\n", cfg.RedisHost, cfg.RedisPort)
	fmt.Printf("Refresh rate: %v\n", cfg.RefreshRate)
	fmt.Println("Press Ctrl+C to exit")
	fmt.Println()

	// Monitor queues
	for {
		select {
		case <-sigChan:
			fmt.Println("\nShutting down monitor...")
			return
		case <-ticker.C:
			displayQueueStats(ctx, redisClient)
		}
	}
}

func loadConfig() *MonitorConfig {
	cfg := &MonitorConfig{
		RedisHost:     getEnv("REDIS_HOST", "localhost"),
		RedisPort:     getEnv("REDIS_PORT", "6379"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		RefreshRate:   5 * time.Second,
	}

	// Parse Redis DB
	if redisDB := os.Getenv("REDIS_DB"); redisDB != "" {
		db, err := strconv.Atoi(redisDB)
		if err != nil {
			log.Fatalf("Invalid REDIS_DB value: %v", err)
		}
		cfg.RedisDB = db
	}

	// Parse refresh rate
	if refreshRate := os.Getenv("REFRESH_RATE_SECONDS"); refreshRate != "" {
		rate, err := strconv.Atoi(refreshRate)
		if err != nil {
			log.Fatalf("Invalid REFRESH_RATE_SECONDS value: %v", err)
		}
		cfg.RefreshRate = time.Duration(rate) * time.Second
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func displayQueueStats(ctx context.Context, client *redis.Client) {
	// Clear screen
	fmt.Print("\033[H\033[2J")

	// Header
	fmt.Println("Redis Queue Monitor")
	fmt.Println("==================")
	fmt.Printf("Last updated: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println()

	// Define the queues to monitor
	queues := []string{"clone_queue", "processed_queue", "secrets_queue"}

	var stats []QueueStats
	var activeQueues int

	// Get stats for each queue
	for _, queueName := range queues {
		length, err := client.LLen(ctx, queueName).Result()
		if err != nil {
			log.Printf("Error getting length for queue %s: %v", queueName, err)
			continue
		}

		stats = append(stats, QueueStats{
			Name:   queueName,
			Length: length,
		})

		if length > 0 {
			activeQueues++
		}
	}

	// Display summary
	fmt.Printf("Active Queues: %d/%d\n", activeQueues, len(queues))
	fmt.Println()

	// Display queue details
	fmt.Printf("%-20s %s\n", "Queue Name", "Items")
	fmt.Println("----------------------------------------")

	for _, stat := range stats {
		status := "empty"
		if stat.Length > 0 {
			status = "active"
		}

		fmt.Printf("%-20s %d (%s)\n", stat.Name, stat.Length, status)
	}

	fmt.Println()
	fmt.Printf("Total items across all queues: %d\n", getTotalItems(stats))

	// Display additional Redis info
	displayRedisInfo(ctx, client)
}

func getTotalItems(stats []QueueStats) int64 {
	var total int64
	for _, stat := range stats {
		total += stat.Length
	}
	return total
}

func displayRedisInfo(ctx context.Context, client *redis.Client) {
	// Get Redis memory usage
	info, err := client.Info(ctx, "memory").Result()
	if err != nil {
		return
	}

	fmt.Println()
	fmt.Println("Redis Info:")
	fmt.Println("-----------")

	// Parse memory info (simplified)
	lines := parseInfoOutput(info)
	for key, value := range lines {
		if key == "used_memory_human" {
			fmt.Printf("Memory Usage: %s\n", value)
		}
	}
}

func parseInfoOutput(info string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(info, "\n")

	for _, line := range lines {
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				result[key] = value
			}
		}
	}

	return result
}
