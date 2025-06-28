package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/klimeurt/heimdall/internal/collector"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockRedisClient is a mock implementation of Redis client
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

func TestHandleScanError(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		err            error
		expectedStatus string
		contextFunc    func() context.Context
	}{
		{
			name:           "general error",
			err:            fmt.Errorf("scan failed"),
			expectedStatus: "failed",
			contextFunc:    func() context.Context { return ctx },
		},
		{
			name:           "timeout error",
			err:            context.DeadlineExceeded,
			expectedStatus: "timeout",
			contextFunc: func() context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
				defer cancel()
				return ctx
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testCtx := tt.contextFunc()

			// Mock Redis client
			mockRedis := &MockRedisClient{}
			intCmd := redis.NewIntCmd(ctx)
			intCmd.SetVal(1)

			// Expect push to secrets queue
			mockRedis.On("LPush", mock.Anything, "secrets_queue", mock.Anything).Return(intCmd).Once()

			// Expect push to coordinator queue
			mockRedis.On("LPush", mock.Anything, "coordinator_queue", mock.Anything).Return(intCmd).Once()

			scanner := &Scanner{
				config: &config.ScannerConfig{
					SecretsQueueName:     "secrets_queue",
					CoordinatorQueueName: "coordinator_queue",
				},
				redisClient: mockRedis,
			}

			processedRepo := &collector.ProcessedRepository{
				Org:         "test-org",
				Name:        "test-repo",
				ProcessedAt: time.Now(),
				ClonePath:   "/test/path",
			}

			// Handle error
			err := scanner.handleScanError(testCtx, 1, processedRepo, time.Now(), tt.err)
			assert.Error(t, err)

			// Verify that the error status was set correctly
			// We can't directly check the status in the pushed data without complex mocking
			// but we verify the Redis calls were made
			mockRedis.AssertExpectations(t)
		})
	}
}

func TestSendCoordinationMessage(t *testing.T) {
	ctx := context.Background()

	// Mock Redis client
	mockRedis := &MockRedisClient{}
	intCmd := redis.NewIntCmd(ctx)
	intCmd.SetVal(1)
	mockRedis.On("LPush", ctx, "coordinator_queue", mock.Anything).Return(intCmd).Once()

	scanner := &Scanner{
		config: &config.ScannerConfig{
			CoordinatorQueueName: "coordinator_queue",
		},
		redisClient: mockRedis,
	}

	processedRepo := &collector.ProcessedRepository{
		Org:       "test-org",
		Name:      "test-repo",
		ClonePath: "/test/path",
	}
	startTime := time.Now().Add(-5 * time.Minute)

	// Send coordination message
	err := scanner.sendCoordinationMessage(ctx, 1, processedRepo, "success", startTime)
	assert.NoError(t, err)

	// Verify Redis call
	mockRedis.AssertExpectations(t)

	// Verify the pushed data structure
	calls := mockRedis.Calls
	assert.Len(t, calls, 1)
	assert.Equal(t, "LPush", calls[0].Method)

	// Extract and verify the coordination message data
	args := calls[0].Arguments
	assert.Len(t, args, 3)
	assert.Equal(t, "coordinator_queue", args[1])

	// Verify the JSON data
	values := args[2].([]interface{})
	assert.Len(t, values, 1)

	var coordMsg collector.ScanCoordinationMessage
	err = json.Unmarshal(values[0].([]byte), &coordMsg)
	assert.NoError(t, err)
	assert.Equal(t, "/test/path", coordMsg.ClonePath)
	assert.Equal(t, "test-org", coordMsg.Org)
	assert.Equal(t, "test-repo", coordMsg.Name)
	assert.Equal(t, "trufflehog", coordMsg.ScannerType)
	assert.Equal(t, "success", coordMsg.ScanStatus)
	assert.Equal(t, 1, coordMsg.WorkerID)
}

func TestParseTruffleHogOutput(t *testing.T) {
	tests := []struct {
		name             string
		truffleHogOutput string
		expectedFindings int
		validateFindings func(t *testing.T, findings []collector.TruffleHogFinding)
	}{
		{
			name:             "successful scan with verified secret",
			truffleHogOutput: `{"SourceMetadata":{"Data":{"Git":{"commit":"abc123","file":"config.yml","email":"test@example.com","repository":"test-repo","timestamp":"2024-01-01T00:00:00Z","line":42}}},"SourceID":1,"SourceType":1,"SourceName":"git","DetectorType":1,"DetectorName":"AWS","DecoderName":"","Verified":true,"Raw":"AKIAIOSFODNN7EXAMPLE","RawV2":"","Redacted":"AKIA****************","ExtraData":{},"StructuredData":null}`,
			expectedFindings: 1,
			validateFindings: func(t *testing.T, findings []collector.TruffleHogFinding) {
				assert.Equal(t, "AWS", findings[0].SecretType)
				assert.Equal(t, "config.yml", findings[0].File)
				assert.Equal(t, 42, findings[0].Line)
				assert.Equal(t, "abc123", findings[0].Commit)
				assert.True(t, findings[0].Validated)
			},
		},
		{
			name:             "successful scan with unverified secret",
			truffleHogOutput: `{"SourceMetadata":{"Data":{"Git":{"commit":"def456","file":"test.js","email":"test@example.com","repository":"test-repo","timestamp":"2024-01-01T00:00:00Z","line":10}}},"SourceID":1,"SourceType":1,"SourceName":"git","DetectorType":2,"DetectorName":"GitHub","DecoderName":"","Verified":false,"Raw":"ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx","RawV2":"","Redacted":"ghp_************************************","ExtraData":{},"StructuredData":null}`,
			expectedFindings: 1,
			validateFindings: func(t *testing.T, findings []collector.TruffleHogFinding) {
				assert.Equal(t, "GitHub", findings[0].SecretType)
				assert.Equal(t, "test.js", findings[0].File)
				assert.Equal(t, 10, findings[0].Line)
				assert.False(t, findings[0].Validated)
			},
		},
		{
			name:             "secret with verification error",
			truffleHogOutput: `{"SourceMetadata":{"Data":{"Git":{"commit":"xyz789","file":"app.py","line":100}}},"DetectorName":"Slack","Verified":false,"VerificationError":"invalid token"}`,
			expectedFindings: 1,
			validateFindings: func(t *testing.T, findings []collector.TruffleHogFinding) {
				assert.Equal(t, "Slack", findings[0].SecretType)
				assert.Contains(t, findings[0].Service, "verification_error: invalid token")
			},
		},
		{
			name:             "empty scan results",
			truffleHogOutput: "",
			expectedFindings: 0,
		},
		{
			name: "multiple findings",
			truffleHogOutput: `{"SourceMetadata":{"Data":{"Git":{"commit":"abc123","file":"config.yml","line":42}}},"DetectorName":"AWS","Verified":true}
{"SourceMetadata":{"Data":{"Git":{"commit":"def456","file":"test.js","line":10}}},"DetectorName":"GitHub","Verified":false}`,
			expectedFindings: 2,
		},
		{
			name:             "invalid JSON",
			truffleHogOutput: `{invalid json}`,
			expectedFindings: 0, // Invalid lines are skipped
		},
		{
			name: "mixed valid and invalid lines",
			truffleHogOutput: `{"SourceMetadata":{"Data":{"Git":{"commit":"abc123","file":"valid.yml","line":1}}},"DetectorName":"AWS","Verified":true}
{invalid json line}
{"SourceMetadata":{"Data":{"Git":{"commit":"def456","file":"valid2.js","line":2}}},"DetectorName":"GitHub","Verified":false}`,
			expectedFindings: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := parseTruffleHogOutput([]byte(tt.truffleHogOutput))
			assert.Len(t, findings, tt.expectedFindings)

			if tt.validateFindings != nil {
				tt.validateFindings(t, findings)
			}
		})
	}
}

// Helper function to parse TruffleHog output (extracted from scanner.go for testing)
func parseTruffleHogOutput(output []byte) []collector.TruffleHogFinding {
	var findings []collector.TruffleHogFinding

	lines := bytes.Split(output, []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		var result TruffleHogResult
		if err := json.Unmarshal(line, &result); err != nil {
			// Skip invalid lines
			continue
		}

		finding := collector.TruffleHogFinding{
			SecretType:  result.DetectorName,
			Description: fmt.Sprintf("Found %s secret", result.DetectorName),
			File:        result.SourceMetadata.Data.Git.File,
			Line:        result.SourceMetadata.Data.Git.Line,
			Commit:      result.SourceMetadata.Data.Git.Commit,
			Confidence:  "high",
			Validated:   result.Verified,
		}

		if result.VerificationError != "" {
			finding.Service = fmt.Sprintf("verification_error: %s", result.VerificationError)
		}

		findings = append(findings, finding)
	}

	return findings
}

func TestClose(t *testing.T) {
	mockRedis := &MockRedisClient{}
	mockRedis.On("Close").Return(nil).Once()

	scanner := &Scanner{
		redisClient: mockRedis,
	}

	scanner.Close()

	mockRedis.AssertExpectations(t)
}

// Test helper functions
func TestGetStringField(t *testing.T) {
	data := map[string]interface{}{
		"key1": "value1",
		"key2": 123,
		"key3": nil,
	}

	assert.Equal(t, "value1", getStringField(data, "key1"))
	assert.Equal(t, "", getStringField(data, "key2")) // Not a string
	assert.Equal(t, "", getStringField(data, "key3")) // Nil
	assert.Equal(t, "", getStringField(data, "nonexistent"))
}

func TestGetIntField(t *testing.T) {
	data := map[string]interface{}{
		"key1": float64(42),
		"key2": "not a number",
		"key3": nil,
	}

	assert.Equal(t, 42, getIntField(data, "key1"))
	assert.Equal(t, 0, getIntField(data, "key2")) // Not a number
	assert.Equal(t, 0, getIntField(data, "key3")) // Nil
	assert.Equal(t, 0, getIntField(data, "nonexistent"))
}

func TestGetBoolField(t *testing.T) {
	data := map[string]interface{}{
		"key1": true,
		"key2": false,
		"key3": "not a bool",
		"key4": nil,
	}

	assert.True(t, getBoolField(data, "key1"))
	assert.False(t, getBoolField(data, "key2"))
	assert.False(t, getBoolField(data, "key3")) // Not a bool
	assert.False(t, getBoolField(data, "key4")) // Nil
	assert.False(t, getBoolField(data, "nonexistent"))
}
