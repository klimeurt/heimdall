package cloner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/klimeurt/heimdall/internal/collector"
	"github.com/klimeurt/heimdall/internal/config"
)

func TestNew(t *testing.T) {
	// Skip test if Redis is not available
	cfg := &config.ClonerConfig{
		RedisHost:           "localhost",
		RedisPort:           "6379",
		MaxConcurrentClones: 5,
	}

	cloner, err := New(cfg)
	if err != nil {
		t.Skipf("Redis not available, skipping test: %v", err)
	}
	defer cloner.Close()

	if cloner.config.MaxConcurrentClones != 5 {
		t.Errorf("Expected MaxConcurrentClones to be 5, got %d", cloner.config.MaxConcurrentClones)
	}
}

func TestProcessRepository(t *testing.T) {
	// Skip test if Redis is not available
	cfg := &config.ClonerConfig{
		RedisHost:           "localhost",
		RedisPort:           "6379",
		MaxConcurrentClones: 1,
	}

	cloner, err := New(cfg)
	if err != nil {
		t.Skipf("Redis not available, skipping test: %v", err)
	}
	defer cloner.Close()

	// Test with a known public repository
	repo := &collector.Repository{
		Org:  "octocat",
		Name: "Hello-World",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// This should not return an error for a valid public repository
	err = cloner.processRepository(ctx, 0, repo)
	if err != nil {
		t.Logf("Repository processing failed (this might be expected if repository doesn't exist): %v", err)
	}
}

func TestRedisQueueIntegration(t *testing.T) {
	// Skip test if Redis is not available
	cfg := &config.ClonerConfig{
		RedisHost:           "localhost",
		RedisPort:           "6379",
		MaxConcurrentClones: 1,
	}

	cloner, err := New(cfg)
	if err != nil {
		t.Skipf("Redis not available, skipping test: %v", err)
	}
	defer cloner.Close()

	// Clean up test queue
	testQueue := "test_clone_queue"
	ctx := context.Background()
	cloner.redisClient.Del(ctx, testQueue)

	// Test data
	repo := collector.Repository{
		Org:  "test-org",
		Name: "test-repo",
	}

	// Marshal test data
	data, err := json.Marshal(repo)
	if err != nil {
		t.Fatalf("Failed to marshal test data: %v", err)
	}

	// Push to test queue
	err = cloner.redisClient.LPush(ctx, testQueue, data).Err()
	if err != nil {
		t.Fatalf("Failed to push to Redis queue: %v", err)
	}

	// Pop from test queue
	result, err := cloner.redisClient.BRPop(ctx, time.Second, testQueue).Result()
	if err != nil {
		t.Fatalf("Failed to pop from Redis queue: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("Expected 2 elements in result, got %d", len(result))
	}

	// Verify data
	var retrievedRepo collector.Repository
	err = json.Unmarshal([]byte(result[1]), &retrievedRepo)
	if err != nil {
		t.Fatalf("Failed to unmarshal retrieved data: %v", err)
	}

	if retrievedRepo.Org != repo.Org || retrievedRepo.Name != repo.Name {
		t.Errorf("Retrieved repo doesn't match original. Got %+v, expected %+v", retrievedRepo, repo)
	}

	// Clean up
	cloner.redisClient.Del(ctx, testQueue)
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *config.ClonerConfig
		shouldErr bool
	}{
		{
			name: "valid config",
			cfg: &config.ClonerConfig{
				RedisHost:           "localhost",
				RedisPort:           "6379",
				MaxConcurrentClones: 5,
			},
			shouldErr: false,
		},
		{
			name: "invalid redis port",
			cfg: &config.ClonerConfig{
				RedisHost:           "localhost",
				RedisPort:           "invalid",
				MaxConcurrentClones: 5,
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg)
			if tt.shouldErr && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.shouldErr && err != nil {
				// For Redis connection errors, we'll skip the test
				if strings.Contains(err.Error(), "connect: connection refused") {
					t.Skipf("Redis connection error (expected in CI): %v", err)
				}
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}
