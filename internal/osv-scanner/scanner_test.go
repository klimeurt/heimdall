package osvscanner

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/klimeurt/heimdall/internal/collector"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockRedisClient is a mock implementation of RedisClient interface
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

func TestSendCoordinationMessage(t *testing.T) {
	// Create mock Redis client
	mockRedis := new(MockRedisClient)

	// Create scanner with mock
	scanner := &Scanner{
		config: &config.OSVScannerConfig{
			CoordinatorQueueName: "coordinator_queue",
		},
		redisClient: mockRedis,
	}

	// Test data
	processedRepo := &collector.ProcessedRepository{
		ClonePath: "/shared/heimdall-repos/test_repo_123",
		Org:       "test-org",
		Name:      "test-repo",
	}
	startTime := time.Now().Add(-5 * time.Minute)

	// Expected coordination message
	expectedMsg := &collector.ScanCoordinationMessage{
		ClonePath:    processedRepo.ClonePath,
		Org:          processedRepo.Org,
		Name:         processedRepo.Name,
		ScannerType:  "osv",
		CompletedAt:  time.Now(),
		WorkerID:     1,
		ScanStatus:   "success",
		ScanDuration: time.Since(startTime),
	}

	// Mock LPush call
	mockRedis.On("LPush", mock.Anything, "coordinator_queue", mock.MatchedBy(func(data []interface{}) bool {
		if len(data) != 1 {
			return false
		}

		// Unmarshal and check the message
		var msg collector.ScanCoordinationMessage
		if err := json.Unmarshal(data[0].([]byte), &msg); err != nil {
			return false
		}

		return msg.ClonePath == expectedMsg.ClonePath &&
			msg.Org == expectedMsg.Org &&
			msg.Name == expectedMsg.Name &&
			msg.ScannerType == "osv" &&
			msg.WorkerID == 1 &&
			msg.ScanStatus == "success"
	})).Return(redis.NewIntResult(1, nil))

	// Test
	err := scanner.sendCoordinationMessage(context.Background(), 1, processedRepo, "success", startTime)

	// Assertions
	assert.NoError(t, err)
	mockRedis.AssertExpectations(t)
}

func TestHandleScanError(t *testing.T) {
	// Create mock Redis client
	mockRedis := new(MockRedisClient)

	// Create scanner with mock
	scanner := &Scanner{
		config: &config.OSVScannerConfig{
			OSVResultsQueueName:  "osv_results_queue",
			CoordinatorQueueName: "coordinator_queue",
		},
		redisClient: mockRedis,
	}

	// Test data
	processedRepo := &collector.ProcessedRepository{
		ClonePath:   "/shared/heimdall-repos/test_repo_123",
		Org:         "test-org",
		Name:        "test-repo",
		ProcessedAt: time.Now().Add(-10 * time.Minute),
	}
	startTime := time.Now().Add(-5 * time.Minute)
	scanError := errors.New("scan failed")

	// Mock LPush calls
	mockRedis.On("LPush", mock.Anything, "osv_results_queue", mock.MatchedBy(func(data []interface{}) bool {
		if len(data) != 1 {
			return false
		}

		// Unmarshal and check the scanned repo
		var scannedRepo collector.OSVScannedRepository
		if err := json.Unmarshal(data[0].([]byte), &scannedRepo); err != nil {
			return false
		}

		return scannedRepo.Org == processedRepo.Org &&
			scannedRepo.Name == processedRepo.Name &&
			scannedRepo.ScanStatus == "failed" &&
			scannedRepo.VulnerabilitiesFound == 0 &&
			scannedRepo.ErrorMessage == scanError.Error()
	})).Return(redis.NewIntResult(1, nil))

	mockRedis.On("LPush", mock.Anything, "coordinator_queue", mock.Anything).Return(redis.NewIntResult(1, nil))

	// Test
	err := scanner.handleScanError(context.Background(), 1, processedRepo, startTime, scanError)

	// Assertions
	assert.Error(t, err)
	assert.Equal(t, scanError, err)
	mockRedis.AssertExpectations(t)
}

func TestParseOSVOutput(t *testing.T) {
	tests := []struct {
		name           string
		osvOutput      OSVOutput
		expectedCount  int
		checkFirstVuln func(t *testing.T, vuln collector.OSVScanResult)
	}{
		{
			name: "single vulnerability",
			osvOutput: OSVOutput{
				Results: []OSVResult{
					{
						Source: struct {
							Path string `json:"path"`
							Type string `json:"type"`
						}{
							Path: "package.json",
							Type: "lockfile",
						},
						Packages: []OSVPackage{
							{
								Package: struct {
									Name      string `json:"name"`
									Version   string `json:"version"`
									Ecosystem string `json:"ecosystem"`
								}{
									Name:      "lodash",
									Version:   "4.17.19",
									Ecosystem: "npm",
								},
								Vulnerabilities: []OSVVulnerability{
									{
										ID:      "GHSA-p6mc-m468-83gw",
										Summary: "Prototype Pollution in lodash",
										Details: "Lodash versions prior to 4.17.21 are vulnerable to Prototype Pollution",
										Aliases: []string{"CVE-2020-8203"},
										CWE:     []string{"CWE-1321"},
										Severity: []struct {
											Type  string `json:"type"`
											Score string `json:"score"`
										}{
											{Type: "CVSS_V3", Score: "7.4"},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedCount: 1,
			checkFirstVuln: func(t *testing.T, vuln collector.OSVScanResult) {
				assert.Equal(t, "GHSA-p6mc-m468-83gw", vuln.ID)
				assert.Equal(t, "lodash", vuln.Package)
				assert.Equal(t, "4.17.19", vuln.Version)
				assert.Equal(t, "npm", vuln.Ecosystem)
				assert.Equal(t, "7.4", vuln.Severity)
				assert.Equal(t, "CVE-2020-8203", vuln.CVE)
				assert.Equal(t, []string{"CWE-1321"}, vuln.CWE)
				assert.Equal(t, "package.json", vuln.PackageFile)
			},
		},
		{
			name: "multiple vulnerabilities",
			osvOutput: OSVOutput{
				Results: []OSVResult{
					{
						Source: struct {
							Path string `json:"path"`
							Type string `json:"type"`
						}{
							Path: "go.mod",
							Type: "lockfile",
						},
						Packages: []OSVPackage{
							{
								Package: struct {
									Name      string `json:"name"`
									Version   string `json:"version"`
									Ecosystem string `json:"ecosystem"`
								}{
									Name:      "github.com/gin-gonic/gin",
									Version:   "v1.6.3",
									Ecosystem: "Go",
								},
								Vulnerabilities: []OSVVulnerability{
									{
										ID:      "GO-2020-0001",
										Summary: "HTTP Request Smuggling",
										Details: "HTTP Request Smuggling vulnerability",
										Aliases: []string{},
										CWE:     []string{"CWE-444"},
									},
									{
										ID:      "GO-2020-0002",
										Summary: "Denial of Service",
										Details: "DoS vulnerability",
										Aliases: []string{"CVE-2020-1234"},
										CWE:     []string{"CWE-400"},
									},
								},
							},
						},
					},
				},
			},
			expectedCount: 2,
			checkFirstVuln: func(t *testing.T, vuln collector.OSVScanResult) {
				assert.Equal(t, "GO-2020-0001", vuln.ID)
				assert.Equal(t, "github.com/gin-gonic/gin", vuln.Package)
				assert.Equal(t, "v1.6.3", vuln.Version)
				assert.Equal(t, "Go", vuln.Ecosystem)
			},
		},
		{
			name:          "no vulnerabilities",
			osvOutput:     OSVOutput{Results: []OSVResult{}},
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert test output to findings
			var findings []collector.OSVScanResult
			for _, result := range tt.osvOutput.Results {
				for _, pkg := range result.Packages {
					for _, vuln := range pkg.Vulnerabilities {
						finding := collector.OSVScanResult{
							ID:          vuln.ID,
							Package:     pkg.Package.Name,
							Version:     pkg.Package.Version,
							Ecosystem:   pkg.Package.Ecosystem,
							Summary:     vuln.Summary,
							Details:     vuln.Details,
							Aliases:     vuln.Aliases,
							PackageFile: result.Source.Path,
							CWE:         vuln.CWE,
						}

						// Extract severity
						if len(vuln.Severity) > 0 {
							finding.Severity = vuln.Severity[0].Score
						}

						// Extract CVE if present in aliases
						for _, alias := range vuln.Aliases {
							if len(alias) > 3 && alias[:3] == "CVE" {
								finding.CVE = alias
								break
							}
						}

						findings = append(findings, finding)
					}
				}
			}

			// Assertions
			assert.Equal(t, tt.expectedCount, len(findings))
			if tt.expectedCount > 0 && tt.checkFirstVuln != nil {
				tt.checkFirstVuln(t, findings[0])
			}
		})
	}
}
