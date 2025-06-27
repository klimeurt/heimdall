# Heimdall

A security analysis pipeline for GitHub repositories that scans for secrets and sensitive information.

## Architecture

Five microservices communicating through Redis queues:
- **Collector**: Fetches repositories from GitHub organizations
- **Cloner**: Clones repositories and performs initial analysis
- **Scanner**: Deep secret scanning using TruffleHog
- **Cleaner**: Removes cloned repositories after scanning
- **Indexer**: Indexes scan results to Elasticsearch for search and analysis

```mermaid
graph TB
    subgraph "GitHub"
        GH[GitHub API]
    end
    
    subgraph "Heimdall Services"
        COL[Collector<br/>Service]
        CLO[Cloner<br/>Service]
        SCA[Scanner<br/>Service]
        CLE[Cleaner<br/>Service]
        IDX[Indexer<br/>Service]
    end
    
    subgraph "Storage"
        R[(Redis<br/>Queues)]
        SV[Shared Volume<br/>/shared/heimdall-repos]
        ES[(Elasticsearch)]
    end
    
    subgraph "Redis Queues"
        Q1{{clone_queue}}
        Q2{{processed_queue}}
        Q3{{secrets_queue}}
        Q4{{cleanup_queue}}
    end
    
    GH -->|Fetch repos| COL
    COL -->|Repository info| Q1
    Q1 -->|Pull jobs| CLO
    CLO -->|Clone repos| SV
    CLO -->|Job metadata| Q2
    Q2 -->|Pull jobs| SCA
    SCA -->|Read repos| SV
    SCA -->|Scan results| Q3
    SCA -->|Cleanup jobs| Q4
    Q3 -->|Pull results| IDX
    Q4 -->|Pull jobs| CLE
    CLE -->|Remove repos| SV
    IDX -->|Index findings| ES
    
    style COL fill:#4A90E2
    style CLO fill:#4A90E2
    style SCA fill:#4A90E2
    style CLE fill:#4A90E2
    style IDX fill:#4A90E2
    style R fill:#DC382D
    style SV fill:#F5A623
    style ES fill:#005571
    style GH fill:#333
```

### Data Flow

1. **Collector** periodically fetches repository lists from GitHub organizations
2. **Cloner** pulls from `clone_queue`, clones repositories to shared volume
3. **Scanner** pulls from `processed_queue`, scans with TruffleHog, validates secrets
4. **Indexer** pulls from `secrets_queue`, indexes findings to Elasticsearch
5. **Cleaner** pulls from `cleanup_queue`, removes cloned repositories

## Quick Start

### Prerequisites
- Docker and Docker Compose
- GitHub token (optional for public repos)
- Elasticsearch (for indexer service)

### Launch

```bash
# Set required environment variables
export GITHUB_ORG=your-org-name
export GITHUB_TOKEN=your-github-token  # Optional for public repos

# Start all services
docker-compose up -d

# View logs
docker-compose logs -f

# Stop services
docker-compose down
```

### Local Development

```bash
# Start Redis and Elasticsearch
docker run -d -p 6379:6379 redis:alpine
docker run -d -p 9200:9200 -e "discovery.type=single-node" elasticsearch:8.11.0

# Build and run services
make build-all
make run-collector  # In separate terminals
make run-cloner
make run-scanner
make run-cleaner
make run-indexer
```

### Configuration

Services are configured via environment variables. Key settings:
- `GITHUB_ORG`: Organization to scan
- `GITHUB_TOKEN`: Access token for private repos
- `REDIS_URL`: Redis connection (default: localhost:6379)
- `ELASTICSEARCH_URL`: Elasticsearch connection (default: http://localhost:9200)
- `MAX_CONCURRENT_*`: Worker pool sizes
- `SHARED_VOLUME_PATH`: Repository storage location

See `docker-compose.yml` for all available options.