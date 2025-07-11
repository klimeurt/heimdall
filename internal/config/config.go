package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)


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
	TruffleHogConcurrency  int
	TruffleHogOnlyVerified bool
	ScanTimeout            time.Duration
	SharedVolumeDir        string
}


// IndexerConfig holds the indexer service configuration
type IndexerConfig struct {
	RedisHost                string
	RedisPort                string
	RedisPassword            string
	RedisDB                  int
	SecretsQueueName         string
	OSVResultsQueueName      string
	MaxConcurrentWorkers     int
	ElasticsearchURL         string
	IndexName                string
	VulnerabilitiesIndexName string
	BulkSize                 int
	BulkFlushInterval        time.Duration
}

// OSVScannerConfig holds the OSV scanner service configuration
type OSVScannerConfig struct {
	RedisHost            string
	RedisPort            string
	RedisPassword        string
	RedisDB              int
	OSVQueueName         string
	OSVResultsQueueName  string
	MaxConcurrentScans   int
	ScanTimeout          time.Duration
	SharedVolumeDir      string
}


// SyncConfig holds the sync service configuration
type SyncConfig struct {
	RedisURL              string
	GitHubOrg             string
	GitHubToken           string
	FetchSchedule         string
	FetchOnStartup        bool
	MaxConcurrentSyncs    int
	SyncTimeoutMinutes    int
	SharedVolumePath      string
	TruffleHogQueueName   string
	OSVQueueName          string
	GitHubAPIDelayMs      int
	QueueBatchSize        int
	EnableScannerQueues   bool
	EnableTruffleHogQueue bool
	EnableOSVQueue        bool
}


// LoadScannerConfig loads scanner service configuration from environment variables
func LoadScannerConfig() (*ScannerConfig, error) {
	cfg := &ScannerConfig{
		GitHubToken:          os.Getenv("GITHUB_TOKEN"),
		RedisHost:            os.Getenv("REDIS_HOST"),
		RedisPort:            os.Getenv("REDIS_PORT"),
		RedisPassword:        os.Getenv("REDIS_PASSWORD"),
		ProcessedQueueName:   os.Getenv("TRUFFLEHOG_QUEUE_NAME"),
		SecretsQueueName:     os.Getenv("TRUFFLEHOG_RESULTS_QUEUE_NAME"),
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
		cfg.ProcessedQueueName = "trufflehog_queue"
	}
	if cfg.SecretsQueueName == "" {
		cfg.SecretsQueueName = "trufflehog_results_queue"
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


// LoadIndexerConfig loads indexer service configuration from environment variables
func LoadIndexerConfig() (*IndexerConfig, error) {
	cfg := &IndexerConfig{
		RedisHost:                os.Getenv("REDIS_HOST"),
		RedisPort:                os.Getenv("REDIS_PORT"),
		RedisPassword:            os.Getenv("REDIS_PASSWORD"),
		SecretsQueueName:         os.Getenv("TRUFFLEHOG_RESULTS_QUEUE_NAME"),
		OSVResultsQueueName:      os.Getenv("OSV_RESULTS_QUEUE_NAME"),
		ElasticsearchURL:         os.Getenv("ELASTICSEARCH_URL"),
		IndexName:                os.Getenv("INDEX_NAME"),
		VulnerabilitiesIndexName: os.Getenv("VULNERABILITIES_INDEX_NAME"),
	}

	// Set defaults
	if cfg.RedisHost == "" {
		cfg.RedisHost = "localhost"
	}
	if cfg.RedisPort == "" {
		cfg.RedisPort = "6379"
	}
	if cfg.SecretsQueueName == "" {
		cfg.SecretsQueueName = "trufflehog_results_queue"
	}
	if cfg.OSVResultsQueueName == "" {
		cfg.OSVResultsQueueName = "osv_results_queue"
	}
	if cfg.ElasticsearchURL == "" {
		cfg.ElasticsearchURL = "http://localhost:9200"
	}
	if cfg.IndexName == "" {
		cfg.IndexName = "heimdall-secrets"
	}
	if cfg.VulnerabilitiesIndexName == "" {
		cfg.VulnerabilitiesIndexName = "heimdall-vulnerabilities"
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

