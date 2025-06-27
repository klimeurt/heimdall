# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Heimdall Overview

Heimdall is a microservices-based security analysis pipeline for GitHub repositories consisting of four main services:
- **Collector**: Fetches repositories from GitHub organizations on a schedule
- **Cloner**: Clones repositories to shared volume and performs initial analysis
- **Scanner**: Performs deep secret scanning using TruffleHog with validation
- **Cleaner**: Removes cloned repositories from shared volume after scanning

Services communicate through Redis queues: `clone_queue` → `processed_queue` → `secrets_queue` → `cleanup_queue`

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

# Build Docker images
make docker-build        # all images
make docker-build-collector
make docker-build-cloner
make docker-build-scanner
make docker-build-cleaner

# Push Docker images to registry
make docker-push         # all images
make docker-push-collector
make docker-push-cloner
make docker-push-scanner
make docker-push-cleaner
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

# Generate coverage reports
make test-coverage      # Unit tests only
make test-coverage-all  # All tests including integration
```

### Running Locally
```bash
# Start Redis (required)
docker run -d -p 6379:6379 redis:alpine

# Run services (each in separate terminal)
make run-collector  # or go run ./cmd/collector
make run-cloner     # or go run ./cmd/cloner
make run-scanner    # or go run ./cmd/scanner
make run-cleaner    # or go run ./cmd/cleaner

# Monitor queues and disk space
make run-monitor    # or go run ./cmd/monitor
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

### Worker Pool Pattern
All processing services use concurrent worker pools:
- Configurable worker count (`MAX_CONCURRENT_CLONES`, `MAX_CONCURRENT_SCANS`, `MAX_CONCURRENT_JOBS`)
- Pull from input queue, process, push to output queue
- Graceful shutdown handling

### Queue Message Flow
1. Collector → `clone_queue`: `{"org": "name", "name": "repo"}`
2. Cloner → `processed_queue`: Adds processing metadata and `clone_path`
3. Scanner → `secrets_queue`: Adds scan results with validated secrets
4. Scanner → `cleanup_queue`: Sends cleanup job with `clone_path` and metadata
5. Cleaner: Consumes from `cleanup_queue` and removes directories

### Error Handling
- Services log errors but continue processing other items
- Failed items are not retried (consider implementing DLQ)
- Timeouts configured for long operations (scanner: 30min default)

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