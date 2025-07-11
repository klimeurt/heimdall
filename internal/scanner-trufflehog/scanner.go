package scanner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/klimeurt/heimdall/internal/collector"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/redis/go-redis/v9"
)

// Scanner handles the repository secret scanning operations
type Scanner struct {
	config      *config.ScannerConfig
	redisClient RedisClient
	auth        *http.BasicAuth
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
func New(cfg *config.ScannerConfig) (*Scanner, error) {
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
	}, nil
}

// Start begins the scanner workers
func (s *Scanner) Start(ctx context.Context) error {
	// Log queue length at startup
	queueLen, err := s.redisClient.LLen(ctx, s.config.ProcessedQueueName).Result()
	if err != nil {
		log.Printf("Failed to get queue length at startup: %v", err)
	} else {
		log.Printf("Queue length at startup: %d items", queueLen)
	}

	log.Printf("Starting %d scanner workers", s.config.MaxConcurrentScans)

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
	log.Println("All scanner workers have stopped")
	return nil
}

// worker processes repository scan jobs from the Redis queue
func (s *Scanner) worker(ctx context.Context, workerID int) {
	log.Printf("Scanner Worker %d started", workerID)
	defer log.Printf("Scanner Worker %d stopped", workerID)

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
				log.Printf("Scanner Worker %d: Redis error: %v", workerID, err)
				continue
			}

			// result[0] is the queue name, result[1] is the data
			if len(result) < 2 {
				log.Printf("Scanner Worker %d: Invalid Redis result", workerID)
				continue
			}

			// Parse processed repository data
			var processedRepo collector.ProcessedRepository
			if err := json.Unmarshal([]byte(result[1]), &processedRepo); err != nil {
				log.Printf("Scanner Worker %d: Failed to parse processed repository data: %v", workerID, err)
				continue
			}

			// Scan the repository for secrets
			if err := s.scanRepository(ctx, workerID, &processedRepo); err != nil {
				log.Printf("Scanner Worker %d: Failed to scan repository %s/%s: %v", workerID, processedRepo.Org, processedRepo.Name, err)
			}

			// Log queue length after job execution
			queueLen, err := s.redisClient.LLen(ctx, s.config.ProcessedQueueName).Result()
			if err != nil {
				log.Printf("Scanner Worker %d: Failed to get queue length after job: %v", workerID, err)
			} else {
				log.Printf("Scanner Worker %d: Queue length after job: %d items", workerID, queueLen)
			}
		}
	}
}

// scanRepository scans a repository from shared volume with TruffleHog
func (s *Scanner) scanRepository(ctx context.Context, workerID int, processedRepo *collector.ProcessedRepository) error {
	startTime := time.Now()
	log.Printf("Scanner Worker %d: Scanning repository %s/%s", workerID, processedRepo.Org, processedRepo.Name)

	// Use the clone path from the processed repository
	repoDir := processedRepo.ClonePath

	// Validate that the path exists
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		err := fmt.Errorf("clone path does not exist: %s", repoDir)
		// Still send coordination message even if path is missing
		if coordErr := s.sendCoordinationMessage(ctx, workerID, processedRepo, "failed", startTime); coordErr != nil {
			log.Printf("Scanner Worker %d: Failed to send coordination message for missing path %s/%s: %v", workerID, processedRepo.Org, processedRepo.Name, coordErr)
		}
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
	scannedRepo := &collector.ScannedRepository{
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

	// Send coordination message
	if err := s.sendCoordinationMessage(ctx, workerID, processedRepo, scannedRepo.ScanStatus, startTime); err != nil {
		log.Printf("Scanner Worker %d: Failed to send coordination message for %s/%s: %v", workerID, processedRepo.Org, processedRepo.Name, err)
	}

	log.Printf("Scanner Worker %d: Repository %s/%s scanned successfully - found %d valid secrets in %v",
		workerID, processedRepo.Org, processedRepo.Name, len(findings), time.Since(startTime))

	return nil
}

// runTruffleHogScan executes TruffleHog and parses results
func (s *Scanner) runTruffleHogScan(ctx context.Context, repoDir string) ([]collector.TruffleHogFinding, error) {
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
	log.Printf("Running TruffleHog scan on all branches in: %s", repoDir)

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
				log.Printf("TruffleHog stderr: %s", stderrBuf.String())
			}
			// Continue processing if it's just an exit error
		} else {
			// Real error (command not found, etc.)
			return nil, fmt.Errorf("trufflehog command failed: %w (stderr: %s)", err, stderrBuf.String())
		}
	}

	// Parse JSON output line by line (TruffleHog outputs JSON lines)
	var findings []collector.TruffleHogFinding
	scanner := bufio.NewScanner(&stdoutBuf)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var result TruffleHogResult
		if err := json.Unmarshal(line, &result); err != nil {
			log.Printf("Failed to parse TruffleHog JSON line: %v", err)
			continue
		}

		// Convert to our finding format
		finding := collector.TruffleHogFinding{
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
func (s *Scanner) handleScanError(ctx context.Context, workerID int, processedRepo *collector.ProcessedRepository, startTime time.Time, err error) error {
	log.Printf("Scanner Worker %d: Error scanning %s/%s: %v", workerID, processedRepo.Org, processedRepo.Name, err)

	status := "failed"
	if ctx.Err() == context.DeadlineExceeded {
		status = "timeout"
	}

	// Create failed scan result
	scannedRepo := &collector.ScannedRepository{
		Org:               processedRepo.Org,
		Name:              processedRepo.Name,
		ProcessedAt:       processedRepo.ProcessedAt,
		ScannedAt:         time.Now(),
		WorkerID:          workerID,
		ValidSecretsFound: 0,
		ValidSecrets:      []collector.TruffleHogFinding{},
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

	// Send coordination message even for failed scans
	if coordErr := s.sendCoordinationMessage(ctx, workerID, processedRepo, status, startTime); coordErr != nil {
		log.Printf("Scanner Worker %d: Failed to send coordination message for failed scan %s/%s: %v", workerID, processedRepo.Org, processedRepo.Name, coordErr)
	}

	return err // Return original error
}


// sendCoordinationMessage sends a message to the coordinator when scanning is complete
func (s *Scanner) sendCoordinationMessage(ctx context.Context, workerID int, processedRepo *collector.ProcessedRepository, status string, startTime time.Time) error {
	coordMsg := &collector.ScanCoordinationMessage{
		ClonePath:    processedRepo.ClonePath,
		Org:          processedRepo.Org,
		Name:         processedRepo.Name,
		ScannerType:  "scanner-trufflehog",
		CompletedAt:  time.Now(),
		WorkerID:     workerID,
		ScanStatus:   status,
		ScanDuration: time.Since(startTime),
	}

	// Marshal to JSON
	coordData, err := json.Marshal(coordMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal coordination message: %w", err)
	}

	// Push to coordinator queue
	if err := s.redisClient.LPush(ctx, s.config.CoordinatorQueueName, coordData).Err(); err != nil {
		return fmt.Errorf("failed to push coordination message to queue: %w", err)
	}

	return nil
}

// Close cleanly shuts down the scanner
func (s *Scanner) Close() {
	if s.redisClient != nil {
		s.redisClient.Close()
	}
}
