# OSV Scanner Implementation

## Overview

This document describes the implementation of the OSV (Open Source Vulnerabilities) scanner service that runs in parallel with the existing TruffleHog scanner in the Heimdall security analysis pipeline.

## Architecture Changes

### New Message Flow
```
Collector → clone_queue → Cloner → ┬→ processed_queue → TruffleHog Scanner → coordinator_queue
                                    └→ osv_queue → OSV Scanner → coordinator_queue
                                    
coordinator_queue → Coordinator → cleanup_queue → Cleaner
```

### New Services

1. **OSV Scanner Service** (`internal/osv-scanner/`)
   - Consumes from `osv_queue`
   - Runs `osv-scanner` binary on cloned repositories
   - Sends results to `osv_results_queue` for future indexing
   - Sends completion notification to `coordinator_queue`

2. **Coordinator Service** (`internal/coordinator/`)
   - Maintains in-memory state of scanning jobs
   - Waits for both TruffleHog and OSV scanner completions
   - Only sends to cleanup_queue when both scanners are done
   - Handles timeouts and partial completions (default 60 minute timeout)

## Key Implementation Details

### Modified Components

1. **Cloner Service**
   - Now sends messages to both `processed_queue` (for TruffleHog) and `osv_queue` (for OSV scanner)
   - No other changes required

2. **Scanner Service (TruffleHog)**
   - Changed from sending directly to `cleanup_queue` to sending to `coordinator_queue`
   - Sends `ScanCoordinationMessage` with scanner type "trufflehog"

### New Data Structures

- `OSVScanResult`: Represents a vulnerability found by OSV Scanner
- `OSVScannedRepository`: Repository scan results from OSV scanner
- `ScanCoordinationMessage`: Message sent to coordinator when a scanner completes
- `CoordinationState`: Tracks the state of a repository being scanned by multiple scanners

### Configuration

New environment variables for OSV Scanner:
- `OSV_QUEUE_NAME`: Queue for OSV scanner jobs (default: "osv_queue")
- `OSV_RESULTS_QUEUE_NAME`: Queue for OSV scan results (default: "osv_results_queue")
- `COORDINATOR_QUEUE_NAME`: Queue for coordination messages (default: "coordinator_queue")
- `MAX_CONCURRENT_SCANS`: Number of concurrent OSV scans (default: 3)
- `SCAN_TIMEOUT_MINUTES`: Timeout for OSV scans (default: 30)

New environment variables for Coordinator:
- `COORDINATOR_QUEUE_NAME`: Queue for coordination messages (default: "coordinator_queue")
- `CLEANUP_QUEUE_NAME`: Queue for cleanup jobs (default: "cleanup_queue")
- `JOB_TIMEOUT_MINUTES`: Timeout for coordination jobs (default: 60)
- `STATE_CLEANUP_INTERVAL`: Interval for cleaning up expired jobs (default: 5m)

## Building and Running

### Build Commands
```bash
# Build OSV scanner
make build-osv-scanner

# Build coordinator
make build-coordinator

# Build all services (includes new ones)
make build-all
```

### Docker Commands
```bash
# Build Docker images
make docker-build-osv-scanner
make docker-build-coordinator

# Push to registry
make docker-push-osv-scanner
make docker-push-coordinator
```

### Running Locally
```bash
# Run OSV scanner
make run-osv-scanner

# Run coordinator
make run-coordinator
```

## Testing

Unit tests have been added for both new services:
- `internal/osv-scanner/scanner_test.go`
- `internal/coordinator/coordinator_test.go`

Run tests with:
```bash
go test ./internal/osv-scanner/...
go test ./internal/coordinator/...
```

## Docker Compose

The `docker-compose.yml` has been updated to include the new services:
- `osv-scanner`: Runs the OSV vulnerability scanner
- `coordinator`: Manages coordination between scanners

Both services are configured with appropriate environment variables and dependencies.

## Future Enhancements

1. **OSV Results Indexing**: The OSV scanner sends results to `osv_results_queue` which can be consumed by a future indexer service to store vulnerability data in Elasticsearch.

2. **Additional Scanners**: The coordinator pattern makes it easy to add more scanners in the future - just have the cloner send to additional queues and update the coordinator to track the new scanner type.

3. **Configurable Coordination**: The coordinator could be enhanced to support configurable rules about which scanners must complete before cleanup.