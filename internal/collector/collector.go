package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/go-github/v57/github"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
)

// Collector handles the GitHub collection operations
type Collector struct {
	config      *config.Config
	ghClient    *github.Client
	redisClient *redis.Client
}

// New creates a new Collector instance
func New(cfg *config.Config) (*Collector, error) {
	// Create GitHub client
	ctx := context.Background()
	var ghClient *github.Client
	
	if cfg.GitHubToken != "" {
		// Use authenticated client if token is provided
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: cfg.GitHubToken},
		)
		tc := oauth2.NewClient(ctx, ts)
		ghClient = github.NewClient(tc)
	} else {
		// Use unauthenticated client for public repositories
		ghClient = github.NewClient(nil)
	}

	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort),
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	// Test Redis connection
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &Collector{
		config:      cfg,
		ghClient:    ghClient,
		redisClient: redisClient,
	}, nil
}

// CollectRepositories fetches all repositories from the GitHub organization
func (c *Collector) CollectRepositories(ctx context.Context) error {
	log.Printf("Starting repository collection for organization: %s", c.config.GitHubOrg)

	opt := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: c.config.GitHubPageSize},
	}

	totalProcessed := 0
	pageNum := 1

	for {
		repos, resp, err := c.ghClient.Repositories.ListByOrg(ctx, c.config.GitHubOrg, opt)
		if err != nil {
			return fmt.Errorf("failed to list repositories: %w", err)
		}

		log.Printf("Processing page %d with %d repositories", pageNum, len(repos))

		// Process and send each repository from this page to Redis queue immediately
		pageProcessed := 0
		for _, repo := range repos {
			if err := c.queueRepository(ctx, repo); err != nil {
				log.Printf("Failed to queue repository %s: %v", repo.GetName(), err)
				// Continue processing other repositories
			} else {
				pageProcessed++
			}
		}

		totalProcessed += pageProcessed
		log.Printf("Completed page %d: queued %d/%d repositories (total: %d)", pageNum, pageProcessed, len(repos), totalProcessed)

		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
		pageNum++

		// Add delay between API requests if configured
		if c.config.GitHubAPIDelay > 0 {
			time.Sleep(c.config.GitHubAPIDelay)
		}
	}

	log.Printf("Repository collection completed: successfully processed %d repositories across %d pages", totalProcessed, pageNum)
	return nil
}

// queueRepository queues a repository to the Redis queue
func (c *Collector) queueRepository(ctx context.Context, repo *github.Repository) error {
	// Convert GitHub repository to our simplified Repository struct
	r := Repository{
		Org:  c.config.GitHubOrg,
		Name: repo.GetName(),
	}

	// Serialize to JSON
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("failed to marshal repository: %w", err)
	}

	// Push to Redis queue
	if err := c.redisClient.LPush(ctx, "clone_queue", data).Err(); err != nil {
		return fmt.Errorf("failed to push to Redis queue: %w", err)
	}

	log.Printf("Queued repository: %s/%s", r.Org, r.Name)
	return nil
}

// Close cleanly shuts down the collector
func (c *Collector) Close() {
	if c.redisClient != nil {
		c.redisClient.Close()
	}
}
