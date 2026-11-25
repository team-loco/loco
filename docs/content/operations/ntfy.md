---
title: "ntfy notifications"
---

Loco publishes selected events to a topic on an [ntfy](https://ntfy.sh) server, so an operator's phone or desktop shows a notification within seconds of a failure. Run the ntfy server outside the Loco install, so notifications still arrive when Loco is down.

## Configure the API

Set the server and topic on the API. Without them, notifications are off.

| Variable | Effect |
| --- | --- |
| `NTFY_URL` | Base URL of the ntfy server, such as `https://ntfy.example.com`. |
| `NTFY_TOPIC` | Topic to publish to. |
| `NTFY_TOKEN` | Optional access token, sent as a bearer token. |
| `NTFY_EVENT_TYPES` | Optional comma-separated list of [event types](webhooks.md#cluster-events) to notify. |

With the control-plane chart, set the same values under `api.ntfy`. The token comes from a Secret:

```yaml
api:
  ntfy:
    url: https://ntfy.example.com
    topic: loco-alerts
    tokenSecret:
      name: loco-ntfy
      key: token
    eventTypes: []
```

An empty `eventTypes` notifies these events:

| Event | Notification |
| --- | --- |
| `build.failed` | High priority: the build failed, naming the organization, workspace and resource |
| `deployment.failed`, `deployment.crashed` | High priority: the deployment failed or keeps restarting |
| `cluster.silent` | Urgent priority: a cluster's agent stopped reporting |
| `cluster.recovered` | A silent cluster reports again |
| `cluster.health_changed` | A cluster's reported health changed, with the old and new state |
| `org.suspended` | An organization was suspended, with the reason |

Any other event type in `eventTypes` is published with the type as its title and the organization, workspace and resource names as its message.

Build and deployment notifications open the resource's page on the dashboard, and `org.suspended` opens the organization's audit page; they need `WEB_URL` set on the API. Cluster notifications carry no link.

## Delivery

An ntfy topic is an install delivery target. Matching events enter the same outbox as [install webhooks](webhooks.md) in the transaction that records them, so each event is published once, survives API restarts and works with several API replicas. A failed publish is retried on the `WEBHOOK_RETRY_DELAYS` schedule, and the delivery row keeps its attempt count and last error, in `webhook_deliveries` like every other webhook delivery. ntfy answers with a 2xx status when it accepts a message.
