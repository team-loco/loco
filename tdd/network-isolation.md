# TDD: Namespace Network Isolation

## Problem

User app namespaces (`wks-*-res-*`) currently have no network policies applied.
Any pod can reach any other pod cluster-wide via `.cluster.local` DNS, which means
a compromised or misbehaving app can freely communicate with other users' apps,
platform internals, or the Kubernetes API.

## Goal

- Every app namespace is isolated by default — no inter-namespace traffic unless explicitly allowed
- Platform components can reach into app namespaces only for the flows they actually need
- Apps within the same workspace can opt in to talk to each other
- Cross-workspace communication is never allowed

---

## Cluster namespace inventory

| Namespace | Role |
|---|---|
| `wks-*-res-*` | user app namespaces |
| `envoy-gateway-system` | ingress (Envoy Gateway forwards HTTP to app pods) |
| `observability` | otel-col-deploy (OTLP receiver), otel-col-daemon (hostNetwork — no netpol needed), grafana, obs-proxy |
| `loco-system` | agent, controller, UI |
| `kube-system` | kube-dns |
| `cert-manager` | cert-manager |

---

## Allowed traffic matrix

### Ingress into app namespace

| Source namespace | Port | Reason |
|---|---|---|
| `envoy-gateway-system` | app container port | HTTP traffic forwarding |
| *(nothing else)* | — | — |

### Egress from app namespace

| Destination | Port | Reason |
|---|---|---|
| `kube-system` (kube-dns pods) | 53 UDP + TCP | DNS resolution |
| `observability` (otel-col-deploy) | 4317 (gRPC), 4318 (HTTP) | push traces + metrics |
| internet (0.0.0.0/0, excluding cluster CIDR) | any | app external API calls |
| peer app namespace (same workspace, opt-in only) | peer's container port | inter-app communication |

Everything else is **denied**.

Note: `loco-system` does not need ingress into app namespaces. The controller
manages the namespace, not the running pods. The agent is called by apps
externally, not the other way round.

---

## Implementation

### Where policies are created

The controller (`application_controller.go`) already owns the namespace lifecycle.
A new `ensureNetworkPolicies` function is added alongside `ensureNamespace` and
called in the same reconcile loop. Policies are namespace-scoped, so they are
automatically garbage-collected when the namespace is deleted.

### Policies created per app namespace

**1. default-deny-all**
```yaml
kind: NetworkPolicy
spec:
  podSelector: {}        # applies to all pods in namespace
  policyTypes: [Ingress, Egress]
  # no ingress/egress rules = deny all
```

**2. allow-dns-egress**
```yaml
kind: NetworkPolicy
spec:
  podSelector: {}
  policyTypes: [Egress]
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
        - podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - port: 53
          protocol: UDP
        - port: 53
          protocol: TCP
```

**3. allow-envoy-ingress**
```yaml
kind: NetworkPolicy
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: envoy-gateway-system
```

**4. allow-otel-egress**
```yaml
kind: NetworkPolicy
spec:
  podSelector: {}
  policyTypes: [Egress]
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: observability
      ports:
        - port: 4317
          protocol: TCP
        - port: 4318
          protocol: TCP
```

**5. allow-internet-egress**
```yaml
kind: NetworkPolicy
spec:
  podSelector: {}
  policyTypes: [Egress]
  egress:
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except:
              - <cluster pod CIDR>     # populated from controller config
              - <cluster service CIDR> # populated from controller config
```

### Opt-in inter-app communication

A user declares allowed peers in the Application CRD:

```go
// added to ApplicationSpec
AllowedPeers []string  // list of resource IDs within the same workspace
```

When the controller reconciles an app with `AllowedPeers`, for each peer resource ID
it creates a targeted policy **in the peer's namespace** allowing ingress from this app:

**allow-peer-{resourceId}** (created in peer's namespace)
```yaml
kind: NetworkPolicy
metadata:
  name: allow-peer-<source-resource-id>
  namespace: wks-<workspaceId>-res-<peerResourceId>
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              loco.io/workspace-id: <workspaceId>   # enforces same-workspace only
              loco.io/resource-id: <sourceResourceId>
```

Cross-workspace is structurally prevented: the `loco.io/workspace-id` label match
means a policy allowing namespace A can never match namespace B in a different
workspace, even if someone manually sets `AllowedPeers` to a foreign resource ID.

The controller also needs to watch peer apps and clean up these policies when:
- the source app removes a peer from `AllowedPeers`
- the source app is deleted

### Namespace labels required

The controller already applies `loco.io/workspace-id` to app namespaces (line 335
in `application_controller.go`). We need to add `loco.io/resource-id` so that
peer selectors can target individual namespaces precisely.

Platform namespaces (`envoy-gateway-system`, `observability`, `kube-system`) need
`kubernetes.io/metadata.name` labels — these are automatically added by Kubernetes
1.21+ so no manual labeling needed.

---

## CRD changes

Add to `ApplicationSpec`:

```go
// AllowedPeers is a list of resource IDs (within the same workspace) that
// are permitted to send traffic to this application.
AllowedPeers []string `json:"allowedPeers,omitempty"`
```

This is intentionally receive-side: an app declares who is allowed to reach it,
not who it can reach. This is consistent with how NetworkPolicy ingress rules work
and keeps authorization in the hands of the receiving service.

---

## Controller config changes

The internet-egress policy needs the cluster's pod and service CIDRs to exclude
from the `0.0.0.0/0` block. These should be passed to the controller as environment
variables or flags (similar to how `locoNamespace` is configured today).

---

## What is explicitly out of scope

- L7 / HTTP-level policies (Cilium supports this but unnecessary for now)
- Cross-workspace communication (structurally blocked, no config option)
- Egress to `loco-system` from app pods (apps call the API via the external domain,
  not the internal service address)
- `cert-manager` ingress (cert-manager uses HTTP-01/DNS-01 challenges externally,
  it does not reach into app namespaces)
