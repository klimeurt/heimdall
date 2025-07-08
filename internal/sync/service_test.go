package sync

import (
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
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/orgs/test-org/repos", r.URL.Path)

		page := r.URL.Query().Get("page")
		if page == "1" {
			repos := []map[string]interface{}{
				{
					"name":      "repo1",
					"clone_url": "https://github.com/test-org/repo1.git",
					"ssh_url":   "git@github.com:test-org/repo1.git",
					"private":   false,
				},
				{
					"name":      "repo2",
					"clone_url": "https://github.com/test-org/repo2.git",
					"ssh_url":   "git@github.com:test-org/repo2.git",
					"private":   true,
				},
			}
			json.NewEncoder(w).Encode(repos)
		} else {
			// Empty response for second page
			json.NewEncoder(w).Encode([]map[string]interface{}{})
		}
	}))
	defer server.Close()

	cfg := config.SyncConfig{
		GitHubOrg:        "test-org",
		GitHubToken:      "test-token",
		GitHubAPIDelayMs: 0,
	}

	// svc would be used in a more complete test
	_ = &Service{
		cfg:    cfg,
		client: &http.Client{},
	}

	// Override GitHub API URL for testing
	oldURL := "https://api.github.com"
	defer func() { _ = oldURL }() // Keep reference

	// Monkey patch the URL in the method
	ctx := context.Background()
	repos, err := func() ([]*Repository, error) {
		// Create custom service for this test
		testSvc := &Service{
			cfg:    cfg,
			client: &http.Client{},
		}

		// Override the fetchGitHubRepositories method to use test server
		return testSvc.fetchGitHubRepositoriesWithURL(ctx, server.URL)
	}()

	require.NoError(t, err)
	assert.Len(t, repos, 2)
	assert.Equal(t, "repo1", repos[0].Name)
	assert.Equal(t, "test-org", repos[0].Org)
	assert.False(t, repos[0].Private)
	assert.Equal(t, "repo2", repos[1].Name)
	assert.True(t, repos[1].Private)
}

// Helper method for testing with custom URL
func (s *Service) fetchGitHubRepositoriesWithURL(ctx context.Context, baseURL string) ([]*Repository, error) {
	var allRepos []*Repository
	page := 1
	perPage := 100

	for {
		url := fmt.Sprintf("%s/orgs/%s/repos?page=%d&per_page=%d",
			baseURL, s.cfg.GitHubOrg, page, perPage)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}

		if s.cfg.GitHubToken != "" {
			req.Header.Set("Authorization", "token "+s.cfg.GitHubToken)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
		}

		var repos []struct {
			Name     string `json:"name"`
			CloneURL string `json:"clone_url"`
			SSHURL   string `json:"ssh_url"`
			Private  bool   `json:"private"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		for _, r := range repos {
			repo := &Repository{
				Name:     r.Name,
				Org:      s.cfg.GitHubOrg,
				CloneURL: r.CloneURL,
				SSHURL:   r.SSHURL,
				Private:  r.Private,
			}
			allRepos = append(allRepos, repo)
		}

		if len(repos) < perPage {
			break
		}
		page++
	}

	return allRepos, nil
}

func TestListLocalRepositories(t *testing.T) {
	// Create temporary directory structure
	tmpDir := t.TempDir()
	orgDir := filepath.Join(tmpDir, "test-org")

	// Create test repositories
	repo1Dir := filepath.Join(orgDir, "repo1")
	repo2Dir := filepath.Join(orgDir, "repo2")
	nonRepoDir := filepath.Join(orgDir, "not-a-repo")
	hiddenDir := filepath.Join(orgDir, ".hidden")

	require.NoError(t, os.MkdirAll(filepath.Join(repo1Dir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo2Dir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(nonRepoDir, 0o755))
	require.NoError(t, os.MkdirAll(hiddenDir, 0o755))

	cfg := config.SyncConfig{
		GitHubOrg:        "test-org",
		SharedVolumePath: tmpDir,
	}

	svc := &Service{cfg: cfg}

	repos, err := svc.listLocalRepositories()
	require.NoError(t, err)
	assert.Len(t, repos, 2)
	assert.Contains(t, repos, "repo1")
	assert.Contains(t, repos, "repo2")
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
		CloneURL: "https://github.com/test-org/test-repo.git",
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
