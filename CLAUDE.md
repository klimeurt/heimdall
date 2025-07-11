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

# Run sync service only (no scanner queues)
ENABLE_SCANNER_QUEUES=false make run-sync

# Run sync + specific scanners only
ENABLE_OSV_QUEUE=false make run-sync           # Only TruffleHog scanner
ENABLE_TRUFFLEHOG_QUEUE=false make run-sync    # Only OSV scanner
```

### Docker Compose
```bash
# Set required environment variables
export GITHUB_ORG=your-org-name
export GITHUB_TOKEN=your-github-token  # Optional for public repos

# Start all services with dependencies (Redis, Elasticsearch, Kibana)
docker-compose up -d

# Run sync service only (no scanners)
ENABLE_SCANNER_QUEUES=false docker-compose up -d sync redis init-volume

# Run sync + only TruffleHog scanner  
ENABLE_OSV_QUEUE=false docker-compose up -d sync scanner-trufflehog redis init-volume

# Run sync + only OSV scanner
ENABLE_TRUFFLEHOG_QUEUE=false docker-compose up -d sync scanner-osv redis init-volume

# View logs
docker-compose logs -f sync
docker-compose logs -f scanner-trufflehog
docker-compose logs -f indexer

# Stop all services
docker-compose down

# View Kibana dashboards (after startup)
# http://localhost:5601
```

### Scanner Queue Configuration
The sync service can optionally push repositories to scanner queues. By default, all queues are enabled for backward compatibility.

**Environment Variables:**
- `ENABLE_SCANNER_QUEUES` - Global toggle to enable/disable all scanner queue pushes (default: true)
- `ENABLE_TRUFFLEHOG_QUEUE` - Individual control for TruffleHog queue (default: true)
- `ENABLE_OSV_QUEUE` - Individual control for OSV queue (default: true)

**Usage Examples:**
```bash
# Sync only (no scanners)
ENABLE_SCANNER_QUEUES=false

# Sync + TruffleHog scanner only
ENABLE_OSV_QUEUE=false

# Sync + OSV scanner only  
ENABLE_TRUFFLEHOG_QUEUE=false

# All scanners (default behavior)
# No additional configuration needed
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
1. Sync → `trufflehog_queue` & `osv_queue`: Conditionally pushes repositories based on scanner queue configuration
2. Scanner-TruffleHog → `trufflehog_results_queue`: Adds scan results with validated secrets
3. Scanner-OSV → `osv_results_queue`: Adds vulnerability findings
4. Indexer: Consumes from `trufflehog_results_queue` & `osv_results_queue` → Elasticsearch

**Scanner Queue Configuration:**
- Sync service can run independently without pushing to scanner queues
- Individual scanner queues can be enabled/disabled independently
- Default behavior maintains backward compatibility (all queues enabled)

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

## Service Dependencies and Optional Components

### Core Components
- **Sync Service**: The primary service that maintains repository mirrors. Can run independently.
- **Redis**: Required by all services for queue communication and coordination.

### Optional Scanner Components
- **Scanner-TruffleHog**: Secret scanning service. Only runs if `ENABLE_TRUFFLEHOG_QUEUE=true` in sync service.
- **Scanner-OSV**: Vulnerability scanning service. Only runs if `ENABLE_OSV_QUEUE=true` in sync service.
- **Indexer**: Elasticsearch indexing service. Only needed if scanner results need to be stored/visualized.

### Optional Visualization Components
- **Elasticsearch**: Required only if using the indexer service for storing scan results.
- **Kibana**: Optional dashboard for visualizing scan results stored in Elasticsearch.

This architecture allows you to run only the components you need:
- **Sync Only**: Just maintain repository mirrors without scanning
- **Sync + Single Scanner**: Run only one type of scanner (secrets OR vulnerabilities)
- **Full Pipeline**: Run all components for complete security analysis and visualization