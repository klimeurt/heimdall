package cloner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/klimeurt/heimdall/internal/collector"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/redis/go-redis/v9"
)

// Cloner handles the repository cloning operations
type Cloner struct {
	config      *config.ClonerConfig
	redisClient *redis.Client
	auth        *http.BasicAuth
}

// New creates a new Cloner instance
func New(cfg *config.ClonerConfig) (*Cloner, error) {
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

	// Set up GitHub authentication if token is provided
	var auth *http.BasicAuth
	if cfg.GitHubToken != "" {
		auth = &http.BasicAuth{
			Username: "token", // GitHub uses "token" as username for personal access tokens
			Password: cfg.GitHubToken,
		}
	}

	// Ensure shared volume directory exists
	if err := os.MkdirAll(cfg.SharedVolumeDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create shared volume directory: %w", err)
	}

	return &Cloner{
		config:      cfg,
		redisClient: redisClient,
		auth:        auth,
	}, nil
}

// Start begins the cloning workers
func (c *Cloner) Start(ctx context.Context) error {
	// Log queue length at startup
	queueLen, err := c.redisClient.LLen(ctx, "clone_queue").Result()
	if err != nil {
		log.Printf("Failed to get queue length at startup: %v", err)
	} else {
		log.Printf("Queue length at startup: %d items", queueLen)
	}

	log.Printf("Starting %d cloner workers", c.config.MaxConcurrentClones)

	var wg sync.WaitGroup

	// Start worker goroutines
	for i := 0; i < c.config.MaxConcurrentClones; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			c.worker(ctx, workerID)
		}(i)
	}

	// Wait for all workers to complete
	wg.Wait()
	log.Println("All cloner workers have stopped")
	return nil
}

// worker processes repository clone jobs from the Redis queue
func (c *Cloner) worker(ctx context.Context, workerID int) {
	log.Printf("Worker %d started", workerID)
	defer log.Printf("Worker %d stopped", workerID)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Block until a job is available or context is cancelled
			result, err := c.redisClient.BRPop(ctx, 0, "clone_queue").Result()
			if err != nil {
				if err == redis.Nil {
					continue // No items in queue, keep waiting
				}
				if ctx.Err() != nil {
					return // Context cancelled
				}
				log.Printf("Worker %d: Redis error: %v", workerID, err)
				continue
			}

			// result[0] is the queue name, result[1] is the data
			if len(result) < 2 {
				log.Printf("Worker %d: Invalid Redis result", workerID)
				continue
			}

			// Parse repository data
			var repo collector.Repository
			if err := json.Unmarshal([]byte(result[1]), &repo); err != nil {
				log.Printf("Worker %d: Failed to parse repository data: %v", workerID, err)
				continue
			}

			// Clone and analyze the repository
			if err := c.processRepository(ctx, workerID, &repo); err != nil {
				log.Printf("Worker %d: Failed to process repository %s/%s: %v", workerID, repo.Org, repo.Name, err)
			}

			// Log queue length after job execution
			queueLen, err := c.redisClient.LLen(ctx, "clone_queue").Result()
			if err != nil {
				log.Printf("Worker %d: Failed to get queue length after job: %v", workerID, err)
			} else {
				log.Printf("Worker %d: Queue length after job: %d items", workerID, queueLen)
			}
		}
	}
}

// processRepository clones a repository and analyzes it
func (c *Cloner) processRepository(ctx context.Context, workerID int, repo *collector.Repository) error {
	log.Printf("Worker %d: Processing repository %s/%s", workerID, repo.Org, repo.Name)

	// Construct repository URL
	repoURL := fmt.Sprintf("https://github.com/%s/%s.git", repo.Org, repo.Name)

	// Clone options
	cloneOptions := &git.CloneOptions{
		URL: repoURL,
	}

	// Add authentication if available
	if c.auth != nil {
		cloneOptions.Auth = c.auth
	}

	// Generate UUID for unique directory name
	uuid, err := generateUUID()
	if err != nil {
		return fmt.Errorf("failed to generate UUID: %w", err)
	}

	// Create clone path
	clonePath := filepath.Join(c.config.SharedVolumeDir, fmt.Sprintf("%s_%s_%s", repo.Org, repo.Name, uuid))

	// Clone repository to disk
	_, err = git.PlainCloneContext(ctx, clonePath, false, cloneOptions)
	if err != nil {
		// Clean up directory if clone fails
		os.RemoveAll(clonePath)
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	// Create processed repository data
	processedRepo := &collector.ProcessedRepository{
		Org:         repo.Org,
		Name:        repo.Name,
		ProcessedAt: time.Now(),
		WorkerID:    workerID,
		ClonePath:   clonePath,
	}

	// Marshal to JSON
	processedData, err := json.Marshal(processedRepo)
	if err != nil {
		return fmt.Errorf("failed to marshal processed repository data: %w", err)
	}

	// Push to processed queue
	if err := c.redisClient.LPush(ctx, c.config.ProcessedQueueName, processedData).Err(); err != nil {
		return fmt.Errorf("failed to push processed repository to queue: %w", err)
	}

	log.Printf("Worker %d: Repository %s/%s cloned and processed successfully", workerID, repo.Org, repo.Name)

	return nil
}

// Close cleanly shuts down the cloner
func (c *Cloner) Close() {
	if c.redisClient != nil {
		c.redisClient.Close()
	}
}

// generateUUID generates a random UUID string
func generateUUID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
