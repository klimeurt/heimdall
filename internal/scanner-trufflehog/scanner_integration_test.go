//go:build integration
// +build integration

package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/klimeurt/heimdall/internal/types"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScannerIntegration(t *testing.T) {
	// Skip if Redis is not available
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15, // Use a different DB for testing
	})

	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available for integration tests")
	}
	defer redisClient.Close()

	// Clean up test queues
	redisClient.Del(ctx, "test_trufflehog_queue", "test_trufflehog_results_queue")

	// Create test configuration
	cfg := &config.ScannerConfig{
		RedisHost:              "localhost",
		RedisPort:              "6379",
		RedisDB:                15,
		ProcessedQueueName:     "test_trufflehog_queue",
		SecretsQueueName:       "test_trufflehog_results_queue",
		MaxConcurrentScans:     1,
		ScanTimeout:            30 * time.Second,
		TruffleHogConcurrency:  1,
		TruffleHogOnlyVerified: false,
	}

	// Check if trufflehog is available
	if _, err := exec.LookPath("trufflehog"); err != nil {
		t.Skip("TruffleHog not available for integration tests")
	}

	t.Run("full scan workflow", func(t *testing.T) {
		// Create scanner
		scanner, err := New(cfg)
		require.NoError(t, err)
		defer scanner.Close()

		// Create a test repository with a fake secret
		tmpDir, err := os.MkdirTemp("", "scanner-integration-test-*")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		// Initialize git repo
		gitDir := filepath.Join(tmpDir, ".git")
		require.NoError(t, os.MkdirAll(gitDir, 0755))

		// Create a file with a fake secret pattern
		secretFile := filepath.Join(tmpDir, "config.yml")
		secretContent := `# Test config
api_key: test_AKIAIOSFODNN7EXAMPLE_test
database_password: supersecret123!
`
		require.NoError(t, os.WriteFile(secretFile, []byte(secretContent), 0644))

		// Create processed repository data
		processedRepo := &types.ProcessedRepository{
			Org:         "test-org",
			Name:        "test-repo",
			ProcessedAt: time.Now(),
			ClonePath:   tmpDir,
		}

		// Push to processed queue
		processedData, err := json.Marshal(processedRepo)
		require.NoError(t, err)
		err = redisClient.LPush(ctx, cfg.ProcessedQueueName, processedData).Err()
		require.NoError(t, err)

		// Start scanner in a goroutine
		scanCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		done := make(chan bool)
		go func() {
			scanner.Start(scanCtx)
			done <- true
		}()

		// Wait for scan to complete
		time.Sleep(2 * time.Second)

		// Check if secrets were pushed to queue
		secretsLen, err := redisClient.LLen(ctx, cfg.SecretsQueueName).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(1), secretsLen)

		// Verify the scanned data
		scannedData, err := redisClient.RPop(ctx, cfg.SecretsQueueName).Result()
		require.NoError(t, err)

		var scannedRepo types.ScannedRepository
		err = json.Unmarshal([]byte(scannedData), &scannedRepo)
		require.NoError(t, err)

		assert.Equal(t, "test-org", scannedRepo.Org)
		assert.Equal(t, "test-repo", scannedRepo.Name)
		assert.NotEmpty(t, scannedRepo.ScannedAt)


		// Cancel context to stop scanner
		cancel()
		<-done
	})
}

func TestScannerWorkerConcurrency(t *testing.T) {
	// Skip if Redis is not available
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})

	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available for integration tests")
	}
	defer redisClient.Close()

	// Clean up test queues
	redisClient.Del(ctx, "test_trufflehog_queue", "test_trufflehog_results_queue")

	// Create test configuration with multiple workers
	cfg := &config.ScannerConfig{
		RedisHost:              "localhost",
		RedisPort:              "6379",
		RedisDB:                15,
		ProcessedQueueName:     "test_trufflehog_queue",
		SecretsQueueName:       "test_trufflehog_results_queue",
		MaxConcurrentScans:     3,
		ScanTimeout:            30 * time.Second,
		TruffleHogConcurrency:  1,
		TruffleHogOnlyVerified: false,
	}

	// Create scanner
	scanner, err := New(cfg)
	require.NoError(t, err)
	defer scanner.Close()

	// Push multiple items to queue
	for i := 0; i < 5; i++ {
		processedRepo := &types.ProcessedRepository{
			Org:         "test-org",
			Name:        fmt.Sprintf("test-repo-%d", i),
			ProcessedAt: time.Now(),
			ClonePath:   fmt.Sprintf("/nonexistent/path-%d", i), // Will cause scan to fail quickly
		}

		processedData, err := json.Marshal(processedRepo)
		require.NoError(t, err)
		err = redisClient.LPush(ctx, cfg.ProcessedQueueName, processedData).Err()
		require.NoError(t, err)
	}

	// Start scanner with timeout
	scanCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	done := make(chan bool)
	go func() {
		scanner.Start(scanCtx)
		done <- true
	}()

	// Wait for processing
	time.Sleep(2 * time.Second)

	// All items should be processed (even if they failed)
	processedLen, err := redisClient.LLen(ctx, cfg.ProcessedQueueName).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), processedLen)

	// Should have 5 items in secrets queue (all failed scans)
	secretsLen, err := redisClient.LLen(ctx, cfg.SecretsQueueName).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(5), secretsLen)


	cancel()
	<-done
}
