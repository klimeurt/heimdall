package scanner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/klimeurt/heimdall/internal/types"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/klimeurt/heimdall/internal/logging"
	"github.com/redis/go-redis/v9"
)

// Scanner handles the repository secret scanning operations
type Scanner struct {
	config      *config.ScannerConfig
	redisClient RedisClient
	auth        *http.BasicAuth
	logger      *slog.Logger
}

// RedisClient interface for Redis operations (allows mocking in tests)
type RedisClient interface {
	Ping(ctx context.Context) *redis.StatusCmd
	LLen(ctx context.Context, key string) *redis.IntCmd
	BRPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd
	LPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	Close() error
}

// New creates a new Scanner instance
func New(cfg *config.ScannerConfig, logger *slog.Logger) (*Scanner, error) {
	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort),
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	// Test Redis connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	// Set up GitHub authentication if token is provided
	var auth *http.BasicAuth
	if cfg.GitHubToken != "" {
		auth = &http.BasicAuth{
			Username: "token", // GitHub uses "token" as username for personal access tokens
			Password: cfg.GitHubToken,
		}
	}

	// Verify TruffleHog binary exists
	if _, err := exec.LookPath("trufflehog"); err != nil {
		return nil, fmt.Errorf("trufflehog binary not found in PATH: %w", err)
	}

	return &Scanner{
		config:      cfg,
		redisClient: redisClient,
		auth:        auth,
		logger:      logger,
	}, nil
}

// Start begins the scanner workers
func (s *Scanner) Start(ctx context.Context) error {
	// Log queue length at startup
	queueLen, err := s.redisClient.LLen(ctx, s.config.ProcessedQueueName).Result()
	if err != nil {
		s.logger.Error("failed to get queue length at startup", slog.String("error", err.Error()))
	} else {
		s.logger.Info("queue length at startup",
			slog.Int64("items", queueLen),
			slog.String("queue", s.config.ProcessedQueueName))
	}

	s.logger.Info("starting scanner workers",
		slog.Int("worker_count", s.config.MaxConcurrentScans))

	var wg sync.WaitGroup

	// Start worker goroutines
	for i := 0; i < s.config.MaxConcurrentScans; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			s.worker(ctx, workerID)
		}(i)
	}

	// Wait for all workers to complete
	wg.Wait()
	s.logger.Info("all scanner workers have stopped")
	return nil
}

// worker processes repository scan jobs from the Redis queue
func (s *Scanner) worker(ctx context.Context, workerID int) {
	ctx = logging.WithWorker(ctx, workerID)
	logger := logging.LoggerFromContext(ctx, s.logger)
	logger.Info("scanner worker started")
	defer logger.Info("scanner worker stopped")

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Block until a job is available or context is cancelled
			result, err := s.redisClient.BRPop(ctx, 0, s.config.ProcessedQueueName).Result()
			if err != nil {
				if err == redis.Nil {
					continue // No items in queue, keep waiting
				}
				if ctx.Err() != nil {
					return // Context cancelled
				}
				logger.Error("Redis error", slog.String("error", err.Error()))
				continue
			}

			// result[0] is the queue name, result[1] is the data
			if len(result) < 2 {
				logger.Error("invalid Redis result")
				continue
			}

			// Parse processed repository data
			var processedRepo types.ProcessedRepository
			if err := json.Unmarshal([]byte(result[1]), &processedRepo); err != nil {
				logger.Error("failed to parse processed repository data", slog.String("error", err.Error()))
				continue
			}

			// Scan the repository for secrets
			if err := s.scanRepository(ctx, workerID, &processedRepo); err != nil {
				logger.Error("failed to scan repository",
					slog.String("org", processedRepo.Org),
					slog.String("repo", processedRepo.Name),
					slog.String("error", err.Error()))
			}

			// Log queue length after job execution
			queueLen, err := s.redisClient.LLen(ctx, s.config.ProcessedQueueName).Result()
			if err != nil {
				logger.Error("failed to get queue length after job", slog.String("error", err.Error()))
			} else {
				logger.Debug("queue length after job",
					slog.Int64("items", queueLen),
					slog.String("queue", s.config.ProcessedQueueName))
			}
		}
	}
}

// scanRepository scans a repository from shared volume with TruffleHog
func (s *Scanner) scanRepository(ctx context.Context, workerID int, processedRepo *types.ProcessedRepository) error {
	startTime := time.Now()
	logger := logging.LoggerFromContext(ctx, s.logger)
	logger.Info("scanning repository",
		slog.String("org", processedRepo.Org),
		slog.String("repo", processedRepo.Name),
		slog.String("path", processedRepo.ClonePath))

	// Use the clone path from the processed repository
	repoDir := processedRepo.ClonePath

	// Validate that the path exists
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		err := fmt.Errorf("clone path does not exist: %s", repoDir)
		return s.handleScanError(ctx, workerID, processedRepo, startTime, err)
	}

	// Run TruffleHog scan with timeout
	scanCtx, scanCancel := context.WithTimeout(ctx, s.config.ScanTimeout)
	defer scanCancel()

	findings, err := s.runTruffleHogScan(scanCtx, repoDir)
	if err != nil {
		return s.handleScanError(ctx, workerID, processedRepo, startTime, fmt.Errorf("trufflehog scan failed: %w", err))
	}

	// Create scanned repository data
	scannedRepo := &types.ScannedRepository{
		Org:               processedRepo.Org,
		Name:              processedRepo.Name,
		ProcessedAt:       processedRepo.ProcessedAt,
		ScannedAt:         time.Now(),
		WorkerID:          workerID,
		ValidSecretsFound: len(findings),
		ValidSecrets:      findings,
		ScanStatus:        "success",
		ScanDuration:      time.Since(startTime),
	}

	if len(findings) == 0 {
		scannedRepo.ScanStatus = "no_secrets"
	}

	// Marshal to JSON
	scannedData, err := json.Marshal(scannedRepo)
	if err != nil {
		return fmt.Errorf("failed to marshal scanned repository data: %w", err)
	}

	// Push to secrets queue
	if err := s.redisClient.LPush(ctx, s.config.SecretsQueueName, scannedData).Err(); err != nil {
		return fmt.Errorf("failed to push scanned repository to queue: %w", err)
	}

	// Coordination message removed - no longer using coordinator service

	logger.Info("repository scanned successfully",
		slog.String("org", processedRepo.Org),
		slog.String("repo", processedRepo.Name),
		slog.Int("valid_secrets_found", len(findings)),
		slog.Int64("duration_ms", time.Since(startTime).Milliseconds()))

	return nil
}

// runTruffleHogScan executes TruffleHog and parses results
func (s *Scanner) runTruffleHogScan(ctx context.Context, repoDir string) ([]types.TruffleHogFinding, error) {
	// Create temporary file for JSON output
	outputFile := filepath.Join(repoDir, "trufflehog_results.json")
	defer os.Remove(outputFile)

	// Build TruffleHog command
	args := []string{
		"git",
		fmt.Sprintf("file://%s", repoDir),
		"--json",
		"--concurrency", fmt.Sprintf("%d", s.config.TruffleHogConcurrency),
		"--no-update",
	}

	if s.config.TruffleHogOnlyVerified {
		args = append(args, "--only-verified")
	}

	// Log the scan command for debugging
	logger := logging.LoggerFromContext(ctx, s.logger)
	logger.Debug("running TruffleHog scan",
		slog.String("path", repoDir),
		slog.Int("concurrency", s.config.TruffleHogConcurrency),
		slog.Bool("only_verified", s.config.TruffleHogOnlyVerified))

	// Create command
	cmd := exec.CommandContext(ctx, "trufflehog", args...)

	// Capture output
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Run TruffleHog
	err := cmd.Run()
	if err != nil {
		// TruffleHog exits with non-zero when secrets are found, which is not an error for us
		if _, ok := err.(*exec.ExitError); ok {
			// Log stderr for debugging
			if stderrBuf.Len() > 0 {
				logger.Debug("TruffleHog stderr output", slog.String("stderr", stderrBuf.String()))
			}
			// Continue processing if it's just an exit error
		} else {
			// Real error (command not found, etc.)
			return nil, fmt.Errorf("trufflehog command failed: %w (stderr: %s)", err, stderrBuf.String())
		}
	}

	// Parse JSON output line by line (TruffleHog outputs JSON lines)
	var findings []types.TruffleHogFinding
	scanner := bufio.NewScanner(&stdoutBuf)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var result TruffleHogResult
		if err := json.Unmarshal(line, &result); err != nil {
			logger.Debug("failed to parse TruffleHog JSON line", slog.String("error", err.Error()))
			continue
		}

		// Convert to our finding format
		finding := types.TruffleHogFinding{
			SecretType:  result.DetectorName,
			Description: fmt.Sprintf("Found %s secret", result.DetectorName),
			File:        result.SourceMetadata.Data.Git.File,
			Line:        result.SourceMetadata.Data.Git.Line,
			Commit:      result.SourceMetadata.Data.Git.Commit,
			Confidence:  "high",
			Validated:   result.Verified,
		}

		// Add verification error if present
		if result.VerificationError != "" {
			finding.Service = fmt.Sprintf("verification_error: %s", result.VerificationError)
		}

		findings = append(findings, finding)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning trufflehog output: %w", err)
	}

	return findings, nil
}

// TruffleHogResult represents a single finding from TruffleHog JSON output
type TruffleHogResult struct {
	SourceMetadata struct {
		Data struct {
			Git struct {
				Commit     string `json:"commit"`
				File       string `json:"file"`
				Email      string `json:"email"`
				Repository string `json:"repository"`
				Timestamp  string `json:"timestamp"`
				Line       int    `json:"line"`
			} `json:"Git"`
		} `json:"Data"`
	} `json:"SourceMetadata"`
	SourceID          int               `json:"SourceID"`
	SourceType        int               `json:"SourceType"`
	SourceName        string            `json:"SourceName"`
	DetectorType      int               `json:"DetectorType"`
	DetectorName      string            `json:"DetectorName"`
	DecoderName       string            `json:"DecoderName"`
	Verified          bool              `json:"Verified"`
	Raw               string            `json:"Raw"`
	RawV2             string            `json:"RawV2"`
	Redacted          string            `json:"Redacted"`
	ExtraData         map[string]string `json:"ExtraData"`
	StructuredData    interface{}       `json:"StructuredData"`
	VerificationError string            `json:"VerificationError"`
}

// handleScanError creates a failed scan result and pushes it to the queue
func (s *Scanner) handleScanError(ctx context.Context, workerID int, processedRepo *types.ProcessedRepository, startTime time.Time, err error) error {
	logger := logging.LoggerFromContext(ctx, s.logger)
	logger.Error("error scanning repository",
		slog.String("org", processedRepo.Org),
		slog.String("repo", processedRepo.Name),
		slog.String("error", err.Error()))

	status := "failed"
	if ctx.Err() == context.DeadlineExceeded {
		status = "timeout"
	}

	// Create failed scan result
	scannedRepo := &types.ScannedRepository{
		Org:               processedRepo.Org,
		Name:              processedRepo.Name,
		ProcessedAt:       processedRepo.ProcessedAt,
		ScannedAt:         time.Now(),
		WorkerID:          workerID,
		ValidSecretsFound: 0,
		ValidSecrets:      []types.TruffleHogFinding{},
		ScanStatus:        status,
		ScanDuration:      time.Since(startTime),
		ErrorMessage:      err.Error(),
	}

	// Marshal to JSON
	scannedData, marshalErr := json.Marshal(scannedRepo)
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal failed scan data: %w", marshalErr)
	}

	// Push to secrets queue
	if pushErr := s.redisClient.LPush(ctx, s.config.SecretsQueueName, scannedData).Err(); pushErr != nil {
		return fmt.Errorf("failed to push failed scan to queue: %w", pushErr)
	}

	// Coordination message removed - no longer using coordinator service

	return err // Return original error
}

// Helper functions to safely extract fields from JSON
func getStringField(data map[string]interface{}, field string) string {
	if val, ok := data[field]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getIntField(data map[string]interface{}, field string) int {
	if val, ok := data[field]; ok {
		if num, ok := val.(float64); ok {
			return int(num)
		}
	}
	return 0
}

func getBoolField(data map[string]interface{}, field string) bool {
	if val, ok := data[field]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

// sendCoordinationMessage function removed - no longer using coordinator service

// Close cleanly shuts down the scanner
func (s *Scanner) Close() {
	if s.redisClient != nil {
		s.redisClient.Close()
	}
}
