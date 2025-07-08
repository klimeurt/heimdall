# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Heimdall Overview

Heimdall is a microservices-based security analysis pipeline for GitHub repositories consisting of four main services:
- **Sync**: Maintains a full mirror of the GitHub organization on shared volume
- **Scanner-TruffleHog**: Performs deep secret scanning using TruffleHog with validation
- **Scanner-OSV**: Scans for known vulnerabilities using Google's OSV database
- **Indexer**: Indexes findings into Elasticsearch for visualization

Services communicate through Redis queues in a pipeline:
- Sync → `trufflehog_queue` + `osv_queue` (after synchronization)
- Scanner-TruffleHog → `trufflehog_results_queue`
- Scanner-OSV → `osv_results_queue`
- Indexer consumes from `trufflehog_results_queue` and `osv_results_queue`

## Common Development Commands

### Building
```bash
# Build all services
make build-all

# Build specific service
make build-sync
make build-scanner-trufflehog
make build-indexer
make build-scanner-osv

# Build Docker images
make docker-build        # all images
make docker-build-sync
make docker-build-scanner-trufflehog
make docker-build-indexer
make docker-build-scanner-osv

# Push Docker images to registry
make docker-push         # all images
make docker-push-sync
make docker-push-scanner-trufflehog
make docker-push-indexer
make docker-push-scanner-osv
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
go test -v ./internal/sync/...
go test -v ./internal/scanner-trufflehog/...
go test -v ./internal/indexer/...
go test -v ./internal/scanner-osv/...

# Generate coverage reports
make test-coverage      # Unit tests only
make test-coverage-all  # All tests including integration
```

### Running Locally
```bash
# Start Redis (required)
docker run -d -p 6379:6379 redis:alpine

# Run services (each in separate terminal)
make run-sync          # or go run ./cmd/sync
make run-scanner-trufflehog   # or go run ./cmd/scanner-trufflehog
make run-scanner-osv          # or go run ./cmd/scanner-osv
make run-indexer       # or go run ./cmd/indexer

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
docker-compose logs -f sync
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
- Sync maintains repositories at `/shared/heimdall-repos/{org}/{name}` (predictable paths)
- All scanners read from the shared volume path provided in ProcessedRepository
- Sync automatically removes repositories no longer in the organization
- All services validate paths are within the shared volume directory
- Docker Compose includes an `init-volume` service to set up proper permissions
- Shared volume mounted at `/shared/heimdall-repos` with bind mount to host directory

### Sync Pattern
The Sync service maintains a full mirror of the GitHub organization:
- Runs on a cron schedule with immediate execution on startup
- Clones missing repositories with all branches and tags
- Updates existing repositories using `git fetch --all --tags --prune`
- Automatically recovers from fetch/pull failures by deleting and re-cloning the repository
- Removes repositories no longer in the organization
- Pushes all synced repositories to scanner queues after synchronization

### Configuration Pattern
All services use environment variables loaded through `internal/config/`:
- Each service has its own config struct
- Default values provided
- Redis connection shared across services

### Worker Pool Pattern
All processing services use concurrent worker pools:
- Configurable worker count:
  - Sync: `MAX_CONCURRENT_SYNCS` (default: 3)
  - Scanner-TruffleHog: `MAX_CONCURRENT_SCANS` (default: 3)
  - Scanner-OSV: `MAX_CONCURRENT_SCANS` (default: 3)
  - Indexer: `MAX_CONCURRENT_WORKERS` (default: 2)
- Pull from input queue, process, push to output queue
- Graceful shutdown handling with context cancellation

### Queue Message Flow
1. Sync → `trufflehog_queue` & `osv_queue`: Pushes all repositories after synchronization
2. Scanner-TruffleHog → `trufflehog_results_queue`: Adds scan results with validated secrets
3. Scanner-OSV → `osv_results_queue`: Adds vulnerability findings
4. Indexer: Consumes from `trufflehog_results_queue` & `osv_results_queue` → Elasticsearch

### Error Handling
- Services log errors but continue processing other items
- Failed items are not retried (consider implementing DLQ)
- Timeouts configured for long operations:
  - Sync: 30min default per repository (`SYNC_TIMEOUT_MINUTES`)
  - Scanner-TruffleHog: 30min default (`SCAN_TIMEOUT_MINUTES`)
  - Scanner-OSV: 30min default (`SCAN_TIMEOUT_MINUTES`)

## Important Considerations

### Security
- All containers run as non-root user (UID 1000)
- GitHub tokens required for private repositories
- Scanner-TruffleHog validates secrets using TruffleHog verification to reduce false positives
- Sync validates paths are within shared volume before deletion
- Automatic cleanup of orphaned repositories during sync
- Shared volume permissions set by init-volume container

### Performance
- Shared volume architecture allows efficient disk usage
- Persistent repository storage (no re-cloning, only fetching updates)
- Concurrent processing with configurable limits
- API rate limiting support via `GITHUB_API_DELAY_MS`
- Automatic cleanup of orphaned repositories during sync
- TruffleHog concurrency configurable via `TRUFFLEHOG_CONCURRENCY` (default: 8)
- Scanner-TruffleHog supports `TRUFFLEHOG_ONLY_VERIFIED` to reduce false positives
- Sync maintains ALL branches and tags for comprehensive scanning
- Elasticsearch bulk indexing with configurable `BULK_SIZE` (default: 50) and `BULK_FLUSH_INTERVAL`

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
- Services: sync, scanner-trufflehog, scanner-osv, indexer