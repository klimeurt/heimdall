package osvscanner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/klimeurt/heimdall/internal/collector"
	"github.com/klimeurt/heimdall/internal/config"
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
}

// New creates a new OSV Scanner instance
func New(cfg *config.OSVScannerConfig) (*Scanner, error) {
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

	// Verify scanner-osv binary exists
	if _, err := exec.LookPath("scanner-osv"); err != nil {
		return nil, fmt.Errorf("scanner-osv binary not found in PATH: %w", err)
	}

	return &Scanner{
		config:      cfg,
		redisClient: redisClient,
	}, nil
}

// Start begins the OSV scanner workers
func (s *Scanner) Start(ctx context.Context) error {
	// Log queue length at startup
	queueLen, err := s.redisClient.LLen(ctx, s.config.OSVQueueName).Result()
	if err != nil {
		log.Printf("Failed to get OSV queue length at startup: %v", err)
	} else {
		log.Printf("OSV queue length at startup: %d items", queueLen)
	}

	log.Printf("Starting %d OSV scanner workers", s.config.MaxConcurrentScans)

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
	log.Println("All OSV scanner workers have stopped")
	return nil
}

// worker processes repository scan jobs from the Redis queue
func (s *Scanner) worker(ctx context.Context, workerID int) {
	log.Printf("OSV Scanner Worker %d started", workerID)
	defer log.Printf("OSV Scanner Worker %d stopped", workerID)

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
				log.Printf("OSV Scanner Worker %d: Redis error: %v", workerID, err)
				continue
			}

			// result[0] is the queue name, result[1] is the data
			if len(result) < 2 {
				log.Printf("OSV Scanner Worker %d: Invalid Redis result", workerID)
				continue
			}

			// Parse processed repository data
			var processedRepo collector.ProcessedRepository
			if err := json.Unmarshal([]byte(result[1]), &processedRepo); err != nil {
				log.Printf("OSV Scanner Worker %d: Failed to parse processed repository data: %v", workerID, err)
				continue
			}

			// Scan the repository for vulnerabilities
			if err := s.scanRepository(ctx, workerID, &processedRepo); err != nil {
				log.Printf("OSV Scanner Worker %d: Failed to scan repository %s/%s: %v", workerID, processedRepo.Org, processedRepo.Name, err)
			}

			// Log queue length after job execution
			queueLen, err := s.redisClient.LLen(ctx, s.config.OSVQueueName).Result()
			if err != nil {
				log.Printf("OSV Scanner Worker %d: Failed to get queue length after job: %v", workerID, err)
			} else {
				log.Printf("OSV Scanner Worker %d: OSV queue length after job: %d items", workerID, queueLen)
			}
		}
	}
}

// scanRepository scans a repository from shared volume with OSV Scanner
func (s *Scanner) scanRepository(ctx context.Context, workerID int, processedRepo *collector.ProcessedRepository) error {
	startTime := time.Now()
	log.Printf("OSV Scanner Worker %d: Scanning repository %s/%s for vulnerabilities", workerID, processedRepo.Org, processedRepo.Name)

	// Use the clone path from the processed repository
	repoDir := processedRepo.ClonePath

	// Validate that the path exists
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		err := fmt.Errorf("clone path does not exist: %s", repoDir)
		// Still send coordination message even if path is missing
		s.sendCoordinationMessage(ctx, workerID, processedRepo, "failed", startTime)
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
	scannedRepo := &collector.OSVScannedRepository{
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

	// Send coordination message
	if err := s.sendCoordinationMessage(ctx, workerID, processedRepo, scannedRepo.ScanStatus, startTime); err != nil {
		log.Printf("OSV Scanner Worker %d: Failed to send coordination message for %s/%s: %v", workerID, processedRepo.Org, processedRepo.Name, err)
	}

	log.Printf("OSV Scanner Worker %d: Repository %s/%s scanned successfully - found %d vulnerabilities in %v",
		workerID, processedRepo.Org, processedRepo.Name, len(findings), time.Since(startTime))

	return nil
}

// runOSVScan executes scanner-osv and parses results
func (s *Scanner) runOSVScan(ctx context.Context, repoDir string) ([]collector.OSVScanResult, error) {
	// Build scanner-osv command
	args := []string{
		"--format", "json",
		"--recursive",
		repoDir,
	}

	// Log the scan command for debugging
	log.Printf("Running OSV scan on: %s", repoDir)

	// Create command
	cmd := exec.CommandContext(ctx, "scanner-osv", args...)

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
			log.Printf("OSV scanner found vulnerabilities (exit code 1)")
		} else {
			// Real error
			return nil, fmt.Errorf("scanner-osv command failed: %w (stderr: %s)", err, stderrBuf.String())
		}
	}

	// Parse JSON output
	var osvOutput OSVOutput
	if err := json.Unmarshal(stdoutBuf.Bytes(), &osvOutput); err != nil {
		// If parsing fails, it might be empty output (no vulnerabilities)
		if stdoutBuf.Len() == 0 {
			return []collector.OSVScanResult{}, nil
		}
		return nil, fmt.Errorf("failed to parse OSV scanner output: %w", err)
	}

	// Convert to our format
	var findings []collector.OSVScanResult
	for _, result := range osvOutput.Results {
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

// OSVOutput represents the JSON output from scanner-osv
type OSVOutput struct {
	Results []OSVResult `json:"results"`
}

// OSVResult represents a single result from scanner-osv
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
func (s *Scanner) handleScanError(ctx context.Context, workerID int, processedRepo *collector.ProcessedRepository, startTime time.Time, err error) error {
	log.Printf("OSV Scanner Worker %d: Error scanning %s/%s: %v", workerID, processedRepo.Org, processedRepo.Name, err)

	status := "failed"
	if ctx.Err() == context.DeadlineExceeded {
		status = "timeout"
	}

	// Create failed scan result
	scannedRepo := &collector.OSVScannedRepository{
		Org:                  processedRepo.Org,
		Name:                 processedRepo.Name,
		ProcessedAt:          processedRepo.ProcessedAt,
		ScannedAt:            time.Now(),
		WorkerID:             workerID,
		VulnerabilitiesFound: 0,
		Vulnerabilities:      []collector.OSVScanResult{},
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

	// Send coordination message even for failed scans
	if coordErr := s.sendCoordinationMessage(ctx, workerID, processedRepo, status, startTime); coordErr != nil {
		log.Printf("OSV Scanner Worker %d: Failed to send coordination message for failed scan %s/%s: %v", workerID, processedRepo.Org, processedRepo.Name, coordErr)
	}

	return err // Return original error
}

// sendCoordinationMessage sends a message to the coordinator when scanning is complete
func (s *Scanner) sendCoordinationMessage(ctx context.Context, workerID int, processedRepo *collector.ProcessedRepository, status string, startTime time.Time) error {
	coordMsg := &collector.ScanCoordinationMessage{
		ClonePath:    processedRepo.ClonePath,
		Org:          processedRepo.Org,
		Name:         processedRepo.Name,
		ScannerType:  "scanner-osv",
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
