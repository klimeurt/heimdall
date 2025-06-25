# Shared Volume Architecture Implementation Plan

## Overview
Implement a shared volume architecture where the cloner service writes cloned repositories to persistent storage, and the scanner service reads from that storage instead of re-cloning. A cleaner service will handle deletion via queue messages.

## 1. Shared Volume Directory Structure (Simplified)
```
/shared/heimdall-repos/
└── {org}_{name}_{uuid}/  # Unique directory per clone
```

## 2. Queue Architecture
- `clone_queue` → Cloner → `processed_queue` → Scanner → `secrets_queue`
- **NEW**: Scanner → `cleanup_queue` → Cleaner

## 3. Cloner Service Modifications

### Update config/config.go
- Add `SharedVolumeDir` field to `ClonerConfig`
- Default to `/shared/heimdall-repos` with env var `SHARED_VOLUME_DIR`

### Update internal/cloner/cloner.go
- Replace in-memory clone with disk-based clone using `git.PlainCloneContext`
- Clone to `/shared/heimdall-repos/{org}_{name}_{uuid}`
- Add UUID generation for unique directory names
- Update `ProcessedRepository` to include `ClonePath` field
- Push enhanced data to `processed_queue` including clone path

### Update internal/collector/types.go
- Add `ClonePath` field to `ProcessedRepository` struct
- Create new `CleanupJob` struct:
  ```go
  type CleanupJob struct {
      ClonePath    string
      Org          string
      Name         string
      RequestedAt  time.Time
      WorkerID     int
  }
  ```

## 4. Scanner Service Modifications

### Update config/config.go
- Add `SharedVolumeDir` field to `ScannerConfig`
- Add `CleanupQueueName` field (default: "cleanup_queue")
- Remove `TempCloneDir` as it's no longer needed

### Update internal/scanner/scanner.go
- Remove repository cloning logic entirely
- Read `ClonePath` from `ProcessedRepository`
- Run Kingfisher directly on shared volume path
- After scan (success or failure), push to both:
  - `secrets_queue` with scan results
  - `cleanup_queue` with cleanup job
- Handle missing/invalid paths gracefully

## 5. Cleaner Service (New Component)

### Create cmd/cleaner/main.go
- New service consuming from `cleanup_queue`
- Similar worker pool pattern as other services

### Create internal/cleaner/cleaner.go
```go
type Cleaner struct {
    config      *config.CleanerConfig
    redisClient *redis.Client
}
```
- Consume `CleanupJob` from `cleanup_queue`
- Delete directory at `ClonePath`
- Log cleanup operations with metrics
- Handle deletion failures gracefully

### Create cleaner configuration in config/config.go
```go
type CleanerConfig struct {
    RedisHost          string
    RedisPort          string
    RedisPassword      string
    RedisDB            int
    CleanupQueueName   string
    MaxConcurrentJobs  int
    SharedVolumeDir    string
}
```

## 6. Deployment Changes

### Update docker-compose.test.yml
```yaml
volumes:
  shared-repos:

services:
  cloner:
    volumes:
      - shared-repos:/shared/heimdall-repos
    environment:
      - SHARED_VOLUME_DIR=/shared/heimdall-repos

  scanner:
    volumes:
      - shared-repos:/shared/heimdall-repos
    environment:
      - SHARED_VOLUME_DIR=/shared/heimdall-repos
      - CLEANUP_QUEUE_NAME=cleanup_queue

  cleaner:
    build:
      context: .
      dockerfile: deployments/cleaner/Dockerfile
    volumes:
      - shared-repos:/shared/heimdall-repos
    environment:
      - REDIS_HOST=redis
      - CLEANUP_QUEUE_NAME=cleanup_queue
      - SHARED_VOLUME_DIR=/shared/heimdall-repos
      - MAX_CONCURRENT_JOBS=2
```

### Create deployments/cleaner/Dockerfile
- Similar structure to other services
- Alpine-based with minimal footprint

## 7. Error Handling & Edge Cases

- Scanner validates clone path exists before processing
- If path missing, log error and still send to cleanup queue
- Cleaner validates path is within shared volume before deletion
- Atomic operations where possible
- Proper permissions (UID 1000) for all services

## 8. Configuration Updates

New environment variables:
- `SHARED_VOLUME_DIR`: Base directory for shared repos
- `CLEANUP_QUEUE_NAME`: Queue for cleanup jobs
- `MAX_CONCURRENT_JOBS`: Cleaner worker count

## Benefits
- Queue-based cleanup ensures no orphaned repositories
- Clean separation of concerns
- Scalable - can run multiple cleaner instances
- Reliable - cleanup jobs won't be lost
- Observable - queue length shows cleanup backlog

## Testing Strategy
- Unit tests for new shared volume logic
- Integration tests with all four services
- Test cleanup job queuing and processing
- Verify no repository leaks under load