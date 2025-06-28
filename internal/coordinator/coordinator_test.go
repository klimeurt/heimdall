package coordinator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/klimeurt/heimdall/internal/collector"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockRedisClient is a mock implementation of redis.Client
type MockRedisClient struct {
	mock.Mock
}

func (m *MockRedisClient) Ping(ctx context.Context) *redis.StatusCmd {
	args := m.Called(ctx)
	return args.Get(0).(*redis.StatusCmd)
}

func (m *MockRedisClient) LLen(ctx context.Context, key string) *redis.IntCmd {
	args := m.Called(ctx, key)
	return args.Get(0).(*redis.IntCmd)
}

func (m *MockRedisClient) BRPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd {
	args := m.Called(ctx, timeout, keys)
	return args.Get(0).(*redis.StringSliceCmd)
}

func (m *MockRedisClient) LPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	args := m.Called(ctx, key, values)
	return args.Get(0).(*redis.IntCmd)
}

func (m *MockRedisClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

func TestProcessMessage_FirstScanner(t *testing.T) {
	// Create mock Redis client
	mockRedis := new(MockRedisClient)

	// Create coordinator with mock
	coord := &Coordinator{
		config: &config.CoordinatorConfig{
			CleanupQueueName: "cleanup_queue",
		},
		redisClient: mockRedis,
		state: &StateManager{
			jobs: make(map[string]*JobState),
		},
	}

	// Test data - first scanner completes
	msg := &collector.ScanCoordinationMessage{
		ClonePath:    "/shared/heimdall-repos/test_repo_123",
		Org:          "test-org",
		Name:         "test-repo",
		ScannerType:  "trufflehog",
		CompletedAt:  time.Now(),
		WorkerID:     1,
		ScanStatus:   "success",
		ScanDuration: 5 * time.Minute,
	}

	// Process message
	err := coord.processMessage(context.Background(), msg)

	// Assertions
	assert.NoError(t, err)

	// Check state was updated
	job, exists := coord.state.jobs[msg.ClonePath]
	assert.True(t, exists)
	assert.True(t, job.TruffleHogComplete)
	assert.False(t, job.OSVComplete)
	assert.Equal(t, "success", job.TruffleHogStatus)
	assert.Equal(t, msg.Org, job.Org)
	assert.Equal(t, msg.Name, job.Name)

	// No cleanup job should be sent yet
	mockRedis.AssertNotCalled(t, "LPush")
}

func TestProcessMessage_BothScannersComplete(t *testing.T) {
	// Create mock Redis client
	mockRedis := new(MockRedisClient)

	// Create coordinator with mock
	coord := &Coordinator{
		config: &config.CoordinatorConfig{
			CleanupQueueName: "cleanup_queue",
		},
		redisClient: mockRedis,
		state: &StateManager{
			jobs: make(map[string]*JobState),
		},
	}

	// Pre-populate state with first scanner complete
	clonePath := "/shared/heimdall-repos/test_repo_123"
	coord.state.jobs[clonePath] = &JobState{
		CoordinationState: collector.CoordinationState{
			ClonePath:          clonePath,
			Org:                "test-org",
			Name:               "test-repo",
			StartedAt:          time.Now().Add(-10 * time.Minute),
			TruffleHogComplete: true,
			TruffleHogStatus:   "success",
		},
		LastUpdated: time.Now().Add(-5 * time.Minute),
	}

	// Test data - second scanner completes
	msg := &collector.ScanCoordinationMessage{
		ClonePath:    clonePath,
		Org:          "test-org",
		Name:         "test-repo",
		ScannerType:  "osv",
		CompletedAt:  time.Now(),
		WorkerID:     2,
		ScanStatus:   "no_vulnerabilities",
		ScanDuration: 3 * time.Minute,
	}

	// Mock cleanup job send
	mockRedis.On("LPush", mock.Anything, "cleanup_queue", mock.MatchedBy(func(data []interface{}) bool {
		if len(data) != 1 {
			return false
		}

		// Unmarshal and check the cleanup job
		var cleanupJob collector.CleanupJob
		if err := json.Unmarshal(data[0].([]byte), &cleanupJob); err != nil {
			return false
		}

		return cleanupJob.ClonePath == clonePath &&
			cleanupJob.Org == "test-org" &&
			cleanupJob.Name == "test-repo"
	})).Return(redis.NewIntResult(1, nil))

	// Process message
	err := coord.processMessage(context.Background(), msg)

	// Assertions
	assert.NoError(t, err)

	// Check state was removed after completion
	_, exists := coord.state.jobs[clonePath]
	assert.False(t, exists)

	// Cleanup job should have been sent
	mockRedis.AssertExpectations(t)
}

func TestProcessMessage_UnknownScannerType(t *testing.T) {
	// Create coordinator
	coord := &Coordinator{
		config: &config.CoordinatorConfig{},
		state: &StateManager{
			jobs: make(map[string]*JobState),
		},
	}

	// Test data - unknown scanner type
	msg := &collector.ScanCoordinationMessage{
		ClonePath:   "/shared/heimdall-repos/test_repo_123",
		Org:         "test-org",
		Name:        "test-repo",
		ScannerType: "unknown-scanner",
		CompletedAt: time.Now(),
		WorkerID:    1,
		ScanStatus:  "success",
	}

	// Process message
	err := coord.processMessage(context.Background(), msg)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown scanner type")
}

func TestPerformCleanup_ExpiredJobs(t *testing.T) {
	// Create mock Redis client
	mockRedis := new(MockRedisClient)

	// Create coordinator with mock
	coord := &Coordinator{
		config: &config.CoordinatorConfig{
			CleanupQueueName:  "cleanup_queue",
			JobTimeoutMinutes: 60,
		},
		redisClient: mockRedis,
		state: &StateManager{
			jobs: make(map[string]*JobState),
		},
	}

	// Add expired job
	expiredPath := "/shared/heimdall-repos/expired_repo"
	coord.state.jobs[expiredPath] = &JobState{
		CoordinationState: collector.CoordinationState{
			ClonePath:          expiredPath,
			Org:                "test-org",
			Name:               "expired-repo",
			StartedAt:          time.Now().Add(-2 * time.Hour),
			TruffleHogComplete: true,
			TruffleHogStatus:   "success",
			OSVComplete:        false, // OSV never completed
		},
		LastUpdated: time.Now().Add(-90 * time.Minute), // Last updated 90 minutes ago
	}

	// Add active job (should not be cleaned up)
	activePath := "/shared/heimdall-repos/active_repo"
	coord.state.jobs[activePath] = &JobState{
		CoordinationState: collector.CoordinationState{
			ClonePath:          activePath,
			Org:                "test-org",
			Name:               "active-repo",
			StartedAt:          time.Now().Add(-30 * time.Minute),
			TruffleHogComplete: true,
			TruffleHogStatus:   "success",
			OSVComplete:        false,
		},
		LastUpdated: time.Now().Add(-5 * time.Minute), // Recently updated
	}

	// Mock cleanup job send for expired job
	mockRedis.On("LPush", mock.Anything, "cleanup_queue", mock.MatchedBy(func(data []interface{}) bool {
		if len(data) != 1 {
			return false
		}

		var cleanupJob collector.CleanupJob
		if err := json.Unmarshal(data[0].([]byte), &cleanupJob); err != nil {
			return false
		}

		return cleanupJob.ClonePath == expiredPath
	})).Return(redis.NewIntResult(1, nil))

	// Perform cleanup
	coord.performCleanup(context.Background())

	// Assertions
	assert.Equal(t, 1, len(coord.state.jobs)) // Only active job remains
	_, hasActive := coord.state.jobs[activePath]
	assert.True(t, hasActive)
	_, hasExpired := coord.state.jobs[expiredPath]
	assert.False(t, hasExpired)

	mockRedis.AssertExpectations(t)
}

func TestGetActiveJobs(t *testing.T) {
	// Create coordinator
	coord := &Coordinator{
		state: &StateManager{
			jobs: make(map[string]*JobState),
		},
	}

	// Add some jobs
	coord.state.jobs["/path1"] = &JobState{
		CoordinationState: collector.CoordinationState{
			ClonePath:          "/path1",
			Org:                "org1",
			Name:               "repo1",
			TruffleHogComplete: true,
			OSVComplete:        false,
		},
	}

	coord.state.jobs["/path2"] = &JobState{
		CoordinationState: collector.CoordinationState{
			ClonePath:          "/path2",
			Org:                "org2",
			Name:               "repo2",
			TruffleHogComplete: false,
			OSVComplete:        true,
		},
	}

	// Get active jobs
	jobs := coord.GetActiveJobs()

	// Assertions
	assert.Equal(t, 2, len(jobs))

	// Check that we got copies, not references
	for _, job := range jobs {
		if job.ClonePath == "/path1" {
			job.Org = "modified"
			// Original should not be modified
			assert.Equal(t, "org1", coord.state.jobs["/path1"].Org)
		}
	}
}

func TestSendCleanupJob(t *testing.T) {
	// Create mock Redis client
	mockRedis := new(MockRedisClient)

	// Create coordinator with mock
	coord := &Coordinator{
		config: &config.CoordinatorConfig{
			CleanupQueueName: "cleanup_queue",
		},
		redisClient: mockRedis,
	}

	// Test data
	job := &JobState{
		CoordinationState: collector.CoordinationState{
			ClonePath: "/shared/heimdall-repos/test_repo_123",
			Org:       "test-org",
			Name:      "test-repo",
		},
	}

	// Mock LPush
	mockRedis.On("LPush", mock.Anything, "cleanup_queue", mock.MatchedBy(func(data []interface{}) bool {
		if len(data) != 1 {
			return false
		}

		var cleanupJob collector.CleanupJob
		if err := json.Unmarshal(data[0].([]byte), &cleanupJob); err != nil {
			return false
		}

		return cleanupJob.ClonePath == job.ClonePath &&
			cleanupJob.Org == job.Org &&
			cleanupJob.Name == job.Name &&
			cleanupJob.WorkerID == 0 // Coordinator uses worker ID 0
	})).Return(redis.NewIntResult(1, nil))

	// Test
	err := coord.sendCleanupJob(context.Background(), job)

	// Assertions
	assert.NoError(t, err)
	mockRedis.AssertExpectations(t)
}
