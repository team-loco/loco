# ClickStack Testing Environment

Testing environment for ClickStack - a full observability platform built on ClickHouse.

## What's Included

- 1 control-plane node
- 2 worker nodes
- metrics-server for resource metrics
- kubernetes-dashboard for cluster inspection
- ClickStack with HyperDX UI and ClickHouse backend
- OpenTelemetry Demo - microservices app that generates logs, metrics, and traces

## Quick Start

```bash
./setup.sh [cluster-name]
```

Default cluster name is `clickstack-test` if not specified.

## Configuration

The `values.yaml` file contains explicit configuration for:
- HyperDX frontend settings
- ClickHouse resources and persistence
- OTEL collector configuration
- Ingress and TLS settings

Edit `values.yaml` before running setup to customize your deployment.

## Usage

### Create cluster with ClickStack
```bash
./setup.sh my-clickstack
```

### Check deployments
```bash
# ClickStack
kubectl get pods -n clickstack --context kind-my-clickstack
kubectl logs -n clickstack deployment/clickstack-hyperdx --context kind-my-clickstack

# OpenTelemetry Demo
kubectl get pods -n otel-demo --context kind-my-clickstack
```

### Access HyperDX UI (ClickStack)
```bash
kubectl port-forward -n clickstack svc/clickstack-hyperdx 8080:8080 --context kind-my-clickstack
```
Then visit: http://localhost:8080

### Access OpenTelemetry Demo UI
```bash
kubectl port-forward -n otel-demo svc/otel-demo-frontendproxy 8081:8080 --context kind-my-clickstack
```
Then visit: http://localhost:8081

The demo app simulates an e-commerce site with ~10 microservices generating realistic telemetry data.

### Configure API Key
After deployment, access the HyperDX dashboard to generate and configure your API key for telemetry collection.

### Update configuration
```bash
helm upgrade clickstack clickstack/clickstack \
  -n clickstack \
  --values values.yaml \
  --set hyperdx.apiKey=YOUR_API_KEY \
  --kube-context kind-my-clickstack
```

### Access Kubernetes Dashboard
```bash
kubectl proxy --context kind-my-clickstack
```
Then visit: http://localhost:8001/api/v1/namespaces/kubernetes-dashboard/services/https:kubernetes-dashboard:/proxy/

### Monitor Resources
```bash
kubectl top nodes --context kind-my-clickstack
kubectl top pods -n clickstack --context kind-my-clickstack
kubectl top pods -n otel-demo --context kind-my-clickstack
```

### Wiring OpenTelemetry Demo to ClickStack

The `otel-demo-values.yaml` configures the demo's OTEL collector to export to ClickStack. To manually verify or adjust the connection:

1. Check the OTEL collector configuration in the demo
2. Ensure it exports to `clickstack-otel-collector.clickstack.svc.cluster.local:4317`
3. Verify telemetry is flowing by checking ClickHouse tables or HyperDX UI

### Delete cluster
```bash
kind delete cluster --name my-clickstack
```

## References

- [ClickStack Helm Configuration](https://clickhouse.com/docs/use-cases/observability/clickstack/deployment/helm-configuration)
- [ClickHouse Documentation](https://clickhouse.com/docs)
- [OpenTelemetry Demo](https://opentelemetry.io/docs/demo/)
- [ClickHouse OpenTelemetry Demo Fork](https://github.com/ClickHouse/opentelemetry-demo)
