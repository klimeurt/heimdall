package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds the application configuration
type Config struct {
	GitHubOrg      string
	GitHubToken    string
	HTTPEndpoint   string
	CronSchedule   string
	RunOnStartup   bool
	GitHubPageSize int
	GitHubAPIDelay time.Duration
	// Redis configuration
	RedisHost     string
	RedisPort     string
	RedisPassword string
	RedisDB       int
}

// ClonerConfig holds the cloner service configuration
type ClonerConfig struct {
	GitHubToken         string
	RedisHost           string
	RedisPort           string
	RedisPassword       string
	RedisDB             int
	MaxConcurrentClones int
	ProcessedQueueName  string
	OSVQueueName        string
	SharedVolumeDir     string
}

// ScannerConfig holds the scanner service configuration
type ScannerConfig struct {
	GitHubToken            string
	RedisHost              string
	RedisPort              string
	RedisPassword          string
	RedisDB                int
	MaxConcurrentScans     int
	ProcessedQueueName     string
	SecretsQueueName       string
	CleanupQueueName       string
	CoordinatorQueueName   string
	TruffleHogConcurrency  int
	TruffleHogOnlyVerified bool
	ScanTimeout            time.Duration
	SharedVolumeDir        string
}

// CleanerConfig holds the cleaner service configuration
type CleanerConfig struct {
	RedisHost         string
	RedisPort         string
	RedisPassword     string
	RedisDB           int
	CleanupQueueName  string
	MaxConcurrentJobs int
	SharedVolumeDir   string
}

// IndexerConfig holds the indexer service configuration
type IndexerConfig struct {
	RedisHost            string
	RedisPort            string
	RedisPassword        string
	RedisDB              int
	SecretsQueueName     string
	MaxConcurrentWorkers int
	ElasticsearchURL     string
	IndexName            string
	BulkSize             int
	BulkFlushInterval    time.Duration
}

// OSVScannerConfig holds the OSV scanner service configuration
type OSVScannerConfig struct {
	RedisHost            string
	RedisPort            string
	RedisPassword        string
	RedisDB              int
	OSVQueueName         string
	OSVResultsQueueName  string
	CoordinatorQueueName string
	MaxConcurrentScans   int
	ScanTimeout          time.Duration
	SharedVolumeDir      string
}

// CoordinatorConfig holds the coordinator service configuration
type CoordinatorConfig struct {
	RedisHost            string
	RedisPort            string
	RedisPassword        string
	RedisDB              int
	CoordinatorQueueName string
	CleanupQueueName     string
	JobTimeoutMinutes    int
	StateCleanupInterval time.Duration
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		GitHubOrg:     os.Getenv("GITHUB_ORG"),
		GitHubToken:   os.Getenv("GITHUB_TOKEN"),
		HTTPEndpoint:  os.Getenv("HTTP_ENDPOINT"),
		CronSchedule:  os.Getenv("CRON_SCHEDULE"),
		RedisHost:     os.Getenv("REDIS_HOST"),
		RedisPort:     os.Getenv("REDIS_PORT"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
	}

	// Set defaults
	if cfg.HTTPEndpoint == "" {
		cfg.HTTPEndpoint = "http://localhost:8080/repositories"
	}
	if cfg.CronSchedule == "" {
		cfg.CronSchedule = "0 0 * * 0" // Weekly on Sunday at midnight
	}
	if cfg.RedisHost == "" {
		cfg.RedisHost = "localhost"
	}
	if cfg.RedisPort == "" {
		cfg.RedisPort = "6379"
	}

	// Set GitHub API defaults
	cfg.GitHubPageSize = 100
	if pageSize := os.Getenv("GITHUB_PAGE_SIZE"); pageSize != "" {
		size, err := strconv.Atoi(pageSize)
		if err != nil {
			return nil, fmt.Errorf("invalid GITHUB_PAGE_SIZE value: %v", err)
		}
		if size <= 0 || size > 100 {
			return nil, fmt.Errorf("GITHUB_PAGE_SIZE must be between 1 and 100")
		}
		cfg.GitHubPageSize = size
	}

	if delayMs := os.Getenv("GITHUB_API_DELAY_MS"); delayMs != "" {
		delay, err := strconv.Atoi(delayMs)
		if err != nil {
			return nil, fmt.Errorf("invalid GITHUB_API_DELAY_MS value: %v", err)
		}
		if delay < 0 {
			return nil, fmt.Errorf("GITHUB_API_DELAY_MS must be non-negative")
		}
		cfg.GitHubAPIDelay = time.Duration(delay) * time.Millisecond
	}

	// Parse Redis DB
	if redisDB := os.Getenv("REDIS_DB"); redisDB != "" {
		db, err := strconv.Atoi(redisDB)
		if err != nil {
			return nil, fmt.Errorf("invalid REDIS_DB value: %v", err)
		}
		cfg.RedisDB = db
	}

	// Validate required fields
	if cfg.GitHubOrg == "" {
		return nil, fmt.Errorf("GITHUB_ORG environment variable is required")
	}

	// Check if we should run on startup
	if os.Getenv("RUN_ON_STARTUP") == "true" {
		cfg.RunOnStartup = true
	}

	return cfg, nil
}

// LoadClonerConfig loads cloner service configuration from environment variables
func LoadClonerConfig() (*ClonerConfig, error) {
	cfg := &ClonerConfig{
		GitHubToken:        os.Getenv("GITHUB_TOKEN"),
		RedisHost:          os.Getenv("REDIS_HOST"),
		RedisPort:          os.Getenv("REDIS_PORT"),
		RedisPassword:      os.Getenv("REDIS_PASSWORD"),
		ProcessedQueueName: os.Getenv("PROCESSED_QUEUE_NAME"),
		OSVQueueName:       os.Getenv("OSV_QUEUE_NAME"),
		SharedVolumeDir:    os.Getenv("SHARED_VOLUME_DIR"),
	}

	// Set defaults
	if cfg.RedisHost == "" {
		cfg.RedisHost = "localhost"
	}
	if cfg.RedisPort == "" {
		cfg.RedisPort = "6379"
	}
	if cfg.ProcessedQueueName == "" {
		cfg.ProcessedQueueName = "processed_queue"
	}
	if cfg.OSVQueueName == "" {
		cfg.OSVQueueName = "osv_queue"
	}
	if cfg.SharedVolumeDir == "" {
		cfg.SharedVolumeDir = "/shared/heimdall-repos"
	}
	cfg.MaxConcurrentClones = 5 // Default to 5 concurrent clones

	// Parse Redis DB
	if redisDB := os.Getenv("REDIS_DB"); redisDB != "" {
		db, err := strconv.Atoi(redisDB)
		if err != nil {
			return nil, fmt.Errorf("invalid REDIS_DB value: %v", err)
		}
		cfg.RedisDB = db
	}

	// Parse max concurrent clones
	if maxClones := os.Getenv("MAX_CONCURRENT_CLONES"); maxClones != "" {
		max, err := strconv.Atoi(maxClones)
		if err != nil {
			return nil, fmt.Errorf("invalid MAX_CONCURRENT_CLONES value: %v", err)
		}
		if max <= 0 {
			return nil, fmt.Errorf("MAX_CONCURRENT_CLONES must be greater than 0")
		}
		cfg.MaxConcurrentClones = max
	}

	return cfg, nil
}

// LoadScannerConfig loads scanner service configuration from environment variables
func LoadScannerConfig() (*ScannerConfig, error) {
	cfg := &ScannerConfig{
		GitHubToken:          os.Getenv("GITHUB_TOKEN"),
		RedisHost:            os.Getenv("REDIS_HOST"),
		RedisPort:            os.Getenv("REDIS_PORT"),
		RedisPassword:        os.Getenv("REDIS_PASSWORD"),
		ProcessedQueueName:   os.Getenv("PROCESSED_QUEUE_NAME"),
		SecretsQueueName:     os.Getenv("SECRETS_QUEUE_NAME"),
		CleanupQueueName:     os.Getenv("CLEANUP_QUEUE_NAME"),
		CoordinatorQueueName: os.Getenv("COORDINATOR_QUEUE_NAME"),
		SharedVolumeDir:      os.Getenv("SHARED_VOLUME_DIR"),
	}

	// Set defaults
	if cfg.RedisHost == "" {
		cfg.RedisHost = "localhost"
	}
	if cfg.RedisPort == "" {
		cfg.RedisPort = "6379"
	}
	if cfg.ProcessedQueueName == "" {
		cfg.ProcessedQueueName = "processed_queue"
	}
	if cfg.SecretsQueueName == "" {
		cfg.SecretsQueueName = "secrets_queue"
	}
	if cfg.CleanupQueueName == "" {
		cfg.CleanupQueueName = "cleanup_queue"
	}
	if cfg.CoordinatorQueueName == "" {
		cfg.CoordinatorQueueName = "coordinator_queue"
	}
	if cfg.SharedVolumeDir == "" {
		cfg.SharedVolumeDir = "/shared/heimdall-repos"
	}
	cfg.MaxConcurrentScans = 3         // Default to 3 concurrent scans (less than cloner due to disk I/O)
	cfg.ScanTimeout = 30 * time.Minute // Default 30 minute timeout per repository
	cfg.TruffleHogConcurrency = 8      // Default TruffleHog concurrency
	cfg.TruffleHogOnlyVerified = false // Default to showing all secrets, not just verified

	// Parse Redis DB
	if redisDB := os.Getenv("REDIS_DB"); redisDB != "" {
		db, err := strconv.Atoi(redisDB)
		if err != nil {
			return nil, fmt.Errorf("invalid REDIS_DB value: %v", err)
		}
		cfg.RedisDB = db
	}

	// Parse max concurrent scans
	if maxScans := os.Getenv("MAX_CONCURRENT_SCANS"); maxScans != "" {
		max, err := strconv.Atoi(maxScans)
		if err != nil {
			return nil, fmt.Errorf("invalid MAX_CONCURRENT_SCANS value: %v", err)
		}
		if max <= 0 {
			return nil, fmt.Errorf("MAX_CONCURRENT_SCANS must be greater than 0")
		}
		cfg.MaxConcurrentScans = max
	}

	// Parse scan timeout
	if timeoutMin := os.Getenv("SCAN_TIMEOUT_MINUTES"); timeoutMin != "" {
		timeout, err := strconv.Atoi(timeoutMin)
		if err != nil {
			return nil, fmt.Errorf("invalid SCAN_TIMEOUT_MINUTES value: %v", err)
		}
		if timeout <= 0 {
			return nil, fmt.Errorf("SCAN_TIMEOUT_MINUTES must be greater than 0")
		}
		cfg.ScanTimeout = time.Duration(timeout) * time.Minute
	}

	// Parse TruffleHog concurrency
	if concurrency := os.Getenv("TRUFFLEHOG_CONCURRENCY"); concurrency != "" {
		c, err := strconv.Atoi(concurrency)
		if err != nil {
			return nil, fmt.Errorf("invalid TRUFFLEHOG_CONCURRENCY value: %v", err)
		}
		if c <= 0 {
			return nil, fmt.Errorf("TRUFFLEHOG_CONCURRENCY must be greater than 0")
		}
		cfg.TruffleHogConcurrency = c
	}

	// Parse TruffleHog only verified
	if onlyVerified := os.Getenv("TRUFFLEHOG_ONLY_VERIFIED"); onlyVerified != "" {
		cfg.TruffleHogOnlyVerified = onlyVerified == "true"
	}

	return cfg, nil
}

// LoadCleanerConfig loads cleaner service configuration from environment variables
func LoadCleanerConfig() (*CleanerConfig, error) {
	cfg := &CleanerConfig{
		RedisHost:        os.Getenv("REDIS_HOST"),
		RedisPort:        os.Getenv("REDIS_PORT"),
		RedisPassword:    os.Getenv("REDIS_PASSWORD"),
		CleanupQueueName: os.Getenv("CLEANUP_QUEUE_NAME"),
		SharedVolumeDir:  os.Getenv("SHARED_VOLUME_DIR"),
	}

	// Set defaults
	if cfg.RedisHost == "" {
		cfg.RedisHost = "localhost"
	}
	if cfg.RedisPort == "" {
		cfg.RedisPort = "6379"
	}
	if cfg.CleanupQueueName == "" {
		cfg.CleanupQueueName = "cleanup_queue"
	}
	if cfg.SharedVolumeDir == "" {
		cfg.SharedVolumeDir = "/shared/heimdall-repos"
	}
	cfg.MaxConcurrentJobs = 2 // Default to 2 concurrent cleanup jobs

	// Parse Redis DB
	if redisDB := os.Getenv("REDIS_DB"); redisDB != "" {
		db, err := strconv.Atoi(redisDB)
		if err != nil {
			return nil, fmt.Errorf("invalid REDIS_DB value: %v", err)
		}
		cfg.RedisDB = db
	}

	// Parse max concurrent jobs
	if maxJobs := os.Getenv("MAX_CONCURRENT_JOBS"); maxJobs != "" {
		max, err := strconv.Atoi(maxJobs)
		if err != nil {
			return nil, fmt.Errorf("invalid MAX_CONCURRENT_JOBS value: %v", err)
		}
		if max <= 0 {
			return nil, fmt.Errorf("MAX_CONCURRENT_JOBS must be greater than 0")
		}
		cfg.MaxConcurrentJobs = max
	}

	return cfg, nil
}

// LoadIndexerConfig loads indexer service configuration from environment variables
func LoadIndexerConfig() (*IndexerConfig, error) {
	cfg := &IndexerConfig{
		RedisHost:        os.Getenv("REDIS_HOST"),
		RedisPort:        os.Getenv("REDIS_PORT"),
		RedisPassword:    os.Getenv("REDIS_PASSWORD"),
		SecretsQueueName: os.Getenv("SECRETS_QUEUE_NAME"),
		ElasticsearchURL: os.Getenv("ELASTICSEARCH_URL"),
		IndexName:        os.Getenv("INDEX_NAME"),
	}

	// Set defaults
	if cfg.RedisHost == "" {
		cfg.RedisHost = "localhost"
	}
	if cfg.RedisPort == "" {
		cfg.RedisPort = "6379"
	}
	if cfg.SecretsQueueName == "" {
		cfg.SecretsQueueName = "secrets_queue"
	}
	if cfg.ElasticsearchURL == "" {
		cfg.ElasticsearchURL = "http://localhost:9200"
	}
	if cfg.IndexName == "" {
		cfg.IndexName = "heimdall-secrets"
	}
	cfg.MaxConcurrentWorkers = 2             // Default to 2 concurrent workers
	cfg.BulkSize = 50                        // Default bulk size
	cfg.BulkFlushInterval = 10 * time.Second // Default flush interval

	// Parse Redis DB
	if redisDB := os.Getenv("REDIS_DB"); redisDB != "" {
		db, err := strconv.Atoi(redisDB)
		if err != nil {
			return nil, fmt.Errorf("invalid REDIS_DB value: %v", err)
		}
		cfg.RedisDB = db
	}

	// Parse max concurrent workers
	if maxWorkers := os.Getenv("MAX_CONCURRENT_WORKERS"); maxWorkers != "" {
		max, err := strconv.Atoi(maxWorkers)
		if err != nil {
			return nil, fmt.Errorf("invalid MAX_CONCURRENT_WORKERS value: %v", err)
		}
		if max <= 0 {
			return nil, fmt.Errorf("MAX_CONCURRENT_WORKERS must be greater than 0")
		}
		cfg.MaxConcurrentWorkers = max
	}

	// Parse bulk size
	if bulkSize := os.Getenv("BULK_SIZE"); bulkSize != "" {
		size, err := strconv.Atoi(bulkSize)
		if err != nil {
			return nil, fmt.Errorf("invalid BULK_SIZE value: %v", err)
		}
		if size <= 0 {
			return nil, fmt.Errorf("BULK_SIZE must be greater than 0")
		}
		cfg.BulkSize = size
	}

	// Parse bulk flush interval
	if flushInterval := os.Getenv("BULK_FLUSH_INTERVAL"); flushInterval != "" {
		duration, err := time.ParseDuration(flushInterval)
		if err != nil {
			return nil, fmt.Errorf("invalid BULK_FLUSH_INTERVAL value: %v", err)
		}
		if duration <= 0 {
			return nil, fmt.Errorf("BULK_FLUSH_INTERVAL must be greater than 0")
		}
		cfg.BulkFlushInterval = duration
	}

	return cfg, nil
}

// LoadOSVScannerConfig loads OSV scanner service configuration from environment variables
func LoadOSVScannerConfig() (*OSVScannerConfig, error) {
	cfg := &OSVScannerConfig{
		RedisHost:            os.Getenv("REDIS_HOST"),
		RedisPort:            os.Getenv("REDIS_PORT"),
		RedisPassword:        os.Getenv("REDIS_PASSWORD"),
		OSVQueueName:         os.Getenv("OSV_QUEUE_NAME"),
		OSVResultsQueueName:  os.Getenv("OSV_RESULTS_QUEUE_NAME"),
		CoordinatorQueueName: os.Getenv("COORDINATOR_QUEUE_NAME"),
		SharedVolumeDir:      os.Getenv("SHARED_VOLUME_DIR"),
	}

	// Set defaults
	if cfg.RedisHost == "" {
		cfg.RedisHost = "localhost"
	}
	if cfg.RedisPort == "" {
		cfg.RedisPort = "6379"
	}
	if cfg.OSVQueueName == "" {
		cfg.OSVQueueName = "osv_queue"
	}
	if cfg.OSVResultsQueueName == "" {
		cfg.OSVResultsQueueName = "osv_results_queue"
	}
	if cfg.CoordinatorQueueName == "" {
		cfg.CoordinatorQueueName = "coordinator_queue"
	}
	if cfg.SharedVolumeDir == "" {
		cfg.SharedVolumeDir = "/shared/heimdall-repos"
	}
	cfg.MaxConcurrentScans = 3         // Default to 3 concurrent scans
	cfg.ScanTimeout = 30 * time.Minute // Default 30 minute timeout

	// Parse Redis DB
	if redisDB := os.Getenv("REDIS_DB"); redisDB != "" {
		db, err := strconv.Atoi(redisDB)
		if err != nil {
			return nil, fmt.Errorf("invalid REDIS_DB value: %v", err)
		}
		cfg.RedisDB = db
	}

	// Parse max concurrent scans
	if maxScans := os.Getenv("MAX_CONCURRENT_SCANS"); maxScans != "" {
		max, err := strconv.Atoi(maxScans)
		if err != nil {
			return nil, fmt.Errorf("invalid MAX_CONCURRENT_SCANS value: %v", err)
		}
		if max <= 0 {
			return nil, fmt.Errorf("MAX_CONCURRENT_SCANS must be greater than 0")
		}
		cfg.MaxConcurrentScans = max
	}

	// Parse scan timeout
	if timeoutMin := os.Getenv("SCAN_TIMEOUT_MINUTES"); timeoutMin != "" {
		timeout, err := strconv.Atoi(timeoutMin)
		if err != nil {
			return nil, fmt.Errorf("invalid SCAN_TIMEOUT_MINUTES value: %v", err)
		}
		if timeout <= 0 {
			return nil, fmt.Errorf("SCAN_TIMEOUT_MINUTES must be greater than 0")
		}
		cfg.ScanTimeout = time.Duration(timeout) * time.Minute
	}

	return cfg, nil
}

// LoadCoordinatorConfig loads coordinator service configuration from environment variables
func LoadCoordinatorConfig() (*CoordinatorConfig, error) {
	cfg := &CoordinatorConfig{
		RedisHost:            os.Getenv("REDIS_HOST"),
		RedisPort:            os.Getenv("REDIS_PORT"),
		RedisPassword:        os.Getenv("REDIS_PASSWORD"),
		CoordinatorQueueName: os.Getenv("COORDINATOR_QUEUE_NAME"),
		CleanupQueueName:     os.Getenv("CLEANUP_QUEUE_NAME"),
	}

	// Set defaults
	if cfg.RedisHost == "" {
		cfg.RedisHost = "localhost"
	}
	if cfg.RedisPort == "" {
		cfg.RedisPort = "6379"
	}
	if cfg.CoordinatorQueueName == "" {
		cfg.CoordinatorQueueName = "coordinator_queue"
	}
	if cfg.CleanupQueueName == "" {
		cfg.CleanupQueueName = "cleanup_queue"
	}
	cfg.JobTimeoutMinutes = 60                 // Default 60 minute timeout for coordination
	cfg.StateCleanupInterval = 5 * time.Minute // Default 5 minute cleanup interval

	// Parse Redis DB
	if redisDB := os.Getenv("REDIS_DB"); redisDB != "" {
		db, err := strconv.Atoi(redisDB)
		if err != nil {
			return nil, fmt.Errorf("invalid REDIS_DB value: %v", err)
		}
		cfg.RedisDB = db
	}

	// Parse job timeout
	if timeoutMin := os.Getenv("JOB_TIMEOUT_MINUTES"); timeoutMin != "" {
		timeout, err := strconv.Atoi(timeoutMin)
		if err != nil {
			return nil, fmt.Errorf("invalid JOB_TIMEOUT_MINUTES value: %v", err)
		}
		if timeout <= 0 {
			return nil, fmt.Errorf("JOB_TIMEOUT_MINUTES must be greater than 0")
		}
		cfg.JobTimeoutMinutes = timeout
	}

	// Parse state cleanup interval
	if interval := os.Getenv("STATE_CLEANUP_INTERVAL"); interval != "" {
		duration, err := time.ParseDuration(interval)
		if err != nil {
			return nil, fmt.Errorf("invalid STATE_CLEANUP_INTERVAL value: %v", err)
		}
		if duration <= 0 {
			return nil, fmt.Errorf("STATE_CLEANUP_INTERVAL must be greater than 0")
		}
		cfg.StateCleanupInterval = duration
	}

	return cfg, nil
}
