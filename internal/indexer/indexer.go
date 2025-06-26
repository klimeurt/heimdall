package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/klimeurt/heimdall/internal/collector"
	"github.com/klimeurt/heimdall/internal/config"
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

// New creates a new Indexer instance
func New(cfg *config.IndexerConfig) (*Indexer, error) {
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

	// Create Elasticsearch client
	esConfig := elasticsearch.Config{
		Addresses: []string{cfg.ElasticsearchURL},
	}
	esClient, err := elasticsearch.NewClient(esConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Elasticsearch client: %w", err)
	}

	// Test Elasticsearch connection
	res, err := esClient.Info()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Elasticsearch: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("Elasticsearch returned error: %s", res.String())
	}

	log.Printf("Connected to Elasticsearch successfully")

	indexer := &Indexer{
		config:      cfg,
		redisClient: redisClient,
		esClient:    esClient,
		bulkBuffer:  make([]BulkDocument, 0, cfg.BulkSize),
	}

	// Create index if it doesn't exist
	if err := indexer.createIndexIfNotExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to create index: %w", err)
	}

	return indexer, nil
}

// Start begins the indexer workers
func (i *Indexer) Start(ctx context.Context) error {
	// Log queue length at startup
	queueLen, err := i.redisClient.LLen(ctx, i.config.SecretsQueueName).Result()
	if err != nil {
		log.Printf("Failed to get queue length at startup: %v", err)
	} else {
		log.Printf("Queue length at startup: %d items", queueLen)
	}

	log.Printf("Starting %d indexer workers", i.config.MaxConcurrentWorkers)

	// Start the flush timer
	i.startFlushTimer(ctx)

	var wg sync.WaitGroup

	// Start worker goroutines
	for w := 0; w < i.config.MaxConcurrentWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			i.worker(ctx, workerID)
		}(w)
	}

	// Wait for all workers to complete
	wg.Wait()

	// Final flush of any remaining documents
	i.flushBulkBuffer(ctx)

	log.Println("All indexer workers have stopped")
	return nil
}

// worker processes repository scan results from the Redis queue
func (i *Indexer) worker(ctx context.Context, workerID int) {
	log.Printf("Indexer Worker %d started", workerID)
	defer log.Printf("Indexer Worker %d stopped", workerID)

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
				log.Printf("Indexer Worker %d: Redis error: %v", workerID, err)
				continue
			}

			// result[0] is the queue name, result[1] is the data
			if len(result) < 2 {
				log.Printf("Indexer Worker %d: Invalid Redis result", workerID)
				continue
			}

			// Parse scanned repository data
			var scannedRepo collector.ScannedRepository
			if err := json.Unmarshal([]byte(result[1]), &scannedRepo); err != nil {
				log.Printf("Indexer Worker %d: Failed to parse scanned repository data: %v", workerID, err)
				continue
			}

			// Index the repository scan results
			if err := i.indexRepository(ctx, workerID, &scannedRepo); err != nil {
				log.Printf("Indexer Worker %d: Failed to index repository %s/%s: %v", workerID, scannedRepo.Org, scannedRepo.Name, err)
			}

			// Log queue length after job execution
			queueLen, err := i.redisClient.LLen(ctx, i.config.SecretsQueueName).Result()
			if err != nil {
				log.Printf("Indexer Worker %d: Failed to get queue length after job: %v", workerID, err)
			} else {
				log.Printf("Indexer Worker %d: Queue length after job: %d items", workerID, queueLen)
			}
		}
	}
}

// indexRepository indexes the scan results for a repository
func (i *Indexer) indexRepository(ctx context.Context, workerID int, scannedRepo *collector.ScannedRepository) error {
	startTime := time.Now()
	log.Printf("Indexer Worker %d: Indexing repository %s/%s with %d secrets", workerID, scannedRepo.Org, scannedRepo.Name, scannedRepo.ValidSecretsFound)

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

	log.Printf("Indexer Worker %d: Repository %s/%s indexed successfully in %v", workerID, scannedRepo.Org, scannedRepo.Name, time.Since(startTime))
	return nil
}

// addToBulkBuffer adds a document to the bulk buffer
func (i *Indexer) addToBulkBuffer(ctx context.Context, index, id string, doc interface{}) {
	i.bufferMutex.Lock()
	defer i.bufferMutex.Unlock()

	i.bulkBuffer = append(i.bulkBuffer, BulkDocument{
		Index:    index,
		ID:       id,
		Document: doc,
	})

	// Flush if buffer is full
	if len(i.bulkBuffer) >= i.config.BulkSize {
		i.flushBulkBuffer(ctx)
	}
}

// flushBulkBuffer sends all buffered documents to Elasticsearch
func (i *Indexer) flushBulkBuffer(ctx context.Context) {
	i.bufferMutex.Lock()
	defer i.bufferMutex.Unlock()

	if len(i.bulkBuffer) == 0 {
		return
	}

	log.Printf("Flushing %d documents to Elasticsearch", len(i.bulkBuffer))

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
			log.Printf("Failed to encode bulk action: %v", err)
			continue
		}

		// Add the document
		if err := json.NewEncoder(&buf).Encode(doc.Document); err != nil {
			log.Printf("Failed to encode document: %v", err)
			continue
		}
	}

	// Send bulk request
	req := esapi.BulkRequest{
		Body: &buf,
	}

	res, err := req.Do(ctx, i.esClient)
	if err != nil {
		log.Printf("Failed to execute bulk request: %v", err)
		return
	}
	defer res.Body.Close()

	if res.IsError() {
		log.Printf("Bulk request failed: %s", res.String())
		return
	}

	// Clear the buffer
	i.bulkBuffer = i.bulkBuffer[:0]

	log.Printf("Successfully indexed documents to Elasticsearch")
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
		log.Printf("Index %s already exists", i.config.IndexName)
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

	log.Printf("Created index %s successfully", i.config.IndexName)
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