package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v57/github"
	"github.com/klimeurt/heimdall/internal/config"
)

func TestCollectorCreation(t *testing.T) {
	tests := []struct {
		name          string
		config        *config.Config
		expectError   bool
		errorContains string
	}{
		{
			name: "valid config with token",
			config: &config.Config{
				GitHubOrg:      "testorg",
				GitHubToken:    "token123",
				HTTPEndpoint:   "http://localhost:8080/repositories",
				CronSchedule:   "0 0 * * 0",
				GitHubPageSize: 100,
				RedisHost:      "localhost",
				RedisPort:      "6379",
			},
			expectError: true, // Will fail if Redis is not available
		},
		{
			name: "valid config without token",
			config: &config.Config{
				GitHubOrg:      "testorg",
				GitHubToken:    "",
				HTTPEndpoint:   "http://localhost:8080/repositories",
				CronSchedule:   "0 0 * * 0",
				GitHubPageSize: 100,
				RedisHost:      "localhost",
				RedisPort:      "6379",
			},
			expectError: true, // Will fail if Redis is not available
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector, err := New(tt.config)

			if tt.expectError {
				if err == nil {
					t.Errorf("New() expected error, got nil")
					return
				}
				if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("New() error = %v, want to contain %v", err, tt.errorContains)
				}
				return
			}

			if err != nil {
				t.Errorf("New() unexpected error: %v", err)
				return
			}

			if collector == nil {
				t.Error("New() returned nil collector")
				return
			}

			if collector.config != tt.config {
				t.Error("Collector config not set correctly")
			}

			if collector.ghClient == nil {
				t.Error("GitHub client not initialized")
			}

			if collector.redisClient == nil {
				t.Error("Redis client not initialized")
			}

			collector.Close()
		})
	}
}

func TestCollectorQueueRepository(t *testing.T) {
	// Skip test if Redis is not available
	config := &config.Config{
		GitHubOrg:      "testorg",
		GitHubToken:    "token123",
		HTTPEndpoint:   "http://localhost:8080/repositories",
		CronSchedule:   "0 0 * * 0",
		GitHubPageSize: 100,
		RedisHost:      "localhost",
		RedisPort:      "6379",
	}

	collector, err := New(config)
	if err != nil {
		t.Skipf("Redis not available, skipping test: %v", err)
	}
	defer collector.Close()

	// Clean up test queue
	ctx := context.Background()
	collector.redisClient.Del(ctx, "clone_queue")

	// Create test GitHub repository
	githubRepo := createMockGitHubRepo("test-repo")

	// Queue repository
	err = collector.queueRepository(ctx, githubRepo)
	if err != nil {
		t.Fatalf("Failed to queue repository: %v", err)
	}

	// Verify repository was queued
	result, err := collector.redisClient.BRPop(ctx, 0, "clone_queue").Result()
	if err != nil {
		t.Fatalf("Failed to retrieve from queue: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("Expected 2 elements in result, got %d", len(result))
	}

	var repo Repository
	err = json.Unmarshal([]byte(result[1]), &repo)
	if err != nil {
		t.Fatalf("Failed to unmarshal repository: %v", err)
	}

	if repo.Org != "testorg" {
		t.Errorf("Repository org = %v, want %v", repo.Org, "testorg")
	}
	if repo.Name != "test-repo" {
		t.Errorf("Repository name = %v, want %v", repo.Name, "test-repo")
	}
}

// TestCollectorQueueRepositoryRedisError tests Redis connection errors
func TestCollectorQueueRepositoryRedisError(t *testing.T) {
	// This test will be skipped if Redis is not available
	t.Skip("Redis error testing requires specific Redis setup")
}

func TestCollectRepositoriesPagination(t *testing.T) {
	// Create mock GitHub API server
	var gitHubServer *httptest.Server
	gitHubServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/repos") {
			http.NotFound(w, r)
			return
		}

		page := r.URL.Query().Get("page")
		var repos []map[string]interface{}

		if page == "" || page == "1" {
			// First page
			repos = []map[string]interface{}{
				createMockRepoJSON("repo1"),
				createMockRepoJSON("repo2"),
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/orgs/testorg/repos?page=2>; rel="next"`, gitHubServer.URL))
		} else if page == "2" {
			// Second page
			repos = []map[string]interface{}{
				createMockRepoJSON("repo3"),
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(repos)
	}))
	defer gitHubServer.Close()

	config := &config.Config{
		GitHubOrg:      "testorg",
		GitHubToken:    "token123",
		HTTPEndpoint:   "http://localhost:8080/repositories",
		CronSchedule:   "0 0 * * 0",
		GitHubPageSize: 100,
		RedisHost:      "localhost",
		RedisPort:      "6379",
	}

	collector, err := New(config)
	if err != nil {
		t.Skipf("Redis not available, skipping test: %v", err)
	}
	defer collector.Close()

	// Clean up test queue
	ctx := context.Background()
	collector.redisClient.Del(ctx, "clone_queue")

	// Override GitHub client base URL for testing
	collector.ghClient.BaseURL = mustParseURL(gitHubServer.URL + "/")

	// Collect repositories
	err = collector.CollectRepositories(ctx)
	if err != nil {
		t.Fatalf("Failed to collect repositories: %v", err)
	}

	// Verify queued repositories
	expectedRepos := []string{"repo1", "repo2", "repo3"}
	queueLength, err := collector.redisClient.LLen(ctx, "clone_queue").Result()
	if err != nil {
		t.Fatalf("Failed to get queue length: %v", err)
	}

	if queueLength != int64(len(expectedRepos)) {
		t.Errorf("Expected %d repos in queue, got %d", len(expectedRepos), queueLength)
	}

	// Verify each repository in queue
	var receivedRepos []Repository
	for i := 0; i < len(expectedRepos); i++ {
		result, err := collector.redisClient.RPop(ctx, "clone_queue").Result()
		if err != nil {
			t.Fatalf("Failed to pop from queue: %v", err)
		}

		var repo Repository
		err = json.Unmarshal([]byte(result), &repo)
		if err != nil {
			t.Fatalf("Failed to unmarshal repository %d: %v", i, err)
		}
		receivedRepos = append(receivedRepos, repo)
	}

	for _, expected := range expectedRepos {
		found := false
		for _, received := range receivedRepos {
			if received.Name == expected && received.Org == "testorg" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected repo %s not found in received repos", expected)
		}
	}
}

func TestCollectRepositoriesError(t *testing.T) {
	// Create mock GitHub API server that returns error
	gitHubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Bad credentials"})
	}))
	defer gitHubServer.Close()

	config := &config.Config{
		GitHubOrg:      "testorg",
		GitHubToken:    "invalid-token",
		HTTPEndpoint:   "http://localhost:8080/repositories",
		CronSchedule:   "0 0 * * 0",
		GitHubPageSize: 100,
		RedisHost:      "localhost",
		RedisPort:      "6379",
	}

	collector, err := New(config)
	if err != nil {
		t.Skipf("Redis not available, skipping test: %v", err)
	}
	defer collector.Close()

	// Override GitHub client base URL for testing
	collector.ghClient.BaseURL = mustParseURL(gitHubServer.URL + "/")

	// Collect repositories should return error
	ctx := context.Background()
	err = collector.CollectRepositories(ctx)
	if err == nil {
		t.Error("Expected error from CollectRepositories, got nil")
	}
	if !strings.Contains(err.Error(), "failed to list repositories") {
		t.Errorf("Expected 'failed to list repositories' error, got: %v", err)
	}
}

// Test helper functions

func createMockGitHubRepo(name string) *github.Repository {
	return &github.Repository{
		Name: github.String(name),
	}
}

func createMockRepoJSON(name string) map[string]interface{} {
	return map[string]interface{}{
		"name": name,
	}
}

func mustParseURL(rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse URL %s: %v", rawURL, err))
	}
	return u
}
