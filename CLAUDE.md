# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

**Language**: Go 1.24

## Heimdall Overview

Heimdall is a microservices-based security analysis pipeline for GitHub repositories consisting of seven main services:
- **Collector**: Fetches repositories from GitHub organizations on a schedule (cron-based via COLLECTION_SCHEDULE)
- **Cloner**: Clones repositories to shared volume and performs initial analysis
- **Scanner (TruffleHog)**: Performs deep secret scanning using TruffleHog with validation
- **OSV Scanner**: Performs vulnerability scanning using Google's OSV scanner
- **Coordinator**: Manages parallel scanner coordination and synchronization
- **Indexer**: Pushes scan results to Elasticsearch for analysis
- **Cleaner**: Removes cloned repositories from shared volume after all scanning completes

Services communicate through Redis queues with parallel processing:
```
collector → clone_queue → cloner → processed_queue → ┌─→ scanner → secrets_queue ─┐
                                                      └─→ osv-scanner → osv_queue ─┘
                                                                             ↓
                                   coordinator_queue ← coordinator ← both scanners
                                            ↓
                                      cleanup_queue → cleaner
```

For a visual architecture diagram, see the mermaid diagram in README.md.

## Common Development Commands

### Building
```bash
# Build all services
make build-all

# Build specific service
make build-collector
make build-cloner
make build-scanner
make build-cleaner
make build-osv-scanner
make build-coordinator
make build-indexer

# Build Docker images
make docker-build        # all images
make docker-build-collector
make docker-build-cloner
make docker-build-scanner       # builds heimdall-scanner-trufflehog image
make docker-build-cleaner
make docker-build-osv-scanner   # builds heimdall-scanner-osv image
make docker-build-coordinator
make docker-build-indexer

# Push Docker images to registry
make docker-push         # all images
make docker-push-collector
make docker-push-cloner
make docker-push-scanner
make docker-push-cleaner
make docker-push-osv-scanner
make docker-push-coordinator
make docker-push-indexer
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
go test -v ./internal/scanner/...
go test -v ./internal/osv_scanner/...
go test -v ./internal/coordinator/...
go test -v ./internal/indexer/...

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
make run-scanner       # or go run ./cmd/scanner
make run-osv-scanner   # or go run ./cmd/osv-scanner
make run-coordinator   # or go run ./cmd/coordinator
make run-indexer       # or go run ./cmd/indexer
make run-cleaner       # or go run ./cmd/cleaner

# Monitor queues and disk space
make run-monitor       # or go run ./cmd/monitor
```

### Docker Compose
```bash
# Set required environment variables
export GITHUB_ORG=your-org-name
export GITHUB_TOKEN=your-github-token  # Optional for public repos

# Start all services
docker-compose up -d

# View logs
docker-compose logs -f collector
docker-compose logs -f scanner

# Stop all services
docker-compose down
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
- Scanner reads from the shared volume path provided in ProcessedRepository
- Cleaner removes directories after scanning via cleanup_queue
- All services validate paths are within the shared volume directory
- Docker Compose includes an `init-volume` service to set up proper permissions
- Shared volume mounted at `/shared/heimdall-repos` with bind mount to host directory

### Configuration Pattern
All services use environment variables loaded through `internal/config/`:
- Each service has its own config struct
- Default values provided
- Redis connection shared across services
- Key environment variables:
  - `COLLECTION_SCHEDULE`: Cron expression for collector (default: "0 0 * * *")
  - `GITHUB_ORG`: GitHub organization to scan
  - `GITHUB_TOKEN`: GitHub access token for API calls
  - `GITHUB_API_DELAY_MS`: Delay between API calls for rate limiting
  - `REDIS_URL`: Redis connection string (default: "redis://localhost:6379")
  - `SHARED_VOLUME_PATH`: Path for cloned repositories (default: "/shared/heimdall-repos")

### Worker Pool Pattern
All processing services use concurrent worker pools:
- Configurable worker count (`MAX_CONCURRENT_CLONES`, `MAX_CONCURRENT_SCANS`, `MAX_CONCURRENT_JOBS`)
- Pull from input queue, process, push to output queue
- Graceful shutdown handling

### Queue Message Flow
1. Collector → `clone_queue`: `{"org": "name", "name": "repo"}`
2. Cloner → `processed_queue`: Adds processing metadata and `clone_path`
3. Parallel processing:
   - Scanner → `secrets_queue`: Adds scan results with validated secrets
   - OSV Scanner → `osv_queue`: Adds vulnerability scan results
4. Both scanners → `coordinator_queue`: Send completion signals
5. Coordinator → `cleanup_queue`: Sends cleanup job after both scanners complete
6. Cleaner: Consumes from `cleanup_queue` and removes directories
7. Indexer: Monitors `secrets_queue` and `osv_queue` to push results to Elasticsearch

### Error Handling
- Services log errors but continue processing other items
- Failed items are not retried (consider implementing DLQ)
- Timeouts configured for long operations (scanner: 30min default)

### Elasticsearch/Kibana Integration
- Indexer pushes results to two Elasticsearch indices:
  - `heimdall-secrets`: Contains discovered secrets with validation status
  - `heimdall-vulnerabilities`: Contains OSV scanner vulnerability findings
- Kibana available at http://localhost:5601 for visualization
- Index mapping templates automatically applied by indexer
- Document structure includes repository metadata, timestamps, and scan results
- Environment variables:
  - `ELASTICSEARCH_URL`: Elasticsearch endpoint (default: http://elasticsearch:9200)
  - `ELASTICSEARCH_SECRETS_INDEX`: Index for secrets (default: heimdall-secrets)
  - `ELASTICSEARCH_VULNS_INDEX`: Index for vulnerabilities (default: heimdall-vulnerabilities)

## Important Considerations

### Security
- All containers run as non-root user (UID 1000)
- GitHub tokens required for private repositories
- Scanner validates secrets using TruffleHog verification to reduce false positives
- Cleaner validates paths are within shared volume before deletion
- Automatic cleanup of cloned repositories via queue-based system
- Shared volume permissions set by init-volume container

### Performance
- Shared volume architecture allows efficient disk usage
- Scanner reads directly from cloner's output (no re-cloning)
- Concurrent processing with configurable limits
- API rate limiting support via `GITHUB_API_DELAY_MS`
- Automatic cleanup prevents disk space exhaustion
- TruffleHog concurrency configurable via `TRUFFLEHOG_CONCURRENCY`
- Scanner supports `TRUFFLEHOG_ONLY_VERIFIED` to reduce noise
- Cloner fetches ALL branches and tags for comprehensive scanning

### Redis Dependencies
- All services require Redis connection
- No built-in retry mechanism for queue operations
- Queue names configurable via environment variables
- Monitor tool helpful for debugging queue issues

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