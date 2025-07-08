package osvscanner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/klimeurt/heimdall/internal/types"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/klimeurt/heimdall/internal/logging"
	"github.com/redis/go-redis/v9"
)

// RedisClient interface for Redis operations (allows mocking in tests)
type RedisClient interface {
	Ping(ctx context.Context) *redis.StatusCmd
	LLen(ctx context.Context, key string) *redis.IntCmd
	BRPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd
	LPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	Close() error
}

// Scanner handles the OSV vulnerability scanning operations
type Scanner struct {
	config      *config.OSVScannerConfig
	redisClient RedisClient
	logger      *slog.Logger
}

// New creates a new OSV Scanner instance
func New(cfg *config.OSVScannerConfig, logger *slog.Logger) (*Scanner, error) {
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

	// Verify osv-scanner binary exists
	if _, err := exec.LookPath("osv-scanner"); err != nil {
		return nil, fmt.Errorf("osv-scanner binary not found in PATH: %w", err)
	}

	return &Scanner{
		config:      cfg,
		redisClient: redisClient,
		logger:      logger,
	}, nil
}

// Start begins the OSV scanner workers
func (s *Scanner) Start(ctx context.Context) error {
	// Log queue length at startup
	queueLen, err := s.redisClient.LLen(ctx, s.config.OSVQueueName).Result()
	if err != nil {
		s.logger.Error("failed to get OSV queue length at startup", slog.String("error", err.Error()))
	} else {
		s.logger.Info("OSV queue length at startup",
			slog.Int64("items", queueLen),
			slog.String("queue", s.config.OSVQueueName))
	}

	s.logger.Info("starting OSV scanner workers",
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
	s.logger.Info("all OSV scanner workers have stopped")
	return nil
}

// worker processes repository scan jobs from the Redis queue
func (s *Scanner) worker(ctx context.Context, workerID int) {
	ctx = logging.WithWorker(ctx, workerID)
	logger := logging.LoggerFromContext(ctx, s.logger)
	logger.Info("OSV scanner worker started")
	defer logger.Info("OSV scanner worker stopped")

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Block until a job is available or context is cancelled
			result, err := s.redisClient.BRPop(ctx, 0, s.config.OSVQueueName).Result()
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

			// Scan the repository for vulnerabilities
			if err := s.scanRepository(ctx, workerID, &processedRepo); err != nil {
				logger.Error("failed to scan repository",
					slog.String("org", processedRepo.Org),
					slog.String("repo", processedRepo.Name),
					slog.String("error", err.Error()))
			}

			// Log queue length after job execution
			queueLen, err := s.redisClient.LLen(ctx, s.config.OSVQueueName).Result()
			if err != nil {
				logger.Error("failed to get queue length after job", slog.String("error", err.Error()))
			} else {
				logger.Debug("OSV queue length after job",
					slog.Int64("items", queueLen),
					slog.String("queue", s.config.OSVQueueName))
			}
		}
	}
}

// scanRepository scans a repository from shared volume with OSV Scanner
func (s *Scanner) scanRepository(ctx context.Context, workerID int, processedRepo *types.ProcessedRepository) error {
	startTime := time.Now()
	logger := logging.LoggerFromContext(ctx, s.logger)
	logger.Info("scanning repository for vulnerabilities",
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

	// Run OSV Scanner with timeout
	scanCtx, scanCancel := context.WithTimeout(ctx, s.config.ScanTimeout)
	defer scanCancel()

	findings, err := s.runOSVScan(scanCtx, repoDir)
	if err != nil {
		return s.handleScanError(ctx, workerID, processedRepo, startTime, fmt.Errorf("osv scan failed: %w", err))
	}

	// Create scanned repository data
	scannedRepo := &types.OSVScannedRepository{
		Org:                  processedRepo.Org,
		Name:                 processedRepo.Name,
		ProcessedAt:          processedRepo.ProcessedAt,
		ScannedAt:            time.Now(),
		WorkerID:             workerID,
		VulnerabilitiesFound: len(findings),
		Vulnerabilities:      findings,
		ScanStatus:           "success",
		ScanDuration:         time.Since(startTime),
	}

	if len(findings) == 0 {
		scannedRepo.ScanStatus = "no_vulnerabilities"
	}

	// Marshal to JSON
	scannedData, err := json.Marshal(scannedRepo)
	if err != nil {
		return fmt.Errorf("failed to marshal scanned repository data: %w", err)
	}

	// Push to OSV results queue
	if err := s.redisClient.LPush(ctx, s.config.OSVResultsQueueName, scannedData).Err(); err != nil {
		return fmt.Errorf("failed to push scanned repository to results queue: %w", err)
	}

	// Coordination message removed - no longer using coordinator service

	logger.Info("repository scanned successfully",
		slog.String("org", processedRepo.Org),
		slog.String("repo", processedRepo.Name),
		slog.Int("vulnerabilities_found", len(findings)),
		slog.Int64("duration_ms", time.Since(startTime).Milliseconds()))

	return nil
}

// runOSVScan executes osv-scanner and parses results
func (s *Scanner) runOSVScan(ctx context.Context, repoDir string) ([]types.OSVScanResult, error) {
	// Build osv-scanner command
	args := []string{
		"--format", "json",
		"--recursive",
		repoDir,
	}

	// Log the scan command for debugging
	logger := logging.LoggerFromContext(ctx, s.logger)
	logger.Debug("running OSV scan", slog.String("path", repoDir))

	// Create command
	cmd := exec.CommandContext(ctx, "osv-scanner", args...)

	// Capture output
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Run scanner-osv
	err := cmd.Run()
	if err != nil {
		// OSV scanner returns exit code 1 when vulnerabilities are found
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			// This is expected when vulnerabilities are found
			logger.Debug("OSV scanner found vulnerabilities (exit code 1)")
		} else {
			// Real error
			return nil, fmt.Errorf("osv-scanner command failed: %w (stderr: %s)", err, stderrBuf.String())
		}
	}

	// Parse JSON output
	var osvOutput OSVOutput
	if err := json.Unmarshal(stdoutBuf.Bytes(), &osvOutput); err != nil {
		// If parsing fails, it might be empty output (no vulnerabilities)
		if stdoutBuf.Len() == 0 {
			return []types.OSVScanResult{}, nil
		}
		return nil, fmt.Errorf("failed to parse OSV scanner output: %w", err)
	}

	// Convert to our format
	var findings []types.OSVScanResult
	for _, result := range osvOutput.Results {
		for _, pkg := range result.Packages {
			for _, vuln := range pkg.Vulnerabilities {
				finding := types.OSVScanResult{
					ID:          vuln.ID,
					Package:     pkg.Package.Name,
					Version:     pkg.Package.Version,
					Ecosystem:   pkg.Package.Ecosystem,
					Summary:     vuln.Summary,
					Details:     vuln.Details,
					Aliases:     vuln.Aliases,
					PackageFile: result.Source.Path,
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

				// Extract CWE
				finding.CWE = vuln.CWE

				findings = append(findings, finding)
			}
		}
	}

	return findings, nil
}

// OSVOutput represents the JSON output from osv-scanner
type OSVOutput struct {
	Results []OSVResult `json:"results"`
}

// OSVResult represents a single result from osv-scanner
type OSVResult struct {
	Source struct {
		Path string `json:"path"`
		Type string `json:"type"`
	} `json:"source"`
	Packages []OSVPackage `json:"packages"`
}

// OSVPackage represents a package with vulnerabilities
type OSVPackage struct {
	Package struct {
		Name      string `json:"name"`
		Version   string `json:"version"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Vulnerabilities []OSVVulnerability `json:"vulnerabilities"`
}

// OSVVulnerability represents a single vulnerability
type OSVVulnerability struct {
	ID       string   `json:"id"`
	Summary  string   `json:"summary"`
	Details  string   `json:"details"`
	Aliases  []string `json:"aliases"`
	CWE      []string `json:"cwe"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
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
	scannedRepo := &types.OSVScannedRepository{
		Org:                  processedRepo.Org,
		Name:                 processedRepo.Name,
		ProcessedAt:          processedRepo.ProcessedAt,
		ScannedAt:            time.Now(),
		WorkerID:             workerID,
		VulnerabilitiesFound: 0,
		Vulnerabilities:      []types.OSVScanResult{},
		ScanStatus:           status,
		ScanDuration:         time.Since(startTime),
		ErrorMessage:         err.Error(),
	}

	// Marshal to JSON
	scannedData, marshalErr := json.Marshal(scannedRepo)
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal failed scan data: %w", marshalErr)
	}

	// Push to results queue
	if pushErr := s.redisClient.LPush(ctx, s.config.OSVResultsQueueName, scannedData).Err(); pushErr != nil {
		return fmt.Errorf("failed to push failed scan to queue: %w", pushErr)
	}

	// Coordination message removed - no longer using coordinator service

	return err // Return original error
}

// sendCoordinationMessage function removed - no longer using coordinator service

// Close cleanly shuts down the scanner
func (s *Scanner) Close() {
	if s.redisClient != nil {
		s.redisClient.Close()
	}
}
