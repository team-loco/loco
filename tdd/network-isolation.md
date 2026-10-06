# TDD: Workspace Network Isolation

## Problem

Without network policies, any pod can reach any other pod cluster-wide via `.cluster.local`
DNS, so a compromised or misbehaving app could reach other workspaces' apps, platform
internals, or cloud metadata.

## Model

Each workspace has one namespace per cluster, `ws-<workspaceId>`, holding all of its apps.
Clusters separate environments, so a namespace is one workspace in one environment.

- Apps in the same workspace namespace can reach each other on any port, by default.
- Nothing else can reach an app, except the Envoy Gateway proxies on the app's container
  port when the app has a route.
- Apps can reach DNS, the telemetry collector's OTLP ports, and public addresses. They
  cannot reach other workspaces, platform namespaces, private address ranges, or cloud
  metadata.
- Cross-workspace traffic is never allowed and has no configuration option.

---

## Cluster namespace inventory

| Namespace | Role |
|---|---|
| `ws-*` | workspace namespaces, one per workspace per cluster, holding its apps |
| `loco-system` | agent, controller, Envoy Gateway proxies (`gateway.envoyproxy.io/owning-gateway-name=eg`) |
| `observability` | otel-col-deploy (OTLP receiver), grafana, obs-proxy; configurable with `LOCO_OBSERVABILITY_NAMESPACE` |
| `kube-system` | kube-dns |
| `cert-manager` | cert-manager |

---

## Allowed traffic

### Ingress into a workspace namespace

| Source | Port | Reason |
|---|---|---|
| pods in the same namespace | any | apps of one workspace talking to each other |
| Envoy Gateway proxies in `loco-system` | the app's container port, routed apps only | HTTP traffic forwarding |

### Egress from a workspace namespace

| Destination | Port | Reason |
|---|---|---|
| pods in the same namespace | any | apps of one workspace talking to each other |
| `kube-system` kube-dns pods | 53 UDP + TCP | DNS resolution |
| `otel-col-deploy` in the observability namespace | 4317, 4318 TCP | traces and metrics |
| `0.0.0.0/0` and `::/0`, except `10/8`, `100.64/10`, `169.254/16`, `172.16/12`, `192.168/16`, `fc00::/7`, `fe80::/10` | any | external APIs; the exclusions cover pod, service and node ranges, CGNAT, and cloud metadata |

Everything else is denied.

---

## Implementation

The controller owns the namespace and creates the policies with server-side apply under
its field owner, before any workload.

### Workspace policies

Fixed names, identical for every app in the namespace, so the apps sharing it apply the
same object and never overwrite each other. They go away with the namespace when its last
app is deleted.

| Policy | Effect |
|---|---|
| `default-deny` | all pods, ingress and egress, no rules |
| `allow-workspace` | all pods, ingress from and egress to `podSelector: {}` (same namespace only) |
| `allow-dns-egress` | egress to kube-dns on 53 UDP and TCP |
| `allow-telemetry-egress` | egress to `otel-col-deploy` on 4317 and 4318 |
| `allow-internet-egress` | egress to public addresses, excluding the ranges above |

A `from` or `to` peer with only a `podSelector` matches pods in the policy's own namespace,
which holds a single workspace, so `allow-workspace` cannot open traffic to another
workspace.

### Per-app gateway policy

`resource-<resourceId>-gateway` selects the app's pods (`app: resource-<resourceId>`) and
allows ingress from the Envoy Gateway proxy pods in `loco-system` on the app's container
port. It exists only while the app has a route: the controller deletes it when routing is
removed and when the app is deleted.

### Pod hardening

The namespace enforces, audits and warns the `restricted` Pod Security profile. App pods
run with `runAsNonRoot`, the `RuntimeDefault` seccomp profile, no privilege escalation and
all capabilities dropped, and neither the pod nor its ServiceAccount mounts an API token.
Images must run as a numeric non-root user.

---

## Out of scope

- Per-app isolation inside a workspace. If needed, an app could opt out of `allow-workspace`
  with its own deny policy; nothing asks for it yet.
- L7 / HTTP-level policies.
- Egress to `loco-system` from app pods: apps call the API through its public domain.
