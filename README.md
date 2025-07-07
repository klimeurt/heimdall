# Heimdall

A security analysis pipeline for GitHub repositories that scans for secrets and dependencies vulnerabilities.

## Architecture

```mermaid
---
config:
  theme: 'neutral'
---
graph TB
    subgraph "GitHub"
        GH[GitHub API]
    end

    subgraph "Heimdall Services"
        COL[Collector<br/>Service]
        CLO[Cloner<br/>Service]
        TFH[Scanner-TruffleHog<br/>Service]
        OSV[Scanner-OSV<br/>Service]
        COO[Coordinator<br/>Service]
        CLE[Cleaner<br/>Service]
        IDX[Indexer<br/>Service]
    end

    subgraph "Storage"
        R[(Redis<br/>Queues)]
        SV[Shared Volume<br/>/shared/heimdall-repos]
        ES[(Elasticsearch)]
    end

    subgraph "Visualization"
        KB[Kibana]
    end

    subgraph "Redis Queues"
        Q1{{clone_queue}}
        Q2{{trufflehog_queue}}
        Q3{{osv_queue}}
        Q4{{trufflehog_results_queue}}
        Q5{{osv_results_queue}}
        Q6{{coordinator_queue}}
        Q7{{cleanup_queue}}
    end

    GH -->|Fetch repos| COL
    COL -->|Repository info| Q1
    Q1 -->|Pull jobs| CLO
    CLO -->|Clone repos| SV
    CLO -->|Job metadata| Q2
    CLO -->|Job metadata| Q3
    Q2 -->|Pull jobs| TFH
    Q3 -->|Pull jobs| OSV
    TFH -->|Read repos| SV
    OSV -->|Read repos| SV
    TFH -->|Scan results| Q4
    OSV -->|Scan results| Q5
    TFH -->|Completion| Q6
    OSV -->|Completion| Q6
    Q6 -->|Coordinate| COO
    COO -->|When both complete| Q7
    Q4 -->|Pull results| IDX
    Q5 -->|Pull results| IDX
    Q7 -->|Pull jobs| CLE
    CLE -->|Remove repos| SV
    IDX -->|Index findings| ES
    ES -->|Visualize data| KB
    
    style COL fill:#4A90E2
    style CLO fill:#4A90E2
    style TFH fill:#4A90E2
    style OSV fill:#4A90E2
    style COO fill:#4A90E2
    style CLE fill:#4A90E2
    style IDX fill:#4A90E2
    style R fill:#DC382D
    style SV fill:#F5A623
    style ES fill:#005571
    style GH fill:#888
    style KB fill:#F04E98
```

## Quick Start

### Prerequisites

- Docker and Docker Compose
- GitHub token (optional for public repos)
- Redis and Elasticsearch (external deps or from Docker)

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
make run-scanner-trufflehog
make run-scanner-osv
make run-coordinator
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
