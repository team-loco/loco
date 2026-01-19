# Loco Dependencies

Goal is to track everything Loco is directly/indirectly dependent on, so we can map any inter-dependencies and version things properly.

## Overview

Loco is a container orchestration platform with the following components:

- **CLI**: Terminal-based interface for deployments
- **API**: ConnectRPC backend serving the CLI and UI
- **Controller**: Kubernetes operator managing the kubernetes resources
- **UI**: React-based web dashboard

---

## CLI

### External Services & Platforms

- **Container Registries**: Gitlab Container Registry
  - we have secrets for this we need to manage in the API as well.
- **Docker API**: Local Docker daemon for building and pushing images
- **Go Modules**: Standard library, CLI framework libraries (Cobra, Charm Bubble Tea, Lipgloss), protobuf

---

## API Server

### Core Dependencies

- **PostgreSQL Database**: User accounts, deployments, credentials
- **Protobuf**: API contract definitions (shared with CLI/UI)
- **ConnectRPC**: HTTP-based RPC for CLI/UI communication

### Kubernetes Integration

- **Kubernetes Client Libraries** (`k8s.io/client-go`, `k8s.io/api`, `k8s.io/apimachinery`):
  - Read/write Application CRDs from the controller
  - Query deployments and pods
  - Manage secrets and configmaps

- **controller-runtime** (`sigs.k8s.io/controller-runtime`):
  - Client abstraction for Kubernetes API
  - RBAC management

### Cloud Providers

- **GitHub OAuth**: User authentication
- **GitLab API**: Repository access and CI/CD
- **Cloudflare**: DNS and DDoS protection (token required)

### Caching

- **BigCache**: In-memory caching layer

### Observability

- **OpenTelemetry**: Instrumentation for metrics, logs, traces
- **OpenTelemetry Collector**: Data collection and export

---

## Controller

### Kubernetes Operators & APIs

- **controller-runtime** (`sigs.k8s.io/controller-runtime`):
  - Reconciliation loops
  - Webhook management
  - RBAC controller

- **Kubernetes Gateway API** (`sigs.k8s.io/gateway-api`):
  - Manages HTTPRoute, Gateway resources
  - Abstracts ingress/routing configuration

- **Kubernetes API Groups**:
  - Core API (v1): Pods, Services, Secrets, ConfigMaps, PersistentVolumes
  - Apps API: Deployments, StatefulSets, DaemonSets
  - Batch API: Jobs, CronJobs
  - Networking API: NetworkPolicies
  - CRD Extensions: CustomResourceDefinitions management

### Custom Resources

- **Application CRD**: Loco's custom resource for deploying user applications
  - Managed by the controller
  - Reconciles to Kubernetes Deployments, Services, Ingress

### Observability in Controller

- **OpenTelemetry SDK**: Instrument controller operations
- **Prometheus**: Metrics exposure

### Testing

- **Ginkgo/Gomega**: BDD testing framework for controller logic

---

## Kubernetes Infrastructure (via Helm Charts)

### Phase 1: Networking

#### loco-networking Chart

Deploys:

- **Cilium** (v1.18.4)
  - **CNI (Container Network Interface)** implementation
  - Pod-to-pod networking with eBPF
  - Network policies enforcement
  - **Hubble**: Observability plugin for network flows
    - Exposes flow metrics
    - UI dashboard for network visibility

**Cilium Dependencies**:

- Linux kernel eBPF support
- Kubernetes network plugin interface

---

### Phase 2: Core Services

#### loco-core Chart

Deploys:

- **API Server Deployment**: Loco API running in pods
- **UI Server Deployment**: React frontend
- **Envoy Gateway** (v1.6.1 via gateway-helm dependency)
  - **HTTP routing** and load balancing
  - **TLS termination** (HTTPS)
  - **HTTP/3 support**
  - **Gateway API implementation**: Translates K8s Gateway API resources to Envoy config

**Envoy Gateway Dependencies**:

- Kubernetes Gateway API (v1.2.1+)
- Service discovery via Kubernetes services

#### cert-manager Helm Chart (v1.19.1)

- **Let's Encrypt Integration**: Automatic SSL certificate provisioning
- **Certificate Automation**: Renewal and rotation
- **ACME Protocol**: Orchestrates ACME challenges

**cert-manager Dependencies**:

- Kubernetes API for CustomResourceDefinitions (Certificate, Issuer, ClusterIssuer)
- External ACME server (Let's Encrypt production/staging)

#### loco-controller Chart

Deploys:

- **Loco Controller** (our operator)
  - Watches Application CRDs
  - Reconciles to Deployments, Services, Ingress

---

### Phase 3: Observability

#### loco-obs Chart

Deploys:

**OpenTelemetry Stack**:

- **OpenTelemetry Operator** (v0.99.2)
  - Manages instrumentation of applications
  - Configures SDK injection
  - Manages Collector instances

- **OpenTelemetry Collector (Daemon)** (v0.140.1 via otel-col-daemon alias)
  - Node-level collection of telemetry data
  - Runs as DaemonSet
  - Receives spans, metrics, logs from apps and services

- **OpenTelemetry Collector (Deployment)** (v0.140.1 via otel-col-deploy alias)
  - Centralized collection and aggregation
  - Processes and exports data to ClickHouse

**Data Storage & Visualization**:

- **ClickHouse** (v0.3.4 via Altinity helm repo)
  - Time-series data store for metrics, logs, traces
  - OLAP database optimized for analytics

- **Grafana** (v10.3.0)
  - Dashboard creation and visualization
  - Data source connections (ClickHouse, Prometheus)
  - Alerting

**Observability Data Flow**:

```
Applications → OpenTelemetry SDK
  ↓
Collector (Daemon/Deployment)
  ↓
ClickHouse
  ↓
Grafana (Dashboards & Alerts)
```

---

## UI (Web Frontend)

### Framework & Build

- **React**: Component-based UI framework
- **Static HTML/CSS/JS**: Build output served by nginx

### API Communication

- **ConnectRPC Client**: Type-safe client for API calls
- **Protocol Buffers**: Shared message definitions

### No external service dependencies beyond the API server.

---

## External Helm Chart Repositories

Used in `helmfile.yaml.gotmpl`:

| Repository         | URL                                                        | Purpose              |
| ------------------ | ---------------------------------------------------------- | -------------------- |
| **jetstack**       | https://charts.jetstack.io                                 | cert-manager chart   |
| **cilium**         | https://helm.cilium.io                                     | Cilium CNI chart     |
| **altinity**       | https://helm.altinity.com                                  | ClickHouse chart     |
| **grafana**        | https://grafana.github.io/helm-charts                      | Grafana chart        |
| **open-telemetry** | https://open-telemetry.github.io/opentelemetry-helm-charts | OpenTelemetry charts |

---

## Deployment Flow & Dependencies

```
User runs: loco deploy
  ↓
CLI → API Server (ConnectRPC)
  ↓
API → Controller (Kubernetes API)
  ↓
Controller → Create/Update Application CRD
  ↓
Controller Reconciles:
  - Deployment (API workload)
  - Service (internal networking)
  - Ingress (via Envoy Gateway)
  ↓
Envoy Gateway (loco-core)
  - Routes traffic via Cilium networking
  - Terminates TLS (via cert-manager certificates)
  - Exposes HTTP/3
```

---

## External Services (Runtime)

- **Let's Encrypt**: ACME certificate authority
- **GitHub/GitLab**: OAuth, repository access, container registries
- **Cloudflare**: DNS/DDoS protection (optional)
- **PostgreSQL Database**: Managed externally or in-cluster

---

## Summary of Critical Dependencies

### Must-Have

- Kubernetes cluster (1.34+)
- PostgreSQL database
- Cilium CNI
- cert-manager + Let's Encrypt
- Envoy Gateway
- OpenTelemetry (if observability needed)

### Integration Points

- **Kubernetes API**: All components talk to the API server
- **Helm/helmfile**: Deployment and lifecycle management
- **OpenTelemetry SDK**: Instrumentation in API and Controller

---

## Notes

- All Helm chart versions are pinned in `helmfile.yaml.gotmpl` and individual `Chart.yaml` files
- Go module dependencies are managed per component (`cli/go.mod`, `api/go.mod`, `controller/go.mod`, `shared/go.mod`)
- Protobuf definitions are shared across CLI, API, and Controller via the `shared` module
- CI/CD integration (GitHub Actions, GitLab CI) is handled at deployment time, not runtime dependency
