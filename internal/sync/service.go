package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/klimeurt/heimdall/internal/types"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/klimeurt/heimdall/internal/logging"
	"github.com/redis/go-redis/v9"
)

// Service represents the sync service
type Service struct {
	cfg       config.SyncConfig
	rdb       *redis.Client
	client    *http.Client
	mu        sync.Mutex
	syncState map[string]time.Time // Track last sync time for repos
	logger    *slog.Logger
}

// NewService creates a new sync service
func NewService(cfg config.SyncConfig, rdb *redis.Client, logger *slog.Logger) *Service {
	return &Service{
		cfg:       cfg,
		rdb:       rdb,
		client:    &http.Client{Timeout: 30 * time.Second},
		syncState: make(map[string]time.Time),
		logger:    logger,
	}
}

// Start starts the sync service workers
func (s *Service) Start(ctx context.Context) error {
	s.logger.Info("sync service started")
	
	// Just wait for context cancellation
	<-ctx.Done()
	s.logger.Info("sync service stopped")
	return nil
}

// SyncOrganization performs a full sync of the GitHub organization
func (s *Service) SyncOrganization(ctx context.Context) error {
	startTime := time.Now()
	correlationID := fmt.Sprintf("sync-%d", time.Now().Unix())
	ctx = logging.WithCorrelationID(ctx, correlationID)
	logger := logging.LoggerFromContext(ctx, s.logger)
	
	logger.Info("starting organization sync",
		slog.String("org", s.cfg.GitHubOrg))

	// Step 1: Fetch all repositories from GitHub
	githubRepos, err := s.fetchGitHubRepositories(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch GitHub repositories: %w", err)
	}
	logger.Info("fetched GitHub repositories",
		slog.Int("count", len(githubRepos)))

	// Step 2: List local repositories
	localRepos, err := s.listLocalRepositories()
	if err != nil {
		return fmt.Errorf("failed to list local repositories: %w", err)
	}
	logger.Info("found local repositories",
		slog.Int("count", len(localRepos)))

	// Step 3: Create maps for efficient lookups
	githubRepoMap := make(map[string]*Repository)
	for _, repo := range githubRepos {
		githubRepoMap[repo.Name] = repo
	}

	localRepoMap := make(map[string]bool)
	for _, repoName := range localRepos {
		localRepoMap[repoName] = true
	}

	// Step 4: Determine actions needed
	var toClone []*Repository
	var toUpdate []*Repository
	var toRemove []string

	// Find repos to clone or update
	for _, repo := range githubRepos {
		if localRepoMap[repo.Name] {
			toUpdate = append(toUpdate, repo)
		} else {
			toClone = append(toClone, repo)
		}
	}

	// Find repos to remove
	for _, repoName := range localRepos {
		if _, exists := githubRepoMap[repoName]; !exists {
			toRemove = append(toRemove, repoName)
		}
	}

	logger.Info("sync actions determined",
		slog.Int("clone_count", len(toClone)),
		slog.Int("update_count", len(toUpdate)),
		slog.Int("remove_count", len(toRemove)))

	// Step 5: Execute sync operations
	syncErrors := 0
	skippedCount := 0

	// Clone new repositories
	if len(toClone) > 0 {
		errors := s.cloneRepositories(ctx, toClone)
		syncErrors += errors
	}

	// Update existing repositories
	if len(toUpdate) > 0 {
		errors, skipped := s.updateRepositories(ctx, toUpdate)
		syncErrors += errors
		skippedCount += skipped
	}

	// Remove orphaned repositories
	if len(toRemove) > 0 {
		errors := s.removeRepositories(ctx, toRemove)
		syncErrors += errors
	}

	// Step 6: Push all synced repos to scanner queues
	// NOTE: Repositories are now pushed to queues immediately after clone/update
	// so this batch push is no longer needed
	// allRepos := append(toClone, toUpdate...)
	// if err := s.pushToScannerQueues(ctx, allRepos); err != nil {
	// 	log.Printf("Error pushing to scanner queues: %v", err)
	// 	syncErrors++
	// }

	duration := time.Since(startTime)
	actuallyUpdated := len(toUpdate) - skippedCount
	logger.Info("organization sync completed",
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.Int("errors", syncErrors),
		slog.Int("cloned", len(toClone)),
		slog.Int("updated", actuallyUpdated),
		slog.Int("skipped", skippedCount),
		slog.Int("removed", len(toRemove)))

	if syncErrors > 0 {
		return fmt.Errorf("sync completed with %d errors", syncErrors)
	}

	return nil
}

// executeGraphQLQuery executes a GraphQL query against GitHub API
func (s *Service) executeGraphQLQuery(ctx context.Context, query string, variables map[string]interface{}) (*graphQLResponse, error) {
	reqBody := graphQLRequest{
		Query:     query,
		Variables: variables,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal GraphQL request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.github.com/graphql", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create GraphQL request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if s.cfg.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.GitHubToken)
	}

	// Add rate limit delay
	if s.cfg.GitHubAPIDelayMs > 0 {
		time.Sleep(time.Duration(s.cfg.GitHubAPIDelayMs) * time.Millisecond)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GraphQL request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GraphQL API returned status %d", resp.StatusCode)
	}

	var graphQLResp graphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&graphQLResp); err != nil {
		return nil, fmt.Errorf("failed to decode GraphQL response: %w", err)
	}

	if len(graphQLResp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", graphQLResp.Errors)
	}

	return &graphQLResp, nil
}

// fetchGitHubRepositories fetches all repositories from the GitHub organization using GraphQL
func (s *Service) fetchGitHubRepositories(ctx context.Context) ([]*Repository, error) {
	var allRepos []*Repository
	cursor := ""
	
	// GraphQL query to fetch organization repositories
	query := `
		query($org: String!, $cursor: String) {
			organization(login: $org) {
				repositories(first: 100, after: $cursor) {
					pageInfo {
						hasNextPage
						endCursor
					}
					nodes {
						name
						url
						sshUrl
						isPrivate
					}
				}
			}
		}
	`

	for {
		// Prepare variables for the GraphQL query
		variables := map[string]interface{}{
			"org": s.cfg.GitHubOrg,
		}
		if cursor != "" {
			variables["cursor"] = cursor
		}

		// Execute GraphQL query
		resp, err := s.executeGraphQLQuery(ctx, query, variables)
		if err != nil {
			return nil, fmt.Errorf("failed to execute GraphQL query: %w", err)
		}

		// Check if organization exists
		if resp.Data == nil || resp.Data.Organization == nil {
			return nil, fmt.Errorf("organization '%s' not found", s.cfg.GitHubOrg)
		}

		// Convert GraphQL repositories to our Repository model
		for _, gqlRepo := range resp.Data.Organization.Repositories.Nodes {
			repo := &Repository{
				Name:     gqlRepo.Name,
				Org:      s.cfg.GitHubOrg,
				URL:      gqlRepo.URL,
				SSHURL:   gqlRepo.SSHURL,
				Private:  gqlRepo.IsPrivate,
			}
			allRepos = append(allRepos, repo)
		}

		// Check if we have more pages
		if !resp.Data.Organization.Repositories.PageInfo.HasNextPage {
			break
		}
		cursor = resp.Data.Organization.Repositories.PageInfo.EndCursor
	}

	return allRepos, nil
}

// listLocalRepositories lists all repositories in the shared volume
func (s *Service) listLocalRepositories() ([]string, error) {
	orgPath := filepath.Join(s.cfg.SharedVolumePath, s.cfg.GitHubOrg)
	
	// Check if org directory exists
	if _, err := os.Stat(orgPath); os.IsNotExist(err) {
		// Directory doesn't exist, no local repos
		return []string{}, nil
	}

	entries, err := os.ReadDir(orgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", orgPath, err)
	}

	var repos []string
	for _, entry := range entries {
		if entry.IsDir() {
			// Check if it's a git repository
			gitDir := filepath.Join(orgPath, entry.Name(), ".git")
			if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
				repos = append(repos, entry.Name())
			}
		}
	}

	return repos, nil
}

// cloneRepositories clones new repositories
func (s *Service) cloneRepositories(ctx context.Context, repos []*Repository) int {
	errors := 0
	sem := make(chan struct{}, s.cfg.MaxConcurrentSyncs)
	var wg sync.WaitGroup

	for _, repo := range repos {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(r *Repository) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			if err := s.cloneRepository(ctx, r); err != nil {
				s.logger.Error("failed to clone repository",
					slog.String("org", r.Org),
					slog.String("repo", r.Name),
					slog.String("error", err.Error()))
				errors++
			}
		}(repo)
	}

	wg.Wait()
	return errors
}

// updateRepositories updates existing repositories
func (s *Service) updateRepositories(ctx context.Context, repos []*Repository) (int, int) {
	errors := 0
	skipped := 0
	sem := make(chan struct{}, s.cfg.MaxConcurrentSyncs)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, repo := range repos {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(r *Repository) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			// Check if repository needs update
			needsUpdate, err := s.repositoryNeedsUpdate(ctx, r)
			if err != nil {
				s.logger.Warn("failed to check if repository needs update, will perform full update",
					slog.String("org", r.Org),
					slog.String("repo", r.Name),
					slog.String("error", err.Error()))
				needsUpdate = true // Fallback to updating on error
			}

			if !needsUpdate {
				s.logger.Info("repository is up to date, skipping update",
					slog.String("org", r.Org),
					slog.String("repo", r.Name))
				
				// Even if we skip the git update, we still need to push to scanner queues
				// This ensures repositories are still processed by scanners even if unchanged
				if err := s.pushRepositoryToQueues(ctx, r); err != nil {
					s.logger.Warn("failed to push repository to queues",
						slog.String("org", r.Org),
						slog.String("repo", r.Name),
						slog.String("error", err.Error()))
				}
				
				mu.Lock()
				skipped++
				mu.Unlock()
				return
			}

			// Repository needs update, proceed with normal update
			if err := s.updateRepository(ctx, r); err != nil {
				s.logger.Error("failed to update repository",
					slog.String("org", r.Org),
					slog.String("repo", r.Name),
					slog.String("error", err.Error()))
				mu.Lock()
				errors++
				mu.Unlock()
			}
		}(repo)
	}

	wg.Wait()
	return errors, skipped
}

// removeRepositories removes orphaned repositories
func (s *Service) removeRepositories(ctx context.Context, repoNames []string) int {
	errors := 0
	for _, repoName := range repoNames {
		if err := s.removeRepository(ctx, repoName); err != nil {
			s.logger.Error("failed to remove repository",
				slog.String("org", s.cfg.GitHubOrg),
				slog.String("repo", repoName),
				slog.String("error", err.Error()))
			errors++
		}
	}
	return errors
}

// pushToScannerQueues pushes repositories to scanner queues
func (s *Service) pushToScannerQueues(ctx context.Context, repos []*Repository) error {
	// Check if scanner queues are globally disabled
	if !s.cfg.EnableScannerQueues {
		s.logger.Debug("scanner queues disabled, skipping batch repository push",
			slog.Int("count", len(repos)))
		return nil
	}

	// Check if any queues are enabled
	if !s.cfg.EnableTruffleHogQueue && !s.cfg.EnableOSVQueue {
		s.logger.Debug("no scanner queues enabled, skipping batch repository push",
			slog.Int("count", len(repos)))
		return nil
	}

	// Process in batches
	batchSize := s.cfg.QueueBatchSize
	if batchSize <= 0 {
		batchSize = 10
	}

	pushedQueues := []string{}
	if s.cfg.EnableTruffleHogQueue {
		pushedQueues = append(pushedQueues, s.cfg.TruffleHogQueueName)
	}
	if s.cfg.EnableOSVQueue {
		pushedQueues = append(pushedQueues, s.cfg.OSVQueueName)
	}

	for i := 0; i < len(repos); i += batchSize {
		end := i + batchSize
		if end > len(repos) {
			end = len(repos)
		}

		batch := repos[i:end]
		pipe := s.rdb.Pipeline()

		for _, repo := range batch {
			// Create ProcessedRepository with the new path structure
			processed := types.ProcessedRepository{
				Org:         repo.Org,
				Name:        repo.Name,
				ClonePath:   filepath.Join(s.cfg.SharedVolumePath, repo.Org, repo.Name),
				ProcessedAt: time.Now(),
			}

			data, err := json.Marshal(processed)
			if err != nil {
				return fmt.Errorf("failed to marshal repository %s: %w", repo.Name, err)
			}

			// Push to enabled scanner queues
			if s.cfg.EnableTruffleHogQueue {
				pipe.LPush(ctx, s.cfg.TruffleHogQueueName, data)
			}
			if s.cfg.EnableOSVQueue {
				pipe.LPush(ctx, s.cfg.OSVQueueName, data)
			}
		}

		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("failed to push batch to queues: %w", err)
		}
	}

	s.logger.Info("pushed repositories to scanner queues",
		slog.Int("count", len(repos)),
		slog.Any("queues", pushedQueues))
	return nil
}

// Repository represents a GitHub repository
type Repository struct {
	Name     string
	Org      string
	URL string
	SSHURL   string
	Private  bool
}

// GraphQL types for GitHub API
type graphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

type graphQLResponse struct {
	Data   *graphQLData    `json:"data"`
	Errors []graphQLError  `json:"errors"`
}

type graphQLData struct {
	Organization *graphQLOrganization `json:"organization"`
}

type graphQLOrganization struct {
	Repositories graphQLRepositories `json:"repositories"`
}

type graphQLRepositories struct {
	PageInfo graphQLPageInfo       `json:"pageInfo"`
	Nodes    []graphQLRepository   `json:"nodes"`
}

type graphQLPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type graphQLRepository struct {
	Name      string `json:"name"`
	URL  string `json:"url"`
	SSHURL    string `json:"sshUrl"`
	IsPrivate bool   `json:"isPrivate"`
}

type graphQLError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// pushRepositoryToQueues pushes a single repository to scanner queues
func (s *Service) pushRepositoryToQueues(ctx context.Context, repo *Repository) error {
	// Check if scanner queues are globally disabled
	if !s.cfg.EnableScannerQueues {
		s.logger.Debug("scanner queues disabled, skipping repository push",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name))
		return nil
	}

	// Create ProcessedRepository with the new path structure
	processed := types.ProcessedRepository{
		Org:         repo.Org,
		Name:        repo.Name,
		ClonePath:   filepath.Join(s.cfg.SharedVolumePath, repo.Org, repo.Name),
		ProcessedAt: time.Now(),
	}

	data, err := json.Marshal(processed)
	if err != nil {
		return fmt.Errorf("failed to marshal repository %s: %w", repo.Name, err)
	}

	// Push to scanner queues based on individual queue configuration
	pipe := s.rdb.Pipeline()
	pushedQueues := []string{}

	if s.cfg.EnableTruffleHogQueue {
		pipe.LPush(ctx, s.cfg.TruffleHogQueueName, data)
		pushedQueues = append(pushedQueues, s.cfg.TruffleHogQueueName)
	}

	if s.cfg.EnableOSVQueue {
		pipe.LPush(ctx, s.cfg.OSVQueueName, data)
		pushedQueues = append(pushedQueues, s.cfg.OSVQueueName)
	}

	// If no queues are enabled, skip execution
	if len(pushedQueues) == 0 {
		s.logger.Debug("no scanner queues enabled, skipping repository push",
			slog.String("org", repo.Org),
			slog.String("repo", repo.Name))
		return nil
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to push repository to queues: %w", err)
	}

	s.logger.Info("pushed repository to scanner queues",
		slog.String("org", repo.Org),
		slog.String("repo", repo.Name),
		slog.Any("queues", pushedQueues))
	return nil
}

// Helper methods (cloneRepository, updateRepository, removeRepository) will be implemented next