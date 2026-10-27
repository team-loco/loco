# Usage metering

Loco records how long each application pod runs with the CPU and memory it is allocated, and how many bytes each service sends to clients through the gateway. It serves both to system admins as change feeds. An operator reads the feeds to show usage per service, charge back teams or feed an external rating system.

## Workload runs

A workload run is one application pod: its Kubernetes UID, its cluster and region, the organization, workspace, service and environment it belongs to, the sum of its containers' CPU and memory requests, when it started running, when the agent last saw it running, and when it ended. The controller sets each container's limits equal to its requests and changes them only by rolling out new pods, so a run's allocation is fixed for its whole life and its usage is the allocation multiplied by `[started_at, ended_at]`, or `[started_at, last_seen_at]` while it runs.

The agent on each worker cluster watches the pods the controller labels as application pods and sends a usage report over its sync stream every `agent.usageInterval` (`1m` by default) and whenever a pod stops. A report lists every pod that has started at least one container, with the time Kubernetes started its first container, so a pod still pulling its image is not metered. A pod that stops is reported with the time its last container finished, or with the time the agent saw it deleted when Kubernetes recorded none. The agent keeps stopped pods in memory and repeats them in every report until the API acknowledges one, so a stop that happens while the agent is disconnected arrives after it reconnects. If the agent restarts in that window, the run ends at its last report.

The API keeps one row per pod UID. A report only moves `started_at` earlier and `last_seen_at` later, and sets `ended_at` once, after which `last_seen_at` equals it; a repeated or reordered report changes nothing. Times later than the API's clock are clamped to it.

## Gateway egress

Every usage report also carries, for each running proxy of the cluster's Gateway, Envoy's cumulative `upstream_cx_rx_bytes_total` counter of every route: the bytes a service's pods send back through the gateway, which the gateway forwards to clients. Traffic between services inside the cluster and traffic a pod sends straight to the internet do not pass the gateway and are not counted. The agent reads the counters from Envoy's admin interface, which listens on `127.0.0.1:19000` inside the proxy pod, through a Kubernetes port-forward (`create` on `pods/portforward` in the release namespace), so the admin interface is never reachable over the network. It attributes the route `httproute/<namespace>/resource-<id>-route/rule/<n>` to the organization, workspace and environment of the Application for that service.

The API keeps the last value of each counter by gateway pod UID and route, and adds each increase to the hourly bucket of the service and environment for the hour of the report. A value lower than the last one is a counter reset and counts in full. The first value of a counter only sets its baseline, so bytes a new gateway pod or route serves before the agent first reads it are not counted, and neither are bytes a gateway serves between its last read and a restart.

## Coverage

Each report the API records also marks its cluster as reported up to the report's time, separately for each feed. `metered_through` is the earliest of those times over every enrolled cluster that is not removed, and is absent while any of them has never reported. Every run that was running at `metered_through` has been recorded up to at least that time. A report advances egress coverage only if the agent read every running proxy. A paused cluster still counts, because its pods keep running; a removed one does not.

## Read the feed

`loco.usage.v1.UsageService` requires the `system:admin` scope. `ListWorkloadRuns` returns runs in the order they last changed, with an opaque `next_cursor`. Start with an empty cursor and pass back `next_cursor` until it comes back equal to the cursor you sent; a run appears again whenever it changes, so keep the latest version of each `id`.

```sh
curl -sS -X POST https://api.example.com/loco.usage.v1.UsageService/ListWorkloadRuns \
  -H "Authorization: Bearer $LOCO_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"cursor": "", "pageSize": 500}'
```

`ListEgressUsage` works the same way. Each row is one service and environment on one cluster for one UTC hour, with the bytes counted for that hour so far; keep the latest version of each `(cluster_id, resource_id, environment_id, hour_start)`.

`page_size` defaults to 500 and allows at most 1000. A feed returns a change only once every transaction that started before it has finished, so a cursor never moves past a change that a slower transaction commits later.

## Verification

`mise run e2e:usage` deploys an application in a kind cluster, checks that its pod becomes a run with the requested allocation, deletes the pod and checks that the run ends and the replacement pod gets its own run, then sends requests through the gateway and checks that the service's egress grows.
