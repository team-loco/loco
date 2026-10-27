# Usage metering

Loco records how long each application pod runs and what CPU and memory it is allocated, and serves those records to system admins as a change feed. An operator reads the feed to show usage per service, charge back teams or feed an external rating system.

## Workload runs

A workload run is one application pod: its Kubernetes UID, its cluster and region, the organization, workspace, service and environment it belongs to, the sum of its containers' CPU and memory requests, when it started running, when the agent last saw it running, and when it ended. The controller sets each container's limits equal to its requests and changes them only by rolling out new pods, so a run's allocation is fixed for its whole life and its usage is the allocation multiplied by `[started_at, ended_at]`, or `[started_at, last_seen_at]` while it runs.

The agent on each worker cluster watches the pods the controller labels as application pods and sends a usage report over its sync stream every `agent.usageInterval` (`1m` by default) and whenever a pod stops. A report lists every pod that has started at least one container, with the time Kubernetes started its first container, so a pod still pulling its image is not metered. A pod that stops is reported with the time its last container finished, or with the time the agent saw it deleted when Kubernetes recorded none. The agent keeps stopped pods in memory and repeats them in every report until the API acknowledges one, so a stop that happens while the agent is disconnected arrives after it reconnects. If the agent restarts in that window, the run ends at its last report.

The API keeps one row per pod UID. A report only moves `started_at` earlier and `last_seen_at` later, and sets `ended_at` once, after which `last_seen_at` equals it; a repeated or reordered report changes nothing. Times later than the API's clock are clamped to it.

## Coverage

Each report the API records also marks its cluster as reported up to the report's time. `metered_through` is the earliest of those times over every enrolled cluster that is not removed, and is absent while any of them has never reported. Every run that was running at `metered_through` has been recorded up to at least that time. A paused cluster still counts, because its pods keep running; a removed one does not.

## Read the feed

`loco.usage.v1.UsageService` requires the `system:admin` scope. `ListWorkloadRuns` returns runs in the order they last changed, with an opaque `next_cursor`. Start with an empty cursor and pass back `next_cursor` until it comes back equal to the cursor you sent; a run appears again whenever it changes, so keep the latest version of each `id`.

```sh
curl -sS -X POST https://api.example.com/loco.usage.v1.UsageService/ListWorkloadRuns \
  -H "Authorization: Bearer $LOCO_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"cursor": "", "pageSize": 500}'
```

`page_size` defaults to 500 and allows at most 1000. The feed returns a change only once every transaction that started before it has finished, so a cursor never moves past a change that a slower transaction commits later.

## Verification

`mise run e2e:usage` deploys an application in a kind cluster, checks that its pod becomes a run with the requested allocation, deletes the pod and checks that the run ends and the replacement pod gets its own run.
