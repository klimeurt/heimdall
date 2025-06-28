package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/klimeurt/heimdall/internal/collector"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/redis/go-redis/v9"
)

// RedisClient interface for Redis operations (allows mocking in tests)
type RedisClient interface {
	Ping(ctx context.Context) *redis.StatusCmd
	LLen(ctx context.Context, key string) *redis.IntCmd
	BRPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd
	LPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	Close() error
}

// Coordinator handles coordination between multiple scanners
type Coordinator struct {
	config      *config.CoordinatorConfig
	redisClient RedisClient
	state       *StateManager
}

// JobState represents the state of a scanning job
type JobState struct {
	collector.CoordinationState
	LastUpdated time.Time
}

// StateManager manages the in-memory state of scanning jobs
type StateManager struct {
	mu   sync.RWMutex
	jobs map[string]*JobState // Key is clone_path
}

// New creates a new Coordinator instance
func New(cfg *config.CoordinatorConfig) (*Coordinator, error) {
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

	// Initialize state manager
	state := &StateManager{
		jobs: make(map[string]*JobState),
	}

	return &Coordinator{
		config:      cfg,
		redisClient: redisClient,
		state:       state,
	}, nil
}

// Start begins the coordinator
func (c *Coordinator) Start(ctx context.Context) error {
	log.Println("Starting coordinator")

	// Start state cleanup goroutine
	go c.cleanupExpiredJobs(ctx)

	// Start worker to process coordination messages
	return c.processCoordinationMessages(ctx)
}

// processCoordinationMessages processes messages from scanners
func (c *Coordinator) processCoordinationMessages(ctx context.Context) error {
	log.Println("Coordinator listening for scanner completion messages")

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			// Block until a message is available or context is cancelled
			result, err := c.redisClient.BRPop(ctx, 0, c.config.CoordinatorQueueName).Result()
			if err != nil {
				if err == redis.Nil {
					continue // No items in queue, keep waiting
				}
				if ctx.Err() != nil {
					return nil // Context cancelled
				}
				log.Printf("Coordinator: Redis error: %v", err)
				continue
			}

			// result[0] is the queue name, result[1] is the data
			if len(result) < 2 {
				log.Printf("Coordinator: Invalid Redis result")
				continue
			}

			// Parse coordination message
			var msg collector.ScanCoordinationMessage
			if err := json.Unmarshal([]byte(result[1]), &msg); err != nil {
				log.Printf("Coordinator: Failed to parse coordination message: %v", err)
				continue
			}

			// Process the message
			if err := c.processMessage(ctx, &msg); err != nil {
				log.Printf("Coordinator: Failed to process message for %s/%s: %v", msg.Org, msg.Name, err)
			}
		}
	}
}

// processMessage handles a coordination message from a scanner
func (c *Coordinator) processMessage(ctx context.Context, msg *collector.ScanCoordinationMessage) error {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()

	// Get or create job state
	job, exists := c.state.jobs[msg.ClonePath]
	if !exists {
		job = &JobState{
			CoordinationState: collector.CoordinationState{
				ClonePath: msg.ClonePath,
				Org:       msg.Org,
				Name:      msg.Name,
				StartedAt: time.Now(),
			},
		}
		c.state.jobs[msg.ClonePath] = job
	}

	// Update job state based on scanner type
	switch msg.ScannerType {
	case "trufflehog":
		job.TruffleHogComplete = true
		job.TruffleHogStatus = msg.ScanStatus
		log.Printf("Coordinator: TruffleHog scan completed for %s/%s (status: %s)", msg.Org, msg.Name, msg.ScanStatus)
	case "osv":
		job.OSVComplete = true
		job.OSVStatus = msg.ScanStatus
		log.Printf("Coordinator: OSV scan completed for %s/%s (status: %s)", msg.Org, msg.Name, msg.ScanStatus)
	default:
		return fmt.Errorf("unknown scanner type: %s", msg.ScannerType)
	}

	job.LastUpdated = time.Now()

	// Check if both scanners have completed
	if job.TruffleHogComplete && job.OSVComplete {
		log.Printf("Coordinator: Both scanners completed for %s/%s, sending cleanup job", msg.Org, msg.Name)

		// Send cleanup job
		if err := c.sendCleanupJob(ctx, job); err != nil {
			return fmt.Errorf("failed to send cleanup job: %w", err)
		}

		// Remove job from state
		delete(c.state.jobs, msg.ClonePath)

		log.Printf("Coordinator: Cleanup job sent for %s/%s", msg.Org, msg.Name)
	} else {
		log.Printf("Coordinator: Waiting for other scanner to complete for %s/%s (TruffleHog: %v, OSV: %v)",
			msg.Org, msg.Name, job.TruffleHogComplete, job.OSVComplete)
	}

	return nil
}

// sendCleanupJob sends a cleanup job to the cleanup queue
func (c *Coordinator) sendCleanupJob(ctx context.Context, job *JobState) error {
	cleanupJob := &collector.CleanupJob{
		ClonePath:   job.ClonePath,
		Org:         job.Org,
		Name:        job.Name,
		RequestedAt: time.Now(),
		WorkerID:    0, // Coordinator doesn't have a worker ID
	}

	// Marshal to JSON
	cleanupData, err := json.Marshal(cleanupJob)
	if err != nil {
		return fmt.Errorf("failed to marshal cleanup job data: %w", err)
	}

	// Push to cleanup queue
	if err := c.redisClient.LPush(ctx, c.config.CleanupQueueName, cleanupData).Err(); err != nil {
		return fmt.Errorf("failed to push cleanup job to queue: %w", err)
	}

	return nil
}

// cleanupExpiredJobs periodically removes jobs that have timed out
func (c *Coordinator) cleanupExpiredJobs(ctx context.Context) {
	ticker := time.NewTicker(c.config.StateCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.performCleanup(ctx)
		}
	}
}

// performCleanup removes expired jobs from state
func (c *Coordinator) performCleanup(ctx context.Context) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()

	now := time.Now()
	timeout := time.Duration(c.config.JobTimeoutMinutes) * time.Minute
	expiredCount := 0

	for clonePath, job := range c.state.jobs {
		if now.Sub(job.LastUpdated) > timeout {
			log.Printf("Coordinator: Job timeout for %s/%s (started: %v, last updated: %v)",
				job.Org, job.Name, job.StartedAt, job.LastUpdated)

			// Send cleanup job anyway to prevent orphaned directories
			if err := c.sendCleanupJob(ctx, job); err != nil {
				log.Printf("Coordinator: Failed to send cleanup for expired job %s/%s: %v",
					job.Org, job.Name, err)
			}

			delete(c.state.jobs, clonePath)
			expiredCount++
		}
	}

	if expiredCount > 0 {
		log.Printf("Coordinator: Cleaned up %d expired jobs", expiredCount)
	}

	// Log current state size
	log.Printf("Coordinator: Currently tracking %d active jobs", len(c.state.jobs))
}

// GetActiveJobs returns the current active jobs (for monitoring/debugging)
func (c *Coordinator) GetActiveJobs() []JobState {
	c.state.mu.RLock()
	defer c.state.mu.RUnlock()

	jobs := make([]JobState, 0, len(c.state.jobs))
	for _, job := range c.state.jobs {
		jobs = append(jobs, *job)
	}
	return jobs
}

// Close cleanly shuts down the coordinator
func (c *Coordinator) Close() {
	if c.redisClient != nil {
		c.redisClient.Close()
	}
}
