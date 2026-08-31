# Cross-region gateway-to-gateway failover

Working demo of L7 cross-region failover between two Kubernetes clusters **with no
pod-network connectivity between them** — the alternative to Cilium Cluster Mesh,
which needs mutual node reachability and a shared L3 fabric.

Each region's Envoy Gateway carries the *other* region's public gateway as a
priority-1 fallback backend. A regional outage is absorbed at L7 over ordinary
HTTP between two public endpoints. Nothing is meshed; the clusters share no CIDRs,
no CNI state, and no control plane.

Verified end to end on kind + OrbStack, Envoy Gateway **v1.9.0**, Gateway API v1.

```
   client ──▶ eu-west-1 gateway ──▶ eu-west-1 pods        (priority 0, normal)
                     │
                     └───────────▶ us-east-1 gateway ──▶ us-east-1 pods
                                   (priority 1, only when priority 0 is unhealthy)
```

## Run it

```bash
./setup.sh      # two kind clusters + Envoy Gateway + apps + failover routing (~6 min)
./demo.sh       # the four scenarios below
./inspect.sh eu # Envoy's actual priority levels and live host health
./teardown.sh   # delete both clusters
```

`setup.sh` is idempotent and leaves any other kind clusters alone.

## What the demo proves

| # | Scenario | Result |
|---|---|---|
| 1 | Both regions healthy | 100% served by `eu-west-1`. No traffic leaks to the fallback. |
| 2 | EU app scaled to zero | `200`s, served by `us-east-1`, stamped `forwarded-by: eu-west-1`. |
| 3 | EU down, request arrives already stamped by a peer | `503` locally. Does **not** hop back. |
| 4 | EU scaled back up | Traffic returns to `eu-west-1` automatically. No intervention. |

Scenario 3 is the important one. Without the loop guard, a global brownout has EU
forwarding to US, US forwarding back to EU, and the two gateways amplifying a
failure into a traffic storm between regions.

## Verifying the controller's output

The scenarios above run against hand-written manifests. To re-run them against the
manifests `loco-controller` actually generates:

```bash
cd ../../controller
EMIT_MANIFESTS=1 go test ./internal/controller -run TestEmitManifests
# testdata/generated-failover.yaml now holds the controller's output
```

Point the peer port at the live US gateway, drop the empty status block, replace this
experiment's route, and apply:

```bash
US_NP=$(kubectl --context kind-fo-us get svc -n envoy-gateway-system \
  -l gateway.envoyproxy.io/owning-gateway-name=eg \
  -o jsonpath='{.items[0].spec.ports[?(@.port==80)].nodePort}')
sed -e "s/us-east-1.deploy-app.com/us-east-1.gw.demo.local/" -e "s/port: 443/port: $US_NP/" -e '/^status:/,/^  parents: null$/d' \
  ../../controller/internal/controller/testdata/generated-failover.yaml > /tmp/gen.yaml
kubectl --context kind-fo-eu delete httproute demo-app backendtrafficpolicy demo-app-health --ignore-not-found
kubectl --context kind-fo-eu apply -f /tmp/gen.yaml
./inspect.sh eu   # expect priority=0 / priority=1, not two CLUSTER lines
./demo.sh
```

All four scenarios pass against controller-generated config.

## Findings

### 1. `Backend.spec.fallback` silently degrades to a weighted split

This is the thing to know. Envoy Gateway only compiles multiple `backendRefs` into a
**single Envoy cluster with priority levels** when *every* backendRef is a `Backend`
resource using **`fqdn`** endpoints. Get it wrong and EG emits two clusters behind a
`weighted_clusters` route at weight 1 each — a 50/50 split across regions, all the time.

There is no warning, no status condition, and no log line. The route reports
`Accepted` and `ResolvedRefs`. It just quietly does the opposite of failover.

Observed, in order:

| Config | Result |
|---|---|
| Service backendRef + Backend(`ip`, fallback) | two clusters, weighted 50/50 ❌ |
| Backend(`fqdn`) + Backend(`ip`, fallback) | two clusters, weighted 50/50 ❌ |
| Backend(`fqdn`) + Backend(`fqdn`, fallback) | **one cluster, priority 0 / priority 1** ✅ |

Differing ports between the two backends are fine (80 vs a NodePort merged
correctly). The `ip` endpoint type is what breaks it.

**Always verify with `./inspect.sh`.** A working config looks like:

```
priority=0 [{'address': 'demo-app.default.svc.cluster.local', 'port_value': 80}]
priority=1 [{'address': 'us-east-1.gw.demo.local', 'port_value': 32041}]
```

Two `CLUSTER` lines instead of two `priority=` lines means it is broken.

### 2. The `Backend` API is disabled by default

The CRD installs, but any route referencing one fails with `Backend is disabled in
Envoy Gateway configuration`. Needs:

```
--set config.envoyGateway.extensionApis.enableBackend=true
```

### 3. Loop prevention has to be a rule-level filter, not a per-backendRef filter

The natural way to write the loop guard is a `RequestHeaderModifier` on the fallback
backendRef only — stamp the header exactly when failing over. **That silently breaks
the priority merge**, for a good reason: Envoy cannot mutate headers per priority
level within one cluster, so EG is forced back into separate weighted clusters.

So the header is stamped at rule level on every request leaving the rule. A locally
served request carries a header its app ignores; a request that crosses to the peer
carries the signal the peer needs. The header therefore means *"a gateway already
forwarded this — do not forward it again"*, which is why it is named
`x-loco-forwarded-by` rather than `x-loco-failover`.

### 4. Failover is binary, not proportional — and that is what you want

Envoy's priority failover uses an overprovisioning factor of 1.4, so priority 1 starts
receiving traffic once priority 0 health drops below ~72%, and spills over
proportionally after that.

That does not happen here. Because the local backend is an `fqdn` Backend pointing at
a **ClusterIP Service**, Envoy sees exactly one host — the service VIP — not the
individual pods:

```
10.21.186.229:80    -> HEALTHY     # the Service VIP, with 4 pods behind it
192.168.97.6:32041  -> HEALTHY     # peer gateway
```

Per-pod health is kube-proxy's problem behind the VIP. Envoy's view is one host, so
failover is all-or-nothing: the Service is reachable or it isn't.

This is a consequence of finding 1 — the config that gives Envoy per-pod visibility
(a Service backendRef, EDS) is exactly the config that refuses to build priority
levels. You cannot have both.

For cross-region failover this is the correct behaviour anyway. You do not want 30%
of EU traffic crossing the Atlantic because one pod is briefly unready. But it should
be a decision, not a surprise: **this mechanism fails over regions, it does not
load-balance degraded ones.**

### 5. Detection speed is bounded by health-check config, not DNS

Failover in scenario 2 is effectively immediate — no DNS TTL, no propagation. The
`BackendTrafficPolicy` passive health check governs it:

```yaml
healthCheck:
  passive:
    baseEjectionTime: 5s
    interval: 2s
    consecutive5XxErrors: 1
    consecutiveLocalOriginFailures: 1   # catches connection-refused from a 0-endpoint Service
```

`retry` with `connect-failure` and `reset` triggers absorbs the first error that would
otherwise reach the client, which is what makes the transition invisible.

## What this does not solve

- **Stateless apps only.** If the app's database is in EU, failing HTTP over to US
  pods that cannot reach it converts a timeout into a faster 500. This must be opt-in
  per app and documented as such.
- **App failure vs region failure.** Scenario 2 scales the app to zero, which looks
  identical to a regional outage from the gateway's point of view. In production these
  are different events with different correct responses: a bad deploy is broken in
  *both* regions, and failing over just points full global traffic at the other copy of
  the same broken app. The trigger needs to distinguish them — the loco-agent heartbeat
  tells you whether the *cluster* is alive independently of whether the app is.
- **Latency.** A failed-over request pays the full cross-region RTT. This is a bridge
  until DNS/edge steering catches up, not a steady state.
- **TLS between gateways.** This demo runs plaintext HTTP between the two gateways. In
  production that hop is public internet and needs HTTPS with SNI, plus a
  `BackendTLSPolicy`. Host header preservation needs re-checking under TLS.
- **The capacity question.** If EU fails over to US, US now serves both regions' traffic.
  Nothing here provisions for that. Failover without headroom is a slower outage.

## Files

| File | Purpose |
|---|---|
| `setup.sh` | Builds everything from scratch, idempotent |
| `demo.sh` | The four scenarios |
| `hit.sh` | `./hit.sh <eu\|us> [count] [extra-header]` — send traffic, report which region served it |
| `inspect.sh` | `./inspect.sh <eu\|us>` — dump Envoy priority levels and host health |
| `teardown.sh` | Delete both clusters |
| `manifests/00-gateway.yaml` | GatewayClass, EnvoyProxy (NodePort), Gateway |
| `manifests/10-app.yaml` | Demo app, `__REGION__` substituted at apply time |
| `manifests/20-failover.yaml` | Backends, HTTPRoute with loop guard, BackendTrafficPolicy |
