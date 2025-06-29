# Heimdall Helm Chart

This Helm chart deploys Heimdall, a microservices-based security analysis pipeline for GitHub repositories.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- PV provisioner support in the underlying infrastructure (for shared storage)
- StorageClass that supports ReadWriteMany access mode (e.g., NFS, CephFS, GlusterFS)

## Installation

### Add Helm repository (if published)

```bash
helm repo add heimdall https://klimeurt.github.io/heimdall
helm repo update
```

### Install from local chart

```bash
# Clone the repository
git clone https://github.com/klimeurt/heimdall.git
cd heimdall

# Install with minimal configuration
helm install heimdall ./charts/heimdall \
  --set github.org=YOUR_GITHUB_ORG

# Install with GitHub token for private repositories
helm install heimdall ./charts/heimdall \
  --set github.org=YOUR_GITHUB_ORG \
  --set github.token=YOUR_GITHUB_TOKEN
```

## Configuration

### Required Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `github.org` | GitHub organization to scan | `""` (REQUIRED) |

### Common Configuration Options

| Parameter | Description | Default |
|-----------|-------------|---------|
| `github.token` | GitHub access token for private repos | `""` |
| `github.apiDelayMs` | Delay between GitHub API calls | `"100"` |
| `collector.schedule` | Cron schedule for collection | `"0 0 * * *"` (daily) |
| `persistence.size` | Size of shared storage | `100Gi` |
| `persistence.storageClass` | StorageClass for PVC | `""` (default) |

### Service Configuration

Each service can be configured with:
- `enabled`: Enable/disable the service
- `replicaCount`: Number of replicas
- `resources`: CPU/memory limits and requests
- `nodeSelector`: Node selection constraints
- `tolerations`: Pod tolerations
- `affinity`: Pod affinity rules

Example:
```yaml
scanner:
  enabled: true
  replicaCount: 3
  resources:
    limits:
      cpu: 2000m
      memory: 4Gi
    requests:
      cpu: 500m
      memory: 1Gi
```

### External Dependencies

#### Using External Redis
```yaml
redis:
  enabled: false
externalRedis:
  url: "redis://my-redis-host:6379"
```

#### Using External Elasticsearch
```yaml
elasticsearch:
  enabled: false
externalElasticsearch:
  url: "http://my-elasticsearch:9200"
```

## Advanced Configuration

### Custom values file

Create a `values-production.yaml`:

```yaml
github:
  org: "my-organization"
  token: "ghp_xxxxxxxxxxxx"
  apiDelayMs: "200"

collector:
  schedule: "0 */6 * * *"  # Every 6 hours
  replicaCount: 2

scanner:
  replicaCount: 5
  trufflehogOnlyVerified: "true"
  resources:
    limits:
      cpu: 4000m
      memory: 8Gi

persistence:
  storageClass: "fast-ssd"
  size: 500Gi

kibana:
  ingress:
    enabled: true
    hostname: heimdall-kibana.example.com
    annotations:
      kubernetes.io/ingress.class: nginx
      cert-manager.io/cluster-issuer: letsencrypt
    tls: true
```

Install with custom values:
```bash
helm install heimdall ./charts/heimdall -f values-production.yaml
```

## Upgrading

```bash
helm upgrade heimdall ./charts/heimdall \
  --set github.org=YOUR_GITHUB_ORG \
  --set github.token=YOUR_GITHUB_TOKEN
```

## Uninstallation

```bash
helm uninstall heimdall

# Note: PVCs are not automatically deleted
kubectl delete pvc -l app.kubernetes.io/name=heimdall
```

## Monitoring

### Check deployment status
```bash
kubectl get pods -l app.kubernetes.io/name=heimdall
```

### View logs
```bash
# All services
kubectl logs -l app.kubernetes.io/name=heimdall --tail=100 -f

# Specific service
kubectl logs -l app.kubernetes.io/component=scanner --tail=100 -f
```

### Monitor queues
```bash
kubectl exec -it heimdall-redis-master-0 -- redis-cli
> LLEN clone_queue
> LLEN processed_queue
> LLEN secrets_queue
```

### Access Kibana
```bash
kubectl port-forward svc/heimdall-kibana 5601:5601
# Visit http://localhost:5601
```

## Troubleshooting

### Storage Issues

If you encounter storage issues:
1. Ensure your StorageClass supports ReadWriteMany
2. Check PVC status: `kubectl get pvc`
3. Verify init-volume job completed: `kubectl get jobs`

### Service Communication

1. Check Redis connectivity:
   ```bash
   kubectl exec -it deployment/heimdall-collector -- nc -zv heimdall-redis-master 6379
   ```

2. Check Elasticsearch connectivity:
   ```bash
   kubectl exec -it deployment/heimdall-indexer -- curl http://heimdall-elasticsearch:9200/_cluster/health
   ```

### Performance Tuning

Adjust worker counts based on your cluster capacity:
```yaml
cloner:
  maxConcurrent: "10"
scanner:
  maxConcurrent: "20"
  trufflehogConcurrency: "8"
```

## Security Considerations

1. **GitHub Token**: Store securely, use Kubernetes secrets
2. **Network Policies**: Enable if required (`networkPolicy.enabled: true`)
3. **Pod Security**: All containers run as non-root user (UID 1000)
4. **Resource Limits**: Set appropriate limits to prevent resource exhaustion

## Support

For issues and feature requests, please open an issue at:
https://github.com/klimeurt/heimdall/issues