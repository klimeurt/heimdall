package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/elastic/go-elasticsearch/v9"
	"github.com/elastic/go-elasticsearch/v9/esapi"
	"github.com/klimeurt/heimdall/internal/types"
	"github.com/klimeurt/heimdall/internal/config"
	"github.com/klimeurt/heimdall/internal/logging"
	"github.com/redis/go-redis/v9"
)

// Indexer handles the repository secret indexing operations
type Indexer struct {
	config      *config.IndexerConfig
	redisClient *redis.Client
	esClient    *elasticsearch.Client
	bulkBuffer  []BulkDocument
	bufferMutex sync.Mutex
	flushTimer  *time.Timer
	logger      *slog.Logger
}

// BulkDocument represents a document to be indexed
type BulkDocument struct {
	Index    string
	ID       string
	Document interface{}
}

// ElasticsearchDocument represents the structure of documents indexed in Elasticsearch
type ElasticsearchDocument struct {
	Organization string    `json:"organization"`
	Repository   string    `json:"repository"`
	SecretType   string    `json:"secret_type"`
	Description  string    `json:"description"`
	FilePath     string    `json:"file_path"`
	LineNumber   int       `json:"line_number"`
	CommitHash   string    `json:"commit_hash"`
	Confidence   string    `json:"confidence"`
	Validated    bool      `json:"validated"`
	ScannedAt    time.Time `json:"scanned_at"`
	ProcessedAt  time.Time `json:"processed_at"`
	ScanStatus   string    `json:"scan_status"`
	WorkerID     int       `json:"worker_id"`
}

// VulnerabilityDetail represents individual vulnerability information
type VulnerabilityDetail struct {
	ID          string   `json:"id"`
	Package     string   `json:"package"`
	Version     string   `json:"version"`
	Ecosystem   string   `json:"ecosystem"`
	Severity    string   `json:"severity"`
	Summary     string   `json:"summary"`
	CVE         string   `json:"cve,omitempty"`
	CWE         []string `json:"cwe,omitempty"`
	PackageFile string   `json:"package_file"`
}

// ElasticsearchVulnerabilityDocument represents the structure of vulnerability documents indexed in Elasticsearch
type ElasticsearchVulnerabilityDocument struct {
	Organization       string                `json:"organization"`
	Repository         string                `json:"repository"`
	VulnerabilityCount int                   `json:"vulnerability_count"`
	CriticalCount      int                   `json:"critical_count"`
	HighCount          int                   `json:"high_count"`
	MediumCount        int                   `json:"medium_count"`
	LowCount           int                   `json:"low_count"`
	UnknownCount       int                   `json:"unknown_count"`
	ScannedAt          time.Time             `json:"scanned_at"`
	ProcessedAt        time.Time             `json:"processed_at"`
	ScanStatus         string                `json:"scan_status"`
	ScanDuration       time.Duration         `json:"scan_duration"`
	WorkerID           int                   `json:"worker_id"`
	ErrorMessage       string                `json:"error_message,omitempty"`
	Vulnerabilities    []VulnerabilityDetail `json:"vulnerabilities"`
}

// New creates a new Indexer instance
func New(cfg *config.IndexerConfig, logger *slog.Logger) (*Indexer, error) {
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

	// Create Elasticsearch client with retry logic
	esConfig := elasticsearch.Config{
		Addresses: []string{cfg.ElasticsearchURL},
	}

	var esClient *elasticsearch.Client
	var err error

	// Retry connection to Elasticsearch
	maxRetries := 10
	retryDelay := 5 * time.Second

	for i := 0; i < maxRetries; i++ {
		esClient, err = elasticsearch.NewClient(esConfig)
		if err != nil {
			logger.Error("failed to create Elasticsearch client",
				slog.Int("attempt", i+1),
				slog.Int("max_retries", maxRetries),
				slog.String("error", err.Error()))
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return nil, fmt.Errorf("failed to create Elasticsearch client after %d attempts: %w", maxRetries, err)
		}

		// Test Elasticsearch connection
		res, err := esClient.Info()
		if err != nil {
			logger.Error("failed to connect to Elasticsearch",
				slog.Int("attempt", i+1),
				slog.Int("max_retries", maxRetries),
				slog.String("error", err.Error()))
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return nil, fmt.Errorf("failed to connect to Elasticsearch after %d attempts: %w", maxRetries, err)
		}
		defer res.Body.Close()

		if res.IsError() {
			logger.Error("Elasticsearch returned error",
				slog.Int("attempt", i+1),
				slog.Int("max_retries", maxRetries),
				slog.String("response", res.String()))
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return nil, fmt.Errorf("Elasticsearch returned error after %d attempts: %s", maxRetries, res.String())
		}

		// Connection successful
		logger.Info("connected to Elasticsearch successfully",
			slog.Int("attempt", i+1))
		break
	}

	indexer := &Indexer{
		config:      cfg,
		redisClient: redisClient,
		esClient:    esClient,
		bulkBuffer:  make([]BulkDocument, 0, cfg.BulkSize),
		logger:      logger,
	}

	// Create indices if they don't exist
	if err := indexer.createIndexIfNotExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to create secrets index: %w", err)
	}

	if err := indexer.createVulnerabilityIndexIfNotExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to create vulnerabilities index: %w", err)
	}

	return indexer, nil
}

// Start begins the indexer workers
func (i *Indexer) Start(ctx context.Context) error {
	// Log queue lengths at startup
	secretsQueueLen, err := i.redisClient.LLen(ctx, i.config.SecretsQueueName).Result()
	if err != nil {
		i.logger.Error("failed to get secrets queue length at startup", slog.String("error", err.Error()))
	} else {
		i.logger.Info("secrets queue length at startup",
			slog.Int64("items", secretsQueueLen),
			slog.String("queue", i.config.SecretsQueueName))
	}

	osvQueueLen, err := i.redisClient.LLen(ctx, i.config.OSVResultsQueueName).Result()
	if err != nil {
		i.logger.Error("failed to get OSV results queue length at startup", slog.String("error", err.Error()))
	} else {
		i.logger.Info("OSV results queue length at startup",
			slog.Int64("items", osvQueueLen),
			slog.String("queue", i.config.OSVResultsQueueName))
	}

	i.logger.Info("starting indexer workers",
		slog.Int("secrets_workers", i.config.MaxConcurrentWorkers),
		slog.Int("osv_workers", i.config.MaxConcurrentWorkers))

	// Start the flush timer
	i.startFlushTimer(ctx)

	var wg sync.WaitGroup

	// Start secrets worker goroutines
	for w := 0; w < i.config.MaxConcurrentWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			i.worker(ctx, workerID)
		}(w)
	}

	// Start OSV worker goroutines
	for w := 0; w < i.config.MaxConcurrentWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			i.osvWorker(ctx, workerID)
		}(w)
	}

	// Wait for all workers to complete
	wg.Wait()

	// Final flush of any remaining documents
	i.logger.Info("performing final flush before shutdown")
	i.flushBulkBuffer(context.Background()) // Use background context to ensure flush completes

	i.logger.Info("all indexer workers have stopped")
	return nil
}

// worker processes repository scan results from the Redis queue
func (i *Indexer) worker(ctx context.Context, workerID int) {
	ctx = logging.WithWorker(ctx, workerID)
	logger := logging.LoggerFromContext(ctx, i.logger)
	logger.Info("indexer worker started")
	defer logger.Info("indexer worker stopped")

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Block until a job is available or context is cancelled
			result, err := i.redisClient.BRPop(ctx, 0, i.config.SecretsQueueName).Result()
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

			// Parse scanned repository data
			var scannedRepo types.ScannedRepository
			if err := json.Unmarshal([]byte(result[1]), &scannedRepo); err != nil {
				logger.Error("failed to parse scanned repository data", slog.String("error", err.Error()))
				continue
			}

			// Index the repository scan results
			if err := i.indexRepository(ctx, workerID, &scannedRepo); err != nil {
				logger.Error("failed to index repository",
					slog.String("org", scannedRepo.Org),
					slog.String("repo", scannedRepo.Name),
					slog.String("error", err.Error()))
			}

			// Log queue length after job execution
			queueLen, err := i.redisClient.LLen(ctx, i.config.SecretsQueueName).Result()
			if err != nil {
				logger.Error("failed to get queue length after job", slog.String("error", err.Error()))
			} else {
				logger.Debug("queue length after job",
					slog.Int64("items", queueLen),
					slog.String("queue", i.config.SecretsQueueName))
			}
		}
	}
}

// osvWorker processes OSV scanner results from the Redis queue
func (i *Indexer) osvWorker(ctx context.Context, workerID int) {
	ctx = logging.WithWorker(ctx, workerID)
	logger := logging.LoggerFromContext(ctx, i.logger)
	logger.Info("OSV indexer worker started")
	defer logger.Info("OSV indexer worker stopped")

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Block until a job is available or context is cancelled
			result, err := i.redisClient.BRPop(ctx, 0, i.config.OSVResultsQueueName).Result()
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

			// Parse OSV scanned repository data
			var osvScannedRepo types.OSVScannedRepository
			if err := json.Unmarshal([]byte(result[1]), &osvScannedRepo); err != nil {
				logger.Error("failed to parse OSV scanned repository data", slog.String("error", err.Error()))
				continue
			}

			// Index the OSV scan results
			if err := i.indexOSVRepository(ctx, workerID, &osvScannedRepo); err != nil {
				logger.Error("failed to index OSV repository",
					slog.String("org", osvScannedRepo.Org),
					slog.String("repo", osvScannedRepo.Name),
					slog.String("error", err.Error()))
			}

			// Log queue length after job execution
			queueLen, err := i.redisClient.LLen(ctx, i.config.OSVResultsQueueName).Result()
			if err != nil {
				logger.Error("failed to get queue length after job", slog.String("error", err.Error()))
			} else {
				logger.Debug("queue length after job",
					slog.Int64("items", queueLen),
					slog.String("queue", i.config.OSVResultsQueueName))
			}
		}
	}
}

// indexRepository indexes the scan results for a repository
func (i *Indexer) indexRepository(ctx context.Context, workerID int, scannedRepo *types.ScannedRepository) error {
	startTime := time.Now()
	logger := logging.LoggerFromContext(ctx, i.logger)
	logger.Info("indexing repository",
		slog.String("org", scannedRepo.Org),
		slog.String("repo", scannedRepo.Name),
		slog.Int("secrets_found", scannedRepo.ValidSecretsFound))

	// If no secrets found, index a summary document
	if len(scannedRepo.ValidSecrets) == 0 {
		summaryDoc := ElasticsearchDocument{
			Organization: scannedRepo.Org,
			Repository:   scannedRepo.Name,
			SecretType:   "NO_SECRETS_FOUND",
			Description:  "No secrets found in repository",
			ScannedAt:    scannedRepo.ScannedAt,
			ProcessedAt:  scannedRepo.ProcessedAt,
			ScanStatus:   scannedRepo.ScanStatus,
			WorkerID:     scannedRepo.WorkerID,
		}

		docID := fmt.Sprintf("%s_%s_summary_%d", scannedRepo.Org, scannedRepo.Name, scannedRepo.ScannedAt.Unix())
		i.addToBulkBuffer(ctx, i.config.IndexName, docID, summaryDoc)
	} else {
		// Index each finding
		for idx, finding := range scannedRepo.ValidSecrets {
			doc := ElasticsearchDocument{
				Organization: scannedRepo.Org,
				Repository:   scannedRepo.Name,
				SecretType:   finding.SecretType,
				Description:  finding.Description,
				FilePath:     finding.File,
				LineNumber:   finding.Line,
				CommitHash:   finding.Commit,
				Confidence:   finding.Confidence,
				Validated:    finding.Validated,
				ScannedAt:    scannedRepo.ScannedAt,
				ProcessedAt:  scannedRepo.ProcessedAt,
				ScanStatus:   scannedRepo.ScanStatus,
				WorkerID:     scannedRepo.WorkerID,
			}

			// Generate unique document ID
			docID := fmt.Sprintf("%s_%s_%s_%d_%d", scannedRepo.Org, scannedRepo.Name, finding.Commit, finding.Line, idx)
			i.addToBulkBuffer(ctx, i.config.IndexName, docID, doc)
		}
	}

	logger.Info("repository indexed successfully",
		slog.String("org", scannedRepo.Org),
		slog.String("repo", scannedRepo.Name),
		slog.Int64("duration_ms", time.Since(startTime).Milliseconds()))
	return nil
}

// indexOSVRepository indexes the OSV scan results for a repository
func (i *Indexer) indexOSVRepository(ctx context.Context, workerID int, osvScannedRepo *types.OSVScannedRepository) error {
	startTime := time.Now()
	logger := logging.LoggerFromContext(ctx, i.logger)
	logger.Info("indexing OSV repository",
		slog.String("org", osvScannedRepo.Org),
		slog.String("repo", osvScannedRepo.Name),
		slog.Int("vulnerabilities_found", osvScannedRepo.VulnerabilitiesFound))

	// Count vulnerabilities by severity
	criticalCount := 0
	highCount := 0
	mediumCount := 0
	lowCount := 0
	unknownCount := 0

	// Convert OSV vulnerabilities to our indexing format and count severities
	vulnerabilities := make([]VulnerabilityDetail, 0, len(osvScannedRepo.Vulnerabilities))
	for _, vuln := range osvScannedRepo.Vulnerabilities {
		detail := VulnerabilityDetail{
			ID:          vuln.ID,
			Package:     vuln.Package,
			Version:     vuln.Version,
			Ecosystem:   vuln.Ecosystem,
			Severity:    vuln.Severity,
			Summary:     vuln.Summary,
			CVE:         vuln.CVE,
			CWE:         vuln.CWE,
			PackageFile: vuln.PackageFile,
		}
		vulnerabilities = append(vulnerabilities, detail)

		// Count by severity
		switch strings.ToUpper(vuln.Severity) {
		case "CRITICAL":
			criticalCount++
		case "HIGH":
			highCount++
		case "MEDIUM":
			mediumCount++
		case "LOW":
			lowCount++
		default:
			unknownCount++
		}
	}

	// Create the aggregated document for per-repository reporting
	doc := ElasticsearchVulnerabilityDocument{
		Organization:       osvScannedRepo.Org,
		Repository:         osvScannedRepo.Name,
		VulnerabilityCount: osvScannedRepo.VulnerabilitiesFound,
		CriticalCount:      criticalCount,
		HighCount:          highCount,
		MediumCount:        mediumCount,
		LowCount:           lowCount,
		UnknownCount:       unknownCount,
		ScannedAt:          osvScannedRepo.ScannedAt,
		ProcessedAt:        osvScannedRepo.ProcessedAt,
		ScanStatus:         osvScannedRepo.ScanStatus,
		ScanDuration:       osvScannedRepo.ScanDuration,
		WorkerID:           osvScannedRepo.WorkerID,
		ErrorMessage:       osvScannedRepo.ErrorMessage,
		Vulnerabilities:    vulnerabilities,
	}

	// Generate unique document ID (one doc per repo)
	docID := fmt.Sprintf("%s_%s_osv_%d", osvScannedRepo.Org, osvScannedRepo.Name, osvScannedRepo.ScannedAt.Unix())
	i.addToBulkBuffer(ctx, i.config.VulnerabilitiesIndexName, docID, doc)

	logger.Info("OSV repository indexed successfully",
		slog.String("org", osvScannedRepo.Org),
		slog.String("repo", osvScannedRepo.Name),
		slog.Int("total_vulnerabilities", osvScannedRepo.VulnerabilitiesFound),
		slog.Int("critical", criticalCount),
		slog.Int("high", highCount),
		slog.Int("medium", mediumCount),
		slog.Int("low", lowCount),
		slog.Int("unknown", unknownCount),
		slog.Int64("duration_ms", time.Since(startTime).Milliseconds()))
	return nil
}

// addToBulkBuffer adds a document to the bulk buffer
func (i *Indexer) addToBulkBuffer(ctx context.Context, index, id string, doc interface{}) {
	i.bufferMutex.Lock()

	i.bulkBuffer = append(i.bulkBuffer, BulkDocument{
		Index:    index,
		ID:       id,
		Document: doc,
	})

	i.logger.Debug("added document to buffer",
		slog.Int("buffer_size", len(i.bulkBuffer)),
		slog.Int("max_size", i.config.BulkSize))

	shouldFlush := len(i.bulkBuffer) >= i.config.BulkSize
	i.bufferMutex.Unlock()

	// Flush if buffer is full
	if shouldFlush {
		i.logger.Debug("buffer full, triggering flush")
		i.flushBulkBuffer(ctx)
	}
}

// flushBulkBuffer sends all buffered documents to Elasticsearch
func (i *Indexer) flushBulkBuffer(ctx context.Context) {
	i.bufferMutex.Lock()
	defer i.bufferMutex.Unlock()

	if len(i.bulkBuffer) == 0 {
		i.logger.Debug("flush called but buffer is empty")
		return
	}

	i.logger.Info("flushing documents to Elasticsearch",
		slog.Int("document_count", len(i.bulkBuffer)))

	// Build bulk request body
	var buf bytes.Buffer
	for _, doc := range i.bulkBuffer {
		// Add the action line
		meta := map[string]interface{}{
			"index": map[string]interface{}{
				"_index": doc.Index,
				"_id":    doc.ID,
			},
		}
		if err := json.NewEncoder(&buf).Encode(meta); err != nil {
			i.logger.Error("failed to encode bulk action", slog.String("error", err.Error()))
			continue
		}

		// Add the document
		if err := json.NewEncoder(&buf).Encode(doc.Document); err != nil {
			i.logger.Error("failed to encode document", slog.String("error", err.Error()))
			continue
		}
	}

	// Send bulk request
	req := esapi.BulkRequest{
		Body: &buf,
	}

	res, err := req.Do(ctx, i.esClient)
	if err != nil {
		i.logger.Error("failed to execute bulk request", slog.String("error", err.Error()))
		return
	}
	defer res.Body.Close()

	if res.IsError() {
		var errorResponse map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&errorResponse); err != nil {
			i.logger.Error("bulk request failed", slog.String("response", res.String()))
		} else {
			i.logger.Error("bulk request failed", slog.Any("error_response", errorResponse))
		}
		return
	}

	// Parse response to check for individual errors
	var bulkResponse map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&bulkResponse); err != nil {
		i.logger.Error("failed to parse bulk response", slog.String("error", err.Error()))
	} else {
		if errors, ok := bulkResponse["errors"].(bool); ok && errors {
			i.logger.Error("bulk request had errors", slog.Any("response", bulkResponse))
		}
	}

	// Clear the buffer
	docCount := len(i.bulkBuffer)
	i.bulkBuffer = i.bulkBuffer[:0]

	i.logger.Info("successfully indexed documents to Elasticsearch",
		slog.Int("document_count", docCount))
}

// startFlushTimer starts a timer to periodically flush the bulk buffer
func (i *Indexer) startFlushTimer(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(i.config.BulkFlushInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				i.flushBulkBuffer(ctx)
			}
		}
	}()
}

// createIndexIfNotExists creates the Elasticsearch index with proper mappings
func (i *Indexer) createIndexIfNotExists(ctx context.Context) error {
	// Check if index exists
	exists, err := i.esClient.Indices.Exists([]string{i.config.IndexName})
	if err != nil {
		return fmt.Errorf("failed to check if index exists: %w", err)
	}
	defer exists.Body.Close()

	if exists.StatusCode == 200 {
		i.logger.Info("index already exists", slog.String("index", i.config.IndexName))
		return nil
	}

	// Create index with mappings
	mapping := `{
		"mappings": {
			"properties": {
				"organization": { "type": "keyword" },
				"repository": { "type": "keyword" },
				"secret_type": { "type": "keyword" },
				"description": { "type": "text" },
				"file_path": { "type": "keyword" },
				"line_number": { "type": "integer" },
				"commit_hash": { "type": "keyword" },
				"confidence": { "type": "keyword" },
				"validated": { "type": "boolean" },
				"scanned_at": { "type": "date" },
				"processed_at": { "type": "date" },
				"scan_status": { "type": "keyword" },
				"worker_id": { "type": "integer" }
			}
		}
	}`

	req := esapi.IndicesCreateRequest{
		Index: i.config.IndexName,
		Body:  strings.NewReader(mapping),
	}

	res, err := req.Do(ctx, i.esClient)
	if err != nil {
		return fmt.Errorf("failed to create index: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("failed to create index: %s", res.String())
	}

	i.logger.Info("created index successfully", slog.String("index", i.config.IndexName))
	return nil
}

// createVulnerabilityIndexIfNotExists creates the Elasticsearch vulnerability index with proper mappings
func (i *Indexer) createVulnerabilityIndexIfNotExists(ctx context.Context) error {
	// Check if index exists
	exists, err := i.esClient.Indices.Exists([]string{i.config.VulnerabilitiesIndexName})
	if err != nil {
		return fmt.Errorf("failed to check if vulnerability index exists: %w", err)
	}
	defer exists.Body.Close()

	if exists.StatusCode == 200 {
		i.logger.Info("vulnerability index already exists", slog.String("index", i.config.VulnerabilitiesIndexName))
		return nil
	}

	// Create index with mappings optimized for vulnerability reporting
	mapping := `{
		"mappings": {
			"properties": {
				"organization": { "type": "keyword" },
				"repository": { "type": "keyword" },
				"vulnerability_count": { "type": "integer" },
				"critical_count": { "type": "integer" },
				"high_count": { "type": "integer" },
				"medium_count": { "type": "integer" },
				"low_count": { "type": "integer" },
				"unknown_count": { "type": "integer" },
				"scanned_at": { "type": "date" },
				"processed_at": { "type": "date" },
				"scan_status": { "type": "keyword" },
				"scan_duration": { "type": "long" },
				"worker_id": { "type": "integer" },
				"error_message": { "type": "text" },
				"vulnerabilities": {
					"type": "nested",
					"properties": {
						"id": { "type": "keyword" },
						"package": { "type": "keyword" },
						"version": { "type": "keyword" },
						"ecosystem": { "type": "keyword" },
						"severity": { "type": "keyword" },
						"summary": { "type": "text" },
						"cve": { "type": "keyword" },
						"cwe": { "type": "keyword" },
						"package_file": { "type": "keyword" }
					}
				}
			}
		}
	}`

	req := esapi.IndicesCreateRequest{
		Index: i.config.VulnerabilitiesIndexName,
		Body:  strings.NewReader(mapping),
	}

	res, err := req.Do(ctx, i.esClient)
	if err != nil {
		return fmt.Errorf("failed to create vulnerability index: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("failed to create vulnerability index: %s", res.String())
	}

	i.logger.Info("created vulnerability index successfully", slog.String("index", i.config.VulnerabilitiesIndexName))
	return nil
}

// Close cleanly shuts down the indexer
func (i *Indexer) Close() {
	if i.redisClient != nil {
		i.redisClient.Close()
	}
	if i.flushTimer != nil {
		i.flushTimer.Stop()
	}
}
