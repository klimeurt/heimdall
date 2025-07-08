# Logging in Heimdall

Heimdall uses structured logging with Go's `slog` package to provide consistent, queryable logs across all services.

## Configuration

All services support the following environment variables for logging configuration:

| Variable | Description | Default | Options |
|----------|-------------|---------|---------|
| `LOG_LEVEL` | Logging verbosity level | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | Output format for logs | `json` | `json`, `text` |
| `LOG_OUTPUT` | Where to write logs | `stdout` | `stdout`, `stderr`, or file path |
| `LOG_ADD_SOURCE` | Include source file/line in logs | `false` | `true`, `false` |

## Examples

### Development Configuration (Human-Readable)
```bash
export LOG_LEVEL=debug
export LOG_FORMAT=text
export LOG_OUTPUT=stdout
export LOG_ADD_SOURCE=true
```

### Production Configuration (Machine-Readable)
```bash
export LOG_LEVEL=info
export LOG_FORMAT=json
export LOG_OUTPUT=stdout
export LOG_ADD_SOURCE=false
```

### Docker Compose Configuration
```yaml
environment:
  - LOG_LEVEL=${LOG_LEVEL:-info}
  - LOG_FORMAT=${LOG_FORMAT:-json}
  - LOG_OUTPUT=${LOG_OUTPUT:-stdout}
  - LOG_ADD_SOURCE=${LOG_ADD_SOURCE:-false}
```

## Log Structure

### Common Fields
All log entries include these base fields:
- `time`: Timestamp of the log entry
- `level`: Log level (DEBUG, INFO, WARN, ERROR)
- `msg`: Log message
- `service`: Service name (sync, scanner-trufflehog, scanner-osv, indexer)
- `version`: Service version (if available)

### Context Fields
When processing within a worker context:
- `context.worker_id`: Worker ID for concurrent operations
- `context.correlation_id`: Unique ID for tracking operations across services

### Service-Specific Fields

#### Sync Service
- `org`: GitHub organization
- `repo`: Repository name
- `operation`: Operation type (clone, update, remove)
- `duration_ms`: Operation duration in milliseconds

#### Scanner Services (TruffleHog & OSV)
- `org`: GitHub organization
- `repo`: Repository name
- `path`: Repository path on disk
- `findings_count`: Number of findings (secrets or vulnerabilities)
- `duration_ms`: Scan duration in milliseconds

#### Indexer Service
- `org`: GitHub organization
- `repo`: Repository name
- `document_count`: Number of documents indexed
- `index`: Elasticsearch index name
- `duration_ms`: Indexing duration in milliseconds

## Log Levels

### Debug
Detailed information for debugging:
- Queue operations
- Worker state changes
- Detailed scan parameters

Example:
```json
{
  "time": "2024-01-15T10:30:45Z",
  "level": "DEBUG",
  "msg": "running TruffleHog scan",
  "service": "scanner-trufflehog",
  "path": "/shared/heimdall-repos/myorg/myrepo",
  "concurrency": 8,
  "only_verified": true
}
```

### Info
Normal operational events:
- Service startup/shutdown
- Repository processing
- Successful operations

Example:
```json
{
  "time": "2024-01-15T10:30:45Z",
  "level": "INFO",
  "msg": "repository scanned successfully",
  "service": "scanner-trufflehog",
  "org": "myorg",
  "repo": "myrepo",
  "valid_secrets_found": 3,
  "duration_ms": 15234
}
```

### Warn
Warning conditions that don't stop processing:
- Retryable errors
- Performance issues
- Non-critical failures

Example:
```json
{
  "time": "2024-01-15T10:30:45Z",
  "level": "WARN",
  "msg": "failed to push repository to queues",
  "service": "sync",
  "org": "myorg",
  "repo": "myrepo",
  "error": "redis timeout"
}
```

### Error
Error conditions that affect service operation:
- Failed operations
- Connection errors
- Invalid data

Example:
```json
{
  "time": "2024-01-15T10:30:45Z",
  "level": "ERROR",
  "msg": "failed to scan repository",
  "service": "scanner-osv",
  "org": "myorg",
  "repo": "myrepo",
  "error": "clone path does not exist: /shared/heimdall-repos/myorg/myrepo"
}
```

## Querying Logs

### With jq (JSON format)
```bash
# View all errors
docker-compose logs scanner-trufflehog | jq 'select(.level == "ERROR")'

# Find slow operations (>10 seconds)
docker-compose logs sync | jq 'select(.duration_ms > 10000)'

# Count findings by repository
docker-compose logs indexer | jq 'select(.msg == "repository indexed successfully") | {repo: .repo, secrets: .secrets_found}'
```

### With grep (Text format)
```bash
# View all errors
docker-compose logs scanner-osv | grep "level=ERROR"

# Find specific repository logs
docker-compose logs sync | grep "repo=myrepo"
```

### In Elasticsearch/Kibana
When using the indexer service, logs can be queried in Elasticsearch:

```json
{
  "query": {
    "bool": {
      "must": [
        { "term": { "service": "scanner-trufflehog" } },
        { "term": { "level": "ERROR" } },
        { "range": { "@timestamp": { "gte": "now-1h" } } }
      ]
    }
  }
}
```

## Best Practices

1. **Use appropriate log levels**: Don't log everything at INFO level
2. **Include context**: Always include org/repo when processing repositories
3. **Log durations**: Include timing information for operations
4. **Structured data**: Use field names consistently across services
5. **Avoid sensitive data**: Never log tokens, secrets, or passwords

## Troubleshooting

### Enabling Debug Logging
```bash
# For all services
export LOG_LEVEL=debug
docker-compose up

# For specific service
docker-compose run -e LOG_LEVEL=debug scanner-trufflehog
```

### Changing Log Format
```bash
# Human-readable format for debugging
export LOG_FORMAT=text
docker-compose logs -f sync

# Machine-readable for processing
export LOG_FORMAT=json
docker-compose logs indexer | jq .
```

### Writing Logs to File
```bash
# Direct service logs to file
export LOG_OUTPUT=/var/log/heimdall/sync.log

# Or use Docker compose logging
docker-compose logs -f > heimdall.log
```