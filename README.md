# Heimdall - Security Analysis Pipeline

A microservices-based system for collecting, processing, and analyzing GitHub repositories for security insights. The pipeline consists of four services that work together through Redis queues to provide comprehensive repository analysis with secret scanning capabilities and automatic cleanup.

## Features

- **Microservices Architecture**: Four specialized services working together through Redis queues
- **Scheduled Collection**: Collector service uses cron-style scheduling (customizable, defaults to weekly)
- **Shared Volume Architecture**: Cloner service writes repositories to shared volume for efficient processing
- **Secret Scanning**: Scanner service performs full Git history analysis using TruffleHog with validation
- **Automatic Cleanup**: Cleaner service removes cloned repositories after scanning
- **GitHub Integration**: Fetches all repositories from a specified organization
- **Queue-Based Communication**: Reliable Redis-based message passing between services
- **Container Ready**: All services deployable as Docker containers
- **Secure**: All containers run as non-root user, support security contexts
- **Configurable**: Environment variable based configuration for all services
- **Scalable**: Worker pool architecture for cloner and scanner services

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│                 │     │                 │     │                 │
│  GitHub API     │◄────│   Collector     │────►│  Redis Queue    │
│                 │     │   Service       │     │  (clone_queue)  │
└─────────────────┘     └─────────────────┘     └─────────────────┘
                              │                         │
                              ▼                         ▼
                        ┌─────────────┐         ┌─────────────────┐
                        │   Cron      │         │                 │
                        │  Scheduler  │         │  Cloner Service │
                        └─────────────┘         │  (Worker Pool)  │
                                                └─────────────────┘
                                                        │
                                                        ▼
                                                ┌─────────────────┐
                                                │  Redis Queue    │
                                                │(processed_queue)│
                                                └─────────────────┘
                                                        │
                                                        ▼
                                                ┌─────────────────┐
                                                │ Scanner Service │
                                                │ (Worker Pool)   │
                                                │ with Kingfisher │
                                                └─────────────────┘
                                                        │
                                                        ▼
                                                ┌─────────────────┐
                                                │  Redis Queue    │
                                                │(secrets_queue)  │
                                                └─────────────────┘
                                                        │
                                                        ▼
                                                ┌─────────────────┐
                                                │  Redis Queue    │
                                                │(cleanup_queue)  │
                                                └─────────────────┘
                                                        │
                                                        ▼
                                                ┌─────────────────┐
                                                │ Cleaner Service │
                                                │ (Worker Pool)   │
                                                └─────────────────┘
```

## Data Flow

1. **Collector Service**: Periodically collects GitHub organization repositories and pushes them to Redis `clone_queue`
2. **Cloner Service**: Worker pool pulls repositories from `clone_queue`, clones them to shared volume, analyzes them, and pushes results to `processed_queue`
3. **Scanner Service**: Worker pool pulls from `processed_queue`, reads repositories from shared volume, scans for secrets using TruffleHog with validation, and pushes findings to `secrets_queue`
4. **Cleaner Service**: Worker pool pulls from `cleanup_queue` and removes cloned repositories from shared volume after scanning

## Queue Message Formats

### Clone Queue (Input)
```json
{
  "org": "organization-name",
  "name": "repository-name"
}
```

### Processed Queue (Cloner Output)
```json
{
  "org": "organization-name",
  "name": "repository-name",
  "processed_at": "2025-06-23T10:30:00Z",
  "worker_id": 1,
  "clone_path": "/shared/heimdall-repos/organization-name_repository-name_abc123"
}
```

### Secrets Queue (Scanner Output)
```json
{
  "org": "organization-name",
  "name": "repository-name",
  "processed_at": "2025-06-23T10:30:00Z",
  "scanned_at": "2025-06-23T10:35:00Z",
  "worker_id": 2,
  "valid_secrets_found": 2,
  "scan_status": "success",
  "scan_duration": "45s",
  "clone_path": "/shared/heimdall-repos/organization-name_repository-name_abc123",
  "valid_secrets": [
    {
      "secret_type": "aws_access_key",
      "description": "AWS Access Key",
      "file": "config/aws.py",
      "line": 42,
      "commit": "abc123def456",
      "confidence": "high",
      "validated": true,
      "service": "aws",
      "verification_error": ""
    }
  ]
}
```

### Cleanup Queue (Scanner to Cleaner)
```json
{
  "clone_path": "/shared/heimdall-repos/organization-name_repository-name_abc123",
  "repo_name": "organization-name/repository-name",
  "created_at": "2025-06-23T10:35:00Z"
}
```

## Configuration

**Note**: The `GITHUB_TOKEN` environment variable is optional for all services. When not provided, the services will only be able to access public repositories. For private repository access, a GitHub personal access token is required.

### Collector Service Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `GITHUB_ORG` | GitHub organization name | - | Yes |
| `GITHUB_TOKEN` | GitHub personal access token (only needed for private repos) | - | No |
| `HTTP_ENDPOINT` | Legacy - not used with Redis | `http://localhost:8080/repositories` | No |
| `CRON_SCHEDULE` | Cron schedule expression | `0 0 * * 0` (weekly) | No |
| `RUN_ON_STARTUP` | Run collection immediately on startup | `false` | No |
| `GITHUB_PAGE_SIZE` | Repositories per API request (1-100) | `100` | No |
| `GITHUB_API_DELAY_MS` | Delay between GitHub API requests | `0` | No |
| `REDIS_HOST` | Redis server hostname | `localhost` | No |
| `REDIS_PORT` | Redis server port | `6379` | No |
| `REDIS_PASSWORD` | Redis password | - | No |
| `REDIS_DB` | Redis database number | `0` | No |

### Cloner Service Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `GITHUB_TOKEN` | GitHub token for private repos | - | No |
| `REDIS_HOST` | Redis server hostname | `localhost` | No |
| `REDIS_PORT` | Redis server port | `6379` | No |
| `REDIS_PASSWORD` | Redis password | - | No |
| `REDIS_DB` | Redis database number | `0` | No |
| `MAX_CONCURRENT_CLONES` | Number of concurrent workers | `5` | No |
| `PROCESSED_QUEUE_NAME` | Output queue name | `processed_queue` | No |
| `SHARED_VOLUME_DIR` | Directory for cloned repositories | `/shared/heimdall-repos` | No |

### Scanner Service Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `GITHUB_TOKEN` | GitHub token for private repos | - | No |
| `REDIS_HOST` | Redis server hostname | `localhost` | No |
| `REDIS_PORT` | Redis server port | `6379` | No |
| `REDIS_PASSWORD` | Redis password | - | No |
| `REDIS_DB` | Redis database number | `0` | No |
| `MAX_CONCURRENT_SCANS` | Number of concurrent workers | `3` | No |
| `PROCESSED_QUEUE_NAME` | Input queue name | `processed_queue` | No |
| `SECRETS_QUEUE_NAME` | Output queue name | `secrets_queue` | No |
| `TRUFFLEHOG_BINARY_PATH` | Path to TruffleHog binary | `trufflehog` | No |
| `SCAN_TIMEOUT_MINUTES` | Max time per repository scan | `30` | No |
| `SHARED_VOLUME_DIR` | Directory for cloned repositories | `/shared/heimdall-repos` | No |
| `TRUFFLEHOG_CONCURRENCY` | TruffleHog concurrency setting | `8` | No |
| `TRUFFLEHOG_ONLY_VERIFIED` | Only report verified secrets | `false` | No |

### Cleaner Service Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `REDIS_HOST` | Redis server hostname | `localhost` | No |
| `REDIS_PORT` | Redis server port | `6379` | No |
| `REDIS_PASSWORD` | Redis password | - | No |
| `REDIS_DB` | Redis database number | `0` | No |
| `CLEANUP_QUEUE_NAME` | Input queue name | `cleanup_queue` | No |
| `MAX_CONCURRENT_JOBS` | Number of concurrent workers | `2` | No |
| `SHARED_VOLUME_DIR` | Directory for cloned repositories | `/shared/heimdall-repos` | No |

### Cron Schedule Examples

- `0 0 * * 0` - Every Sunday at midnight (default)
- `0 2 * * *` - Daily at 2 AM
- `*/30 * * * *` - Every 30 minutes
- `0 9-17 * * 1-5` - Every hour from 9 AM to 5 PM on weekdays

## Local Development

### Prerequisites

- Go 1.24+
- Docker
- Redis server (for queue communication)
- TruffleHog binary (for scanner service)

### Running Locally

1. Start Redis:
```bash
docker run -d -p 6379:6379 redis:alpine
```

2. Set environment variables for collector:
```bash
export GITHUB_ORG="your-org"
export GITHUB_TOKEN="your-github-token"  # Optional: only needed for private repos
export RUN_ON_STARTUP="true"
```

3. Run services:
```bash
# Terminal 1 - Collector
make run-collector
# or
go run ./cmd/collector

# Terminal 2 - Cloner
make run-cloner
# or
go run ./cmd/cloner

# Terminal 3 - Scanner (requires TruffleHog)
make run-scanner
# or
go run ./cmd/scanner

# Terminal 4 - Cleaner
make run-cleaner
# or
go run ./cmd/cleaner

# Terminal 5 - Monitor queues (optional)
make run-monitor
# or
go run ./cmd/monitor
```

### Building

```bash
# Build all binaries
make build-all

# Build specific service
make build-collector
make build-cloner
make build-scanner
make build-cleaner

# Build all Docker images
make docker-build

# Build specific Docker image
make docker-build-collector
make docker-build-cloner
make docker-build-scanner
make docker-build-cleaner
```

### Code Quality

```bash
# Format code
make fmt

# Run linter (requires golangci-lint)
make lint

# Install/update dependencies
make deps

# Clean build artifacts
make clean
```

## Docker Deployment

### Running with Docker

```bash
# Start Redis
docker run -d --name redis -p 6379:6379 redis:alpine

# Run Collector
docker run -d --name collector \
  -e GITHUB_ORG="your-org" \
  -e GITHUB_TOKEN="your-github-token" \  # Optional: only needed for private repos
  -e REDIS_HOST="redis" \
  -e RUN_ON_STARTUP="true" \
  --link redis \
  ghcr.io/klimeurt/heimdall:latest

# Run Cloner
docker run -d --name cloner \
  -e GITHUB_TOKEN="your-github-token" \
  -e REDIS_HOST="redis" \
  -e MAX_CONCURRENT_CLONES="5" \
  --link redis \
  ghcr.io/klimeurt/heimdall-cloner:latest

# Create shared volume
docker volume create heimdall-repos

# Run Scanner
docker run -d --name scanner \
  -e GITHUB_TOKEN="your-github-token" \
  -e REDIS_HOST="redis" \
  -e MAX_CONCURRENT_SCANS="3" \
  -v heimdall-repos:/shared/heimdall-repos \
  --link redis \
  ghcr.io/klimeurt/heimdall-scanner:latest

# Run Cleaner
docker run -d --name cleaner \
  -e REDIS_HOST="redis" \
  -e MAX_CONCURRENT_JOBS="2" \
  -v heimdall-repos:/shared/heimdall-repos \
  --link redis \
  ghcr.io/klimeurt/heimdall-cleaner:latest
```

### Docker Compose

```yaml
version: '3.8'
services:
  redis:
    image: redis:alpine
    ports:
      - "6379:6379"
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5

  init-volume:
    image: busybox
    volumes:
      - shared-repos:/shared/heimdall-repos
    command: sh -c "chown -R 1000:1000 /shared/heimdall-repos && chmod -R 755 /shared/heimdall-repos"

  collector:
    image: ghcr.io/klimeurt/heimdall:latest
    environment:
      - GITHUB_ORG=${GITHUB_ORG}
      - GITHUB_TOKEN=${GITHUB_TOKEN:-}  # Optional: only needed for private repos
      - REDIS_HOST=redis
      - RUN_ON_STARTUP=true
      - CRON_SCHEDULE=${COLLECTION_SCHEDULE:-0 0 * * 0}
    depends_on:
      redis:
        condition: service_healthy

  cloner:
    image: ghcr.io/klimeurt/heimdall-cloner:latest
    environment:
      - GITHUB_TOKEN=${GITHUB_TOKEN}
      - REDIS_HOST=redis
      - MAX_CONCURRENT_CLONES=5
      - SHARED_VOLUME_DIR=/shared/heimdall-repos
    depends_on:
      redis:
        condition: service_healthy
      init-volume:
        condition: service_completed_successfully
    volumes:
      - shared-repos:/shared/heimdall-repos

  scanner:
    image: ghcr.io/klimeurt/heimdall-scanner:latest
    environment:
      - GITHUB_TOKEN=${GITHUB_TOKEN}
      - REDIS_HOST=redis
      - MAX_CONCURRENT_SCANS=3
      - SHARED_VOLUME_DIR=/shared/heimdall-repos
    depends_on:
      redis:
        condition: service_healthy
      init-volume:
        condition: service_completed_successfully
    volumes:
      - shared-repos:/shared/heimdall-repos

  cleaner:
    image: ghcr.io/klimeurt/heimdall-cleaner:latest
    environment:
      - REDIS_HOST=redis
      - MAX_CONCURRENT_JOBS=2
      - SHARED_VOLUME_DIR=/shared/heimdall-repos
    depends_on:
      redis:
        condition: service_healthy
      init-volume:
        condition: service_completed_successfully
    volumes:
      - shared-repos:/shared/heimdall-repos

volumes:
  shared-repos:
    driver: local
    driver_opts:
      type: none
      o: bind
      device: /home/heims/heimdall_repos
```

## Monitoring

All services log operations to stdout. You can view logs using:

```bash
# View specific service logs
docker logs -f collector
docker logs -f cloner
docker logs -f scanner
docker logs -f cleaner

# Monitor Redis queues
make run-monitor
```

### Log Examples

**Collector Service:**
```
2023/12/01 10:00:00 Cron scheduler started with schedule: 0 0 * * 0
2023/12/01 10:00:00 Running initial collection on startup...
2023/12/01 10:00:01 Starting repository collection for organization: example-org
2023/12/01 10:00:02 Found 25 repositories
2023/12/01 10:00:02 Pushed to queue: example-org/repo-1
2023/12/01 10:00:05 Successfully queued 25 repositories
```

**Cloner Service:**
```
2023/12/01 10:00:03 Worker 1 started
2023/12/01 10:00:03 Worker 1: Processing example-org/repo-1
2023/12/01 10:00:05 Worker 1: Successfully cloned and analyzed repo-1 (150 commits, 45 files)
2023/12/01 10:00:05 Worker 1: Pushed to processed_queue
```

**Scanner Service:**
```
2023/12/01 10:00:06 Worker 1 started
2023/12/01 10:00:06 Worker 1: Scanning example-org/repo-1
2023/12/01 10:00:45 Worker 1: Found 2 valid secrets in repo-1
2023/12/01 10:00:45 Worker 1: Pushed to secrets_queue
2023/12/01 10:00:45 Worker 1: Pushed cleanup job to cleanup_queue
```

**Cleaner Service:**
```
2023/12/01 10:00:46 Worker 1 started
2023/12/01 10:00:46 Worker 1: Processing cleanup job for example-org/repo-1
2023/12/01 10:00:46 Worker 1: Successfully removed directory /shared/heimdall-repos/example-org_repo-1_abc123
```

## Security Considerations

1. **GitHub Token**: Store securely, never in code or version control
2. **Non-root User**: All containers run as UID 1000
3. **Shared Volume Security**: Ensure proper permissions on shared volume
4. **Network Policies**: Consider implementing to restrict traffic
5. **Redis Security**: Use password authentication in production
6. **Secret Scanning**: Scanner validates found secrets to reduce false positives
7. **Automatic Cleanup**: Cleaner service removes cloned repositories after processing

## Testing

### Unit Tests

```bash
# Run all unit tests
make test

# Run with coverage
make test-coverage

# Test specific service
go test -v ./internal/collector/...
go test -v ./internal/cloner/...
go test -v ./internal/scanner/...
```

### Integration Tests

```bash
# Run integration tests
make test-integration

# Run all tests (unit + integration)
make test-all

# Run with coverage including integration
make test-coverage-all
```

### CI Pipeline

```bash
# Run full CI pipeline (unit tests only)
make ci

# Run CI with integration tests
make ci-integration
```

## Directory Structure

```
.
├── cmd/
│   ├── collector/          # Collector service entry point
│   ├── cloner/             # Cloner service entry point
│   ├── scanner/            # Scanner service entry point
│   ├── cleaner/            # Cleaner service entry point
│   └── monitor/            # Redis queue monitor utility
├── internal/
│   ├── collector/          # Collector service logic
│   ├── cloner/             # Cloner service logic
│   ├── scanner/            # Scanner service logic
│   ├── cleaner/            # Cleaner service logic
│   └── config/             # Shared configuration management
├── deployments/
│   ├── collector/          # Collector Docker build files
│   ├── cloner/             # Cloner Docker build files
│   ├── scanner/            # Scanner Docker build files
│   └── cleaner/            # Cleaner Docker build files
├── docker-compose.yml      # Docker Compose configuration
├── docker-compose.test.yml # Test environment setup
├── go.mod                  # Go module definition
├── go.sum                  # Go module checksums
├── Makefile                # Build and development commands
├── CLAUDE.md               # AI assistant instructions
└── README.md               # This file
```

## Troubleshooting

### Common Issues

1. **Authentication Failed**
   - If accessing private repositories, verify GitHub token has `repo` scope
   - Check token hasn't expired
   - Ensure token has access to private repositories if needed
   - Note: Public repositories can be accessed without a token

2. **Redis Connection Failed**
   - Verify Redis is running and accessible
   - Check Redis host, port, and password settings
   - Test connection: `redis-cli ping`

3. **No Repositories Found**
   - Verify organization name is correct
   - For private organizations, ensure token has access to the organization
   - Try with smaller `GITHUB_PAGE_SIZE` if hitting API limits
   - Note: Without a token, API rate limits are more restrictive (60 requests/hour)

4. **Cron Not Triggering**
   - Verify cron schedule syntax
   - Check collector logs for scheduler startup
   - Set `RUN_ON_STARTUP=true` for immediate testing

5. **Cloner Disk Space Issues**
   - Monitor shared volume disk usage
   - Ensure cleaner service is running to remove old clones
   - Check `SHARED_VOLUME_DIR` permissions

6. **Scanner Timeout**
   - Increase `SCAN_TIMEOUT_MINUTES` for large repositories
   - Check if TruffleHog binary is accessible
   - Verify disk space in shared volume
   - Adjust `TRUFFLEHOG_CONCURRENCY` for performance

7. **Queue Processing Stuck**
   - Use monitor tool to check queue status: `make run-monitor`
   - Check worker logs for errors
   - Verify Redis persistence settings

### Debug Mode

Enable verbose logging:
```bash
# For development
export LOG_LEVEL=debug

# Monitor all queues
make run-monitor
```

## Dependencies

- **Redis**: Required for queue communication between services
- **GitHub API Access**: Required for collector service
- **Git**: Required for cloner and scanner services to clone repositories  
- **TruffleHog**: Required binary for scanner service to perform secret scanning
- **Go 1.24+**: For building from source

## Contributing

1. Fork the repository
2. Create feature branch (`git checkout -b feature/amazing-feature`)
3. Commit changes (`git commit -m 'Add amazing feature'`)
4. Push to branch (`git push origin feature/amazing-feature`)
5. Open Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details.