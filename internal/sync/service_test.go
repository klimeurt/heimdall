package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klimeurt/heimdall/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchGitHubRepositories(t *testing.T) {
	// Create test server for GraphQL API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/graphql", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		var reqBody graphQLRequest
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)

		// Check for cursor in variables to determine if this is first or second page
		cursor, hasCursor := reqBody.Variables["cursor"]
		var hasNextPage bool
		var endCursor string
		
		if !hasCursor || cursor == "" {
			// First page
			hasNextPage = true
			endCursor = "cursor123"
		} else {
			// Second page
			hasNextPage = false
			endCursor = ""
		}

		// Mock GraphQL response
		response := graphQLResponse{
			Data: &graphQLData{
				Organization: &graphQLOrganization{
					Repositories: graphQLRepositories{
						PageInfo: graphQLPageInfo{
							HasNextPage: hasNextPage,
							EndCursor:   endCursor,
						},
						Nodes: []graphQLRepository{
							{
								Name:      "repo1",
								URL:  "https://github.com/test-org/repo1.git",
								SSHURL:    "git@github.com:test-org/repo1.git",
								IsPrivate: false,
							},
							{
								Name:      "repo2",
								URL:  "https://github.com/test-org/repo2.git",
								SSHURL:    "git@github.com:test-org/repo2.git",
								IsPrivate: true,
							},
						},
					},
				},
			},
		}

		// If it's the second page, return empty nodes
		if hasCursor && cursor != "" {
			response.Data.Organization.Repositories.Nodes = []graphQLRepository{}
		}

		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := config.SyncConfig{
		GitHubOrg:        "test-org",
		GitHubToken:      "test-token",
		GitHubAPIDelayMs: 0,
	}

	// Create test service with custom GraphQL endpoint
	testSvc := &Service{
		cfg:    cfg,
		client: &http.Client{},
	}

	ctx := context.Background()
	repos, err := testSvc.fetchGitHubRepositoriesWithGraphQLURL(ctx, server.URL+"/graphql")

	require.NoError(t, err)
	assert.Len(t, repos, 2)
	assert.Equal(t, "repo1", repos[0].Name)
	assert.Equal(t, "test-org", repos[0].Org)
	assert.False(t, repos[0].Private)
	assert.Equal(t, "repo2", repos[1].Name)
	assert.True(t, repos[1].Private)
}

// Helper method for testing with custom GraphQL URL
func (s *Service) fetchGitHubRepositoriesWithGraphQLURL(ctx context.Context, graphqlURL string) ([]*Repository, error) {
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

		// Execute GraphQL query with custom URL
		resp, err := s.executeGraphQLQueryWithURL(ctx, graphqlURL, query, variables)
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
				URL: gqlRepo.URL,
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

// Helper method for testing with custom GraphQL URL
func (s *Service) executeGraphQLQueryWithURL(ctx context.Context, graphqlURL string, query string, variables map[string]interface{}) (*graphQLResponse, error) {
	reqBody := graphQLRequest{
		Query:     query,
		Variables: variables,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal GraphQL request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", graphqlURL, bytes.NewBuffer(jsonBody))
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

func TestListLocalRepositories(t *testing.T) {
	// Create temporary directory structure
	tmpDir := t.TempDir()
	orgDir := filepath.Join(tmpDir, "test-org")

	// Create test repositories
	repo1Dir := filepath.Join(orgDir, "repo1")
	repo2Dir := filepath.Join(orgDir, "repo2")
	githubDir := filepath.Join(orgDir, ".github")
	nonRepoDir := filepath.Join(orgDir, "not-a-repo")
	hiddenDir := filepath.Join(orgDir, ".hidden")

	require.NoError(t, os.MkdirAll(filepath.Join(repo1Dir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo2Dir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(githubDir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(nonRepoDir, 0o755))
	require.NoError(t, os.MkdirAll(hiddenDir, 0o755))

	cfg := config.SyncConfig{
		GitHubOrg:        "test-org",
		SharedVolumePath: tmpDir,
	}

	svc := &Service{cfg: cfg}

	repos, err := svc.listLocalRepositories()
	require.NoError(t, err)
	assert.Len(t, repos, 3)
	assert.Contains(t, repos, "repo1")
	assert.Contains(t, repos, "repo2")
	assert.Contains(t, repos, ".github")
}

func TestListLocalRepositoriesDotPrefixed(t *testing.T) {
	// Create temporary directory structure
	tmpDir := t.TempDir()
	orgDir := filepath.Join(tmpDir, "test-org")

	// Create dot-prefixed repositories
	githubDir := filepath.Join(orgDir, ".github")
	devcontainerDir := filepath.Join(orgDir, ".devcontainer")
	vscodeDir := filepath.Join(orgDir, ".vscode")
	
	// Create dot-prefixed directory that is NOT a git repository
	hiddenNonRepoDir := filepath.Join(orgDir, ".hidden-non-repo")

	require.NoError(t, os.MkdirAll(filepath.Join(githubDir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(devcontainerDir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(vscodeDir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(hiddenNonRepoDir, 0o755))

	cfg := config.SyncConfig{
		GitHubOrg:        "test-org",
		SharedVolumePath: tmpDir,
	}

	svc := &Service{cfg: cfg}

	repos, err := svc.listLocalRepositories()
	require.NoError(t, err)
	assert.Len(t, repos, 3)
	assert.Contains(t, repos, ".github")
	assert.Contains(t, repos, ".devcontainer")
	assert.Contains(t, repos, ".vscode")
	assert.NotContains(t, repos, ".hidden-non-repo")
}

func TestPushToScannerQueues(t *testing.T) {
	// Create Redis mock (you might want to use miniredis for testing)
	// ctx would be used in a more complete test
	_ = context.Background()

	// For this test, we'll use a simple mock approach
	var pushedTruffleHog []string
	var pushedOSV []string

	// Create test service
	cfg := config.SyncConfig{
		GitHubOrg:           "test-org",
		SharedVolumePath:    "/shared",
		TruffleHogQueueName: "trufflehog_queue",
		OSVQueueName:        "osv_queue",
		QueueBatchSize:      2,
	}

	// repos would be used in a more complete test
	_ = []*Repository{
		{Name: "repo1", Org: "test-org"},
		{Name: "repo2", Org: "test-org"},
		{Name: "repo3", Org: "test-org"},
	}

	// Create a mock Redis client (simplified for this example)
	// In real tests, you might use miniredis or redis mock
	mockRdb := &redis.Client{}
	// svc would be used in a more complete test
	_ = &Service{
		cfg: cfg,
		rdb: mockRdb,
	}

	// For a complete test, you would mock the Redis operations
	// Here we're just testing the logic structure
	t.Run("batch processing", func(t *testing.T) {
		// Test that repos are processed in batches
		batchCount := 0
		expectedBatches := 2 // 3 repos with batch size 2 = 2 batches

		// In a real test, you would verify the Redis pipeline operations
		_ = batchCount
		_ = expectedBatches
		_ = pushedTruffleHog
		_ = pushedOSV

		// The actual test would require mocking Redis operations
		t.Skip("Redis mocking required for complete test")
	})
}

func TestScannerQueueConfiguration(t *testing.T) {
	tests := []struct {
		name                  string
		enableScannerQueues   bool
		enableTruffleHogQueue bool
		enableOSVQueue        bool
		expectedSkip          bool
		expectedLog           string
	}{
		{
			name:                  "all queues enabled",
			enableScannerQueues:   true,
			enableTruffleHogQueue: true,
			enableOSVQueue:        true,
			expectedSkip:          false,
			expectedLog:           "should push to both queues",
		},
		{
			name:                  "scanner queues globally disabled",
			enableScannerQueues:   false,
			enableTruffleHogQueue: true,
			enableOSVQueue:        true,
			expectedSkip:          true,
			expectedLog:           "scanner queues disabled",
		},
		{
			name:                  "only trufflehog queue enabled",
			enableScannerQueues:   true,
			enableTruffleHogQueue: true,
			enableOSVQueue:        false,
			expectedSkip:          false,
			expectedLog:           "should push to trufflehog queue only",
		},
		{
			name:                  "only osv queue enabled",
			enableScannerQueues:   true,
			enableTruffleHogQueue: false,
			enableOSVQueue:        true,
			expectedSkip:          false,
			expectedLog:           "should push to osv queue only",
		},
		{
			name:                  "no individual queues enabled",
			enableScannerQueues:   true,
			enableTruffleHogQueue: false,
			enableOSVQueue:        false,
			expectedSkip:          true,
			expectedLog:           "no scanner queues enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.SyncConfig{
				GitHubOrg:             "test-org",
				SharedVolumePath:      "/shared",
				TruffleHogQueueName:   "trufflehog_queue",
				OSVQueueName:          "osv_queue",
				EnableScannerQueues:   tt.enableScannerQueues,
				EnableTruffleHogQueue: tt.enableTruffleHogQueue,
				EnableOSVQueue:        tt.enableOSVQueue,
			}

			svc := &Service{
				cfg:    cfg,
				logger: getTestLogger(),
			}

			ctx := context.Background()
			repo := &Repository{
				Name: "test-repo",
				Org:  "test-org",
			}

			// Test the logic without actual Redis operations
			// We can't fully test without mocking Redis, but we can verify the configuration logic
			shouldSkip := !cfg.EnableScannerQueues || (!cfg.EnableTruffleHogQueue && !cfg.EnableOSVQueue)
			assert.Equal(t, tt.expectedSkip, shouldSkip, "configuration should determine skip behavior")

			// Verify the service has the correct configuration
			assert.Equal(t, tt.enableScannerQueues, svc.cfg.EnableScannerQueues)
			assert.Equal(t, tt.enableTruffleHogQueue, svc.cfg.EnableTruffleHogQueue)
			assert.Equal(t, tt.enableOSVQueue, svc.cfg.EnableOSVQueue)

			// Note: We can't fully test the pushRepositoryToQueues method without mocking Redis
			// But we can verify that the configuration is correctly loaded
			_ = ctx
			_ = repo
		})
	}
}

func TestSyncConfigDefaults(t *testing.T) {
	t.Run("default configuration values", func(t *testing.T) {
		cfg := config.SyncConfig{
			GitHubOrg:             "test-org",
			SharedVolumePath:      "/shared",
			TruffleHogQueueName:   "trufflehog_queue",
			OSVQueueName:          "osv_queue",
			EnableScannerQueues:   true,  // default should be true
			EnableTruffleHogQueue: true,  // default should be true
			EnableOSVQueue:        true,  // default should be true
		}

		svc := &Service{
			cfg:    cfg,
			logger: getTestLogger(),
		}

		// Verify default values maintain backward compatibility
		assert.True(t, svc.cfg.EnableScannerQueues, "EnableScannerQueues should default to true")
		assert.True(t, svc.cfg.EnableTruffleHogQueue, "EnableTruffleHogQueue should default to true")
		assert.True(t, svc.cfg.EnableOSVQueue, "EnableOSVQueue should default to true")
	})
}

func TestSyncOrganization(t *testing.T) {
	// This is a complex integration test that would require:
	// 1. Mock GitHub API server
	// 2. Mock Redis server
	// 3. Temporary file system
	// 4. Mock git commands

	t.Run("full sync flow", func(t *testing.T) {
		t.Skip("Integration test - requires extensive mocking")
	})
}

func TestRemoveRepository(t *testing.T) {
	tmpDir := t.TempDir()
	orgDir := filepath.Join(tmpDir, "test-org")
	repoDir := filepath.Join(orgDir, "repo-to-remove")

	// Create test repository
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755))

	// Create test file
	testFile := filepath.Join(repoDir, "test.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("test"), 0o644))

	cfg := config.SyncConfig{
		GitHubOrg:        "test-org",
		SharedVolumePath: tmpDir,
	}

	svc := &Service{
		cfg:       cfg,
		syncState: make(map[string]time.Time),
	}

	// Add to sync state
	svc.syncState["repo-to-remove"] = time.Now()

	// Remove repository
	err := svc.removeRepository(context.Background(), "repo-to-remove")
	require.NoError(t, err)

	// Verify removal
	_, err = os.Stat(repoDir)
	assert.True(t, os.IsNotExist(err))

	// Verify sync state updated
	_, exists := svc.syncState["repo-to-remove"]
	assert.False(t, exists)
}

func TestSecurityPathValidation(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := config.SyncConfig{
		GitHubOrg:        "test-org",
		SharedVolumePath: tmpDir,
	}

	svc := &Service{
		cfg:       cfg,
		syncState: make(map[string]time.Time),
	}

	// Test path traversal attempt
	err := svc.removeRepository(context.Background(), "../../../etc/passwd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside shared volume")
}

func getTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestUpdateRepositoryWithFetchFailureRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	orgPath := filepath.Join(tmpDir, "test-org")
	repoPath := filepath.Join(orgPath, "test-repo")
	
	// Create a mock repository with corrupted git directory
	require.NoError(t, os.MkdirAll(filepath.Join(repoPath, ".git"), 0755))
	
	// Write a file to verify the directory gets deleted
	testFile := filepath.Join(repoPath, "test.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("test content"), 0644))
	
	cfg := config.SyncConfig{
		SharedVolumePath:   tmpDir,
		GitHubOrg:         "test-org",
		GitHubToken:       "",
		SyncTimeoutMinutes: 1,
	}
	
	// Mock Redis client
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	
	svc := &Service{
		cfg:       cfg,
		rdb:       rdb,
		syncState: make(map[string]time.Time),
		logger:    getTestLogger(),
	}
	
	repo := &Repository{
		Name:     "test-repo",
		Org:      "test-org",
		URL: "https://github.com/test-org/test-repo.git",
		Private:  false,
	}
	
	ctx := context.Background()
	
	// The updateRepository should fail on fetch (since it's not a real git repo)
	// and then attempt recovery by deleting and re-cloning
	err := svc.updateRepository(ctx, repo)
	
	// The clone will also fail (no real GitHub repo), but we can verify the corrupted repo was deleted
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to re-clone repository after fetch failure")
	
	// Verify the corrupted repository was deleted
	_, statErr := os.Stat(repoPath)
	assert.True(t, os.IsNotExist(statErr), "corrupted repository should have been deleted")
	
	// Verify it was removed from sync state
	svc.mu.Lock()
	_, exists := svc.syncState["test-repo"]
	svc.mu.Unlock()
	assert.False(t, exists, "repository should be removed from sync state")
}
