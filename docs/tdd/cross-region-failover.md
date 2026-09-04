# Technical Design Document: Cross-Region Failover

**Status:** Approach accepted; mechanism verified end to end against two live clusters (see [`experiments/gateway-failover`](../../experiments/gateway-failover/README.md)). The controller implementation lands separately.
**Supersedes:** the Cilium Cluster Mesh approach. `experiments/mcs` is kept for reference.

## Summary

An app deployed to several regions should survive losing one of them. This is done at **L7, between gateways**: each region's Envoy Gateway carries the other regions' public gateways as lower-priority backends. A regional outage is absorbed over ordinary HTTPS between two public endpoints.

The clusters share no pod network, no CIDRs, no CNI state, and no control plane.

## Why not Cilium Cluster Mesh

Cluster Mesh was the original plan and is now abandoned. The commonly cited blocker — that it needs L3 connectivity between clusters — is real but the weakest of the reasons:

- **It contradicts decisions already made.** `notes.md` requires shutting down cross-cluster traffic for `managed-by-loco` namespaces, and requires all apps in a workspace to land on the same cluster. Cluster Mesh is the opposite of both.
- **It re-couples failure domains.** A shared L3 fabric means a partition or a bad NetworkPolicy in one region can affect another. Multi-region exists to decouple failure domains; meshing them gives back exactly what was paid for.
- **It does not even win on latency.** `EU user -> EU LB -> tunnel -> US pod` is strictly worse than `EU user -> edge -> US LB -> US pod`. It adds a hop and keeps the ocean.
- **Operational weight.** `clustermesh-apiserver` is another stateful component with its own certificate lifecycle and N² connections between clusters.

Technically it is buildable — with encapsulation plus WireGuard you need only node-to-node reachability, which DOKS nodes have. It is simply the wrong shape.

`experiments/mcs` is left in place: the Cilium and multi-cluster-services setup there is still the reference if the question is ever revisited.

## Mechanism

Envoy supports **priority levels** within a cluster: priority 1 receives traffic only when priority 0 cannot absorb it. Envoy Gateway exposes this through `Backend.spec.fallback`.

```
   client ──▶ eu-west-1 gateway ──▶ eu-west-1 pods        (priority 0, normal)
                     │
                     └───────────▶ us-east-1 gateway ──▶ us-east-1 pods
                                   (priority 1, only when priority 0 is unhealthy)
```

For each failover-enabled Application the controller creates:

| Resource | Purpose |
|---|---|
| `Backend <app>-local` | the local Service, by cluster DNS name — priority 0 |
| `Backend <app>-peers` | every peer region's gateway, `fallback: true` — priority 1 |
| `HTTPRoute <app>-route` | loop-guard rule, then a primary rule spanning both Backends |
| `BackendTrafficPolicy <app>-failover` | passive health checks + retries |

### Routing and upstream selection are separate layers

This is the part that governs everything else. The HTTPRoute's primary rule points at **one** Envoy cluster; failover happens *inside* that cluster at load-balancing time. The route layer has no concept of failover, and cannot be given one.

Two consequences follow, and both are load-bearing:

**The forwarded-by stamp must be rule-level.** The intuitive design — stamp `x-loco-forwarded-by` only on the fallback backendRef, exactly when failing over — asks the routing layer to know something only the load balancer knows. Envoy Gateway's response is to split the cluster into two weighted clusters, which destroys the priority relationship and silently produces a permanent 50/50 traffic split across regions. So the header is stamped on every request leaving the rule. A locally served request carries a header its app ignores; a request that crosses to a peer carries the signal the peer needs. Hence the name: *forwarded-by*, not *failover*.

**Failover is binary, not proportional.** Envoy's 1.4 overprovisioning factor would normally spill traffic over gradually once priority-0 health drops below ~72%. That does not happen here, because the local Backend addresses a ClusterIP Service — Envoy sees one host (the VIP), not the individual pods. Per-pod health remains kube-proxy's concern behind the VIP.

This is not a limitation that can be engineered away: the configuration that gives Envoy per-pod visibility (a Service backendRef, via EDS) is precisely the configuration that refuses to build priority levels. For cross-region failover the binary behaviour is correct anyway — sending 30% of EU traffic across the Atlantic because one pod is briefly unready would be worse — but it is a decision, not an accident.

### The loop guard

Without it, a failure affecting both regions has EU forwarding to US, US forwarding back to EU, and the two gateways amplifying one outage into a traffic storm between regions.

Rule 0 matches requests that already carry `x-loco-forwarded-by` and pins them to the local backend, with no fallback. A gateway will forward a request at most once.

> The header is currently trusted unconditionally, so a client can set it and opt itself out of failover. That is self-inflicted and not a cross-tenant issue, so it is accepted for now. The clean fix — strip it from client traffic at the edge and honour it only on an authenticated gateway-to-gateway path — pairs naturally with adding TLS between gateways.

### Detection and recovery

Failover is driven by passive health checking, not DNS, so there is no TTL or propagation delay:

```yaml
healthCheck:
  passive:
    baseEjectionTime: 5s
    interval: 2s
    consecutive5XxErrors: 1
    consecutiveLocalOriginFailures: 1   # a zero-endpoint Service refuses connections
retry:
  numRetries: 2
  retryOn: {triggers: [connect-failure, reset, 5xx]}
```

`consecutiveLocalOriginFailures` is what actually fires on a scaled-to-zero deployment: a Service with no ready endpoints refuses connections rather than returning 5xx. The retry policy absorbs the first failure — the one arriving before ejection takes effect — so clients see a slightly slow success instead of a 503. Recovery is automatic: after `baseEjectionTime` Envoy un-ejects and re-probes the local host, and traffic returns with no controller involvement.

## The sharp edge

**`Backend.spec.fallback` silently degrades to a weighted split** unless *every* backendRef in the rule is a `Backend` resource using an **`fqdn`** endpoint. With a Service backendRef, or a Backend using an `ip` endpoint, Envoy Gateway emits two clusters behind a `weighted_clusters` route at weight 1 each — a permanent 50/50 split across regions.

There is no warning, no status condition, and no log line. The route reports `Accepted` and `ResolvedRefs`. It quietly does the opposite of failover.

Observed while building this:

| Config | Result |
|---|---|
| Service backendRef + Backend(`ip`, fallback) | two clusters, weighted 50/50 ❌ |
| Backend(`fqdn`) + Backend(`ip`, fallback) | two clusters, weighted 50/50 ❌ |
| Backend(`fqdn`) + Backend(`fqdn`, fallback) | one cluster, priority 0 / priority 1 ✅ |

Ports may differ freely between the two backendRefs; the `ip` endpoint type is what breaks it. This is why `clusters.gateway_hostname` is an FQDN column rather than an address, why the CRD documents the constraint on `FailoverPeer.Gateway`, and why `TestBackendUsesFQDNEndpoint` asserts it.

## Data model

`clusters.gateway_hostname` (added to `002_apps_and_deployments.sql`, in the `clusters` table itself rather than as a follow-on migration — there are no deployments to migrate) holds each cluster's publicly resolvable gateway FQDN, e.g. `us-east-1.deploy-app.com`. NULL means the cluster cannot act as a failover peer.

The wildcard certificate in `loco-core` already provisions per-region names (`*.us-east-1.deploy-app.com`, `*.prod.us-east-1.deploy-app.com`), so the naming scheme this depends on predates the feature.

On deploy, `GetFailoverPeersForResource` returns the regions — other than the one being deployed to — where the resource has an active deployment on an active, healthy cluster that advertises a gateway hostname. Those become `ServiceSpec.Failover.Peers`.

**Peer lookup failure degrades rather than fails.** Failover is an availability enhancement; a database hiccup while resolving peers deploys the app single-region and logs a warning rather than failing the deploy.

`Enabled: true` with zero peers is a normal state — a single-region deployment — and the controller treats it identically to failover being off.

## What this does not solve

- **Stateless apps only.** If the app's data lives in EU, failing HTTP over to US pods that cannot reach it converts a timeout into a faster 500. Modern apps are largely stateless, which is why this is acceptable, but it should be documented for users rather than assumed.
- **App failure vs region failure.** From the gateway's point of view a bad deploy is indistinguishable from a regional outage, and a crash-looping app is broken in *both* regions — failing over just points full global traffic at the other copy of the same broken app. The `loco-agent` heartbeat already reports cluster liveness independently of app health, so the signal to distinguish them exists; using it is future work.
- **Capacity.** If EU fails over to US, US serves both regions' traffic. Nothing here provisions for that. Failover without headroom is a slower outage.
- **TLS between gateways.** Peers default to port 443 and cross-region hops traverse the public internet, so this needs a `BackendTLSPolicy` and Host-header verification under SNI before production use.
- **Latency.** A failed-over request pays the full cross-region RTT. This is a bridge, not a steady state; DNS/edge steering is the longer-term complement.

## Verification

`experiments/gateway-failover` builds two kind clusters with no pod-network connectivity and runs four scenarios: baseline (no leak to the fallback), regional failure (200s served by the peer), loop guard (503 rather than a second hop), and automatic recovery.

Once the controller generates these resources, the same four scenarios should be re-run against its output rather than the hand-written manifests, to confirm Envoy builds priority levels rather than a weighted split.

When the controller implementation lands it should pin each property whose violation is silent: fqdn endpoints, `fallback` on peers only, loop guard referencing no peer backend, backendRef ordering and kind, and the stamp being rule-level rather than per-backendRef.
