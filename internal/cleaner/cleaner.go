package cleaner

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/klimeurt/heimdall/internal/collector"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/redis/go-redis/v9"
)

// Cleaner handles the repository cleanup operations
type Cleaner struct {
	config      *config.CleanerConfig
	redisClient *redis.Client
}

// New creates a new Cleaner instance
func New(cfg *config.CleanerConfig) (*Cleaner, error) {
	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort),
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	// Test Redis connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &Cleaner{
		config:      cfg,
		redisClient: redisClient,
	}, nil
}

// Start begins the cleanup workers
func (c *Cleaner) Start(ctx context.Context) error {
	// Log queue length at startup
	queueLen, err := c.redisClient.LLen(ctx, c.config.CleanupQueueName).Result()
	if err != nil {
		log.Printf("Failed to get cleanup queue length at startup: %v", err)
	} else {
		log.Printf("Cleanup queue length at startup: %d items", queueLen)
	}

	log.Printf("Starting %d cleaner workers", c.config.MaxConcurrentJobs)

	var wg sync.WaitGroup

	// Start worker goroutines
	for i := 0; i < c.config.MaxConcurrentJobs; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			c.worker(ctx, workerID)
		}(i)
	}

	// Wait for all workers to complete
	wg.Wait()
	log.Println("All cleaner workers have stopped")
	return nil
}

// worker processes cleanup jobs from the Redis queue
func (c *Cleaner) worker(ctx context.Context, workerID int) {
	log.Printf("Cleaner Worker %d started", workerID)
	defer log.Printf("Cleaner Worker %d stopped", workerID)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Block until a job is available or context is cancelled
			result, err := c.redisClient.BRPop(ctx, 0, c.config.CleanupQueueName).Result()
			if err != nil {
				if err == redis.Nil {
					continue // No items in queue, keep waiting
				}
				if ctx.Err() != nil {
					return // Context cancelled
				}
				log.Printf("Cleaner Worker %d: Redis error: %v", workerID, err)
				continue
			}

			// result[0] is the queue name, result[1] is the data
			if len(result) < 2 {
				log.Printf("Cleaner Worker %d: Invalid Redis result", workerID)
				continue
			}

			// Parse cleanup job data
			var cleanupJob collector.CleanupJob
			if err := json.Unmarshal([]byte(result[1]), &cleanupJob); err != nil {
				log.Printf("Cleaner Worker %d: Failed to parse cleanup job data: %v", workerID, err)
				continue
			}

			// Process the cleanup job
			if err := c.processCleanupJob(ctx, workerID, &cleanupJob); err != nil {
				log.Printf("Cleaner Worker %d: Failed to process cleanup job for %s/%s: %v", 
					workerID, cleanupJob.Org, cleanupJob.Name, err)
			}

			// Log queue length after job execution
			queueLen, err := c.redisClient.LLen(ctx, c.config.CleanupQueueName).Result()
			if err != nil {
				log.Printf("Cleaner Worker %d: Failed to get queue length after job: %v", workerID, err)
			} else {
				log.Printf("Cleaner Worker %d: Cleanup queue length after job: %d items", workerID, queueLen)
			}
		}
	}
}

// processCleanupJob deletes the repository directory
func (c *Cleaner) processCleanupJob(ctx context.Context, workerID int, job *collector.CleanupJob) error {
	log.Printf("Cleaner Worker %d: Processing cleanup job for %s/%s at path %s", 
		workerID, job.Org, job.Name, job.ClonePath)

	// Validate that the path is within the shared volume
	if !c.isPathSafe(job.ClonePath) {
		return fmt.Errorf("unsafe path: %s is not within shared volume %s", 
			job.ClonePath, c.config.SharedVolumeDir)
	}

	// Check if directory exists
	if _, err := os.Stat(job.ClonePath); os.IsNotExist(err) {
		log.Printf("Cleaner Worker %d: Directory %s does not exist, skipping cleanup", 
			workerID, job.ClonePath)
		return nil
	}

	// Remove the directory
	if err := os.RemoveAll(job.ClonePath); err != nil {
		return fmt.Errorf("failed to remove directory %s: %w", job.ClonePath, err)
	}

	log.Printf("Cleaner Worker %d: Successfully cleaned up %s/%s at path %s", 
		workerID, job.Org, job.Name, job.ClonePath)

	return nil
}

// isPathSafe validates that a path is within the shared volume directory
func (c *Cleaner) isPathSafe(path string) bool {
	// Get absolute paths
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	absSharedDir, err := filepath.Abs(c.config.SharedVolumeDir)
	if err != nil {
		return false
	}

	// Check if the path is within the shared volume
	return strings.HasPrefix(absPath, absSharedDir)
}

// Close cleanly shuts down the cleaner
func (c *Cleaner) Close() {
	if c.redisClient != nil {
		c.redisClient.Close()
	}
}