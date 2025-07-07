# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Heimdall Overview

Heimdall is a microservices-based security analysis pipeline for GitHub repositories consisting of seven main services:
- **Collector**: Fetches repositories from GitHub organizations on a schedule (cron)
- **Cloner**: Clones repositories to shared volume and performs initial analysis
- **Scanner-TruffleHog**: Performs deep secret scanning using TruffleHog with validation
- **Scanner-OSV**: Scans for known vulnerabilities using Google's OSV database
- **Coordinator**: Coordinates multiple scanners and triggers cleanup when all are done
- **Indexer**: Indexes findings into Elasticsearch for visualization
- **Cleaner**: Removes cloned repositories from shared volume after scanning

Services communicate through Redis queues in a pipeline:
- `clone_queue` → Cloner
- Cloner → `trufflehog_queue` (for Scanner-TruffleHog) + `osv_queue` (for Scanner-OSV)
- Scanner-TruffleHog → `trufflehog_results_queue` + `coordinator_queue`
- Scanner-OSV → `osv_results_queue` + `coordinator_queue`
- Coordinator → `cleanup_queue`
- Indexer consumes from `trufflehog_results_queue` and `osv_results_queue`

## Common Development Commands

### Building
```bash
# Build all services
make build-all

# Build specific service
make build-collector
make build-cloner
make build-scanner-trufflehog
make build-cleaner
make build-indexer
make build-scanner-osv
make build-coordinator

# Build Docker images
make docker-build        # all images
make docker-build-collector
make docker-build-cloner
make docker-build-scanner-trufflehog
make docker-build-cleaner
make docker-build-indexer
make docker-build-scanner-osv
make docker-build-coordinator

# Push Docker images to registry
make docker-push         # all images
make docker-push-collector
make docker-push-cloner
make docker-push-scanner-trufflehog
make docker-push-cleaner
make docker-push-indexer
make docker-push-scanner-osv
make docker-push-coordinator
```

### Testing
```bash
# Run unit tests
make test

# Run unit tests with coverage
make test-coverage

# Run integration tests
make test-integration

# Run all tests (unit + integration)
make test-all

# Run specific service tests
go test -v ./internal/collector/...
go test -v ./internal/cloner/...
go test -v ./internal/scanner-trufflehog/...
go test -v ./internal/cleaner/...
go test -v ./internal/indexer/...
go test -v ./internal/scanner-osv/...
go test -v ./internal/coordinator/...

# Generate coverage reports
make test-coverage      # Unit tests only
make test-coverage-all  # All tests including integration
```

### Running Locally
```bash
# Start Redis (required)
docker run -d -p 6379:6379 redis:alpine

# Run services (each in separate terminal)
make run-collector     # or go run ./cmd/collector
make run-cloner        # or go run ./cmd/cloner
make run-scanner-trufflehog   # or go run ./cmd/scanner-trufflehog
make run-scanner-osv          # or go run ./cmd/scanner-osv
make run-coordinator   # or go run ./cmd/coordinator
make run-indexer       # or go run ./cmd/indexer
make run-cleaner       # or go run ./cmd/cleaner

# Monitor queues and disk space
make run-monitor    # or go run ./cmd/monitor
```

### Docker Compose
```bash
# Set required environment variables
export GITHUB_ORG=your-org-name
export GITHUB_TOKEN=your-github-token  # Optional for public repos

# Start all services with dependencies (Redis, Elasticsearch, Kibana)
docker-compose up -d

# View logs
docker-compose logs -f collector
docker-compose logs -f scanner-trufflehog
docker-compose logs -f indexer

# Stop all services
docker-compose down

# View Kibana dashboards (after startup)
# http://localhost:5601
```

### Code Quality
```bash
# Format code
make fmt

# Lint code (requires golangci-lint)
make lint

# Full CI pipeline
make ci              # lint, test, build-all, docker-build
make ci-integration  # includes integration tests

# Other commands
make deps            # Install/update dependencies
make clean           # Clean build artifacts
```

## Architecture & Key Patterns

### Service Structure
Each service follows this pattern:
```
cmd/{service}/          # Entry point with main.go
internal/{service}/     # Service implementation
  ├── service.go        # Core service logic
  └── service_test.go   # Unit tests
internal/config/        # Shared configuration
deployments/{service}/  # Dockerfile
```

### Shared Volume Architecture
- Cloner writes cloned repositories to `/shared/heimdall-repos/{org}_{name}_{uuid}`
- All scanners read from the shared volume path provided in ProcessedRepository
- Cleaner removes directories after scanning via cleanup_queue
- All services validate paths are within the shared volume directory
- Docker Compose includes an `init-volume` service to set up proper permissions
- Shared volume mounted at `/shared/heimdall-repos` with bind mount to host directory

### Coordinator Pattern
The Coordinator service ensures proper cleanup timing:
- Tracks scanning jobs from multiple scanners (TruffleHog, OSV)
- Each scanner reports completion to `coordinator_queue`
- Only triggers cleanup via `cleanup_queue` when all scanners finish
- Implements timeout handling for stuck jobs
- Periodically cleans up stale job state (`STATE_CLEANUP_INTERVAL`)

### Configuration Pattern
All services use environment variables loaded through `internal/config/`:
- Each service has its own config struct
- Default values provided
- Redis connection shared across services

### Worker Pool Pattern
All processing services use concurrent worker pools:
- Configurable worker count:
  - Cloner: `MAX_CONCURRENT_CLONES` (default: 5)
  - Scanner-TruffleHog: `MAX_CONCURRENT_SCANS` (default: 3)
  - Scanner-OSV: `MAX_CONCURRENT_SCANS` (default: 3)
  - Cleaner: `MAX_CONCURRENT_JOBS` (default: 2)
  - Indexer: `MAX_CONCURRENT_WORKERS` (default: 2)
- Pull from input queue, process, push to output queue
- Graceful shutdown handling with context cancellation

### Queue Message Flow
1. Collector → `clone_queue`: Repository to clone
2. Cloner → `trufflehog_queue` & `osv_queue`: Adds processing metadata and `clone_path`
3. Scanner-TruffleHog → `trufflehog_results_queue` & `coordinator_queue`: Adds scan results with validated secrets
4. Scanner-OSV → `osv_results_queue` & `coordinator_queue`: Adds vulnerability findings
5. Indexer: Consumes from `trufflehog_results_queue` & `osv_results_queue` → Elasticsearch
6. Coordinator → `cleanup_queue`: Triggers cleanup when all scanners complete
7. Cleaner: Consumes from `cleanup_queue` and removes directories

### Error Handling
- Services log errors but continue processing other items
- Failed items are not retried (consider implementing DLQ)
- Timeouts configured for long operations:
  - Scanner-TruffleHog: 30min default (`SCAN_TIMEOUT_MINUTES`)
  - Scanner-OSV: 30min default (`SCAN_TIMEOUT_MINUTES`)
  - Coordinator job timeout: 60min default (`JOB_TIMEOUT_MINUTES`)

## Important Considerations

### Security
- All containers run as non-root user (UID 1000)
- GitHub tokens required for private repositories
- Scanner-TruffleHog validates secrets using TruffleHog verification to reduce false positives
- Cleaner validates paths are within shared volume before deletion
- Automatic cleanup of cloned repositories via queue-based system
- Shared volume permissions set by init-volume container

### Performance
- Shared volume architecture allows efficient disk usage
- Scanners read directly from cloner's output (no re-cloning)
- Concurrent processing with configurable limits
- API rate limiting support via `GITHUB_API_DELAY_MS`
- Automatic cleanup prevents disk space exhaustion
- TruffleHog concurrency configurable via `TRUFFLEHOG_CONCURRENCY` (default: 8)
- Scanner-TruffleHog supports `TRUFFLEHOG_ONLY_VERIFIED` to reduce false positives
- Cloner fetches ALL branches and tags for comprehensive scanning
- Elasticsearch bulk indexing with configurable `BULK_SIZE` (default: 50) and `BULK_FLUSH_INTERVAL`
- Coordinator ensures cleanup only after all scanners complete

### Dependencies
- **Redis**: All services require Redis connection for queue communication
  - No built-in retry mechanism for queue operations
  - Queue names configurable via environment variables
  - Monitor tool helpful for debugging queue issues
- **Elasticsearch**: Required by Indexer service for storing findings
  - Default URL: `http://localhost:9200`
  - Indices: `heimdall-secrets` and `heimdall-vulnerabilities`
- **Docker**: Shared volume requires proper permissions (handled by init-volume)
- **External Tools**:
  - TruffleHog installed in Scanner-TruffleHog container
  - OSV Scanner installed in Scanner-OSV container

### Testing Notes
- Unit tests use mocks for external dependencies
- Integration tests require actual Redis instance
- CI runs on PRs (unit tests) and tags (build/push)
- Test coverage available via `make test-coverage-all`
- Tests expect Redis to be running locally

### CI/CD Pipeline
- GitHub Actions workflows for testing and deployment
- PRs trigger unit tests only
- Version tags (`v*`) trigger multi-platform Docker builds and push
- Images pushed to GitHub Container Registry (`ghcr.io/klimeurt`)
- Version automatically embedded from git tags