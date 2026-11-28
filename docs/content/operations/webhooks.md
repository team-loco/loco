---
title: "Webhooks"
---

Install webhooks deliver platform events to operator-configured HTTP(S) consumers. Configure `INSTALL_WEBHOOKS` on the API with a JSON array of objects containing `url`, `secret`, and `eventTypes`; an empty `eventTypes` array receives every event. Each signing secret is `whsec_` followed by standard base64 encoding of exactly 32 bytes. Keep these credentials in the API environment Secret.

Operators route install webhook events to any notifier, such as ntfy or Slack, by pointing an install webhook at a receiver they run that forwards the events.

```json
[
  {
    "url": "https://notifications.example.com/events",
    "secret": "whsec_BASE64_SIGNING_KEY",
    "eventTypes": ["invitation.created", "invitation.resent"]
  }
]
```

The example's signing key is a placeholder. The API seals configured signing keys with the secrets key provider at startup.

Events enter the webhook outbox in the transaction that changes platform state. The dispatcher posts them asynchronously and retries failures according to `WEBHOOK_RETRY_DELAYS`; `WEBHOOK_TIMEOUT` bounds each attempt. An invitation RPC does not wait for the consumer. Consumers must handle repeated deliveries.

Requests carry `Webhook-Id`, `Webhook-Timestamp`, and `Webhook-Signature`. Verify the HMAC-SHA256 signature over `Webhook-Id + "." + Webhook-Timestamp + "." + raw request body` using the decoded signing key. The signature header contains `v1,` followed by the base64 signature; secret rotation can produce multiple space-separated signatures. Accept a valid signature and reject stale timestamps before processing the event.

## Workspace webhooks

A workspace webhook delivers the events of one workspace to an HTTPS endpoint. Workspace admins create and manage them on the workspace's **Webhooks** page or through `WebhookService`; every `WebhookService` call requires `workspace:admin`. Deliveries use the outbox, retries, headers and signatures described above.

### Create a webhook

On the **Webhooks** page, choose **New webhook**, enter an optional name and the endpoint URL, and pick **All events** or **Choose events**. Saving shows the signing secret once; copy it before closing the dialog. `WebhookService.CreateWebhook` does the same and returns the secret in `secret`:

```sh
curl -sS "$LOCO_API/loco.webhook.v1.WebhookService/CreateWebhook" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"workspaceId":"<workspace-id>","name":"Deploy alerts","url":"https://hooks.example.com/loco","eventTypes":["build.failed"]}'
```

| Field | Value |
| --- | --- |
| `workspace_id` | Workspace that owns the webhook |
| `url` | Endpoint, at most 2048 characters: `https` with a host name and no user info. With `WEBHOOK_ALLOW_PRIVATE_NETWORKS` on the API, `http` and private addresses are accepted too |
| `event_types` | Event types to deliver, at most 100; an empty list delivers every event |
| `name` | Optional label of at most 100 characters, shown in the webhook list |

`UpdateWebhook` replaces a webhook's name, URL and event types, and pending retries go to the new URL. `DeleteWebhook` removes the webhook and its deliveries. The row's menu on the **Webhooks** page offers both as **Edit** and **Delete**.

A workspace holds at most 20 webhooks, the API's `LOCO_LIMIT_WEBHOOKS_PER_WORKSPACE`. A create past the limit fails with `ResourceExhausted`. A system admin can raise or lower it for one organization with `OrgService.SetOrgLimits` (`webhooks_per_workspace`); see [count limits](access.md#count-limits).

### Event types

A webhook receives each event recorded in its workspace whose type it subscribes to, or every such event when its list is empty. **Choose events** offers these types:

| Group | Event types |
| --- | --- |
| Runtime alerts | `resource.degraded`, `resource.recovered` |
| Deployments | `deployment.created`, `deployment.canceled`, `deployment.deleted` |
| Builds | `build.created`, `build.started`, `build.succeeded`, `build.failed`, `build.canceled` |
| Resources | `resource.created`, `resource.updated`, `resource.deleted`, `resource.scaled`, `resource.restarted`, `resource.partial_transferred` |
| Domains | `domain.created`, `domain.updated`, `domain.deleted` |
| Environments | `environment.created`, `environment.updated`, `environment.deleted` |
| Secrets | `secret.set`, `secret.deleted`, `secret_key.rewrapped` |
| Members | `member.added`, `member.removed` |
| Tokens | `token.created`, `token.revoked` |
| Workspace | `workspace.created`, `workspace.updated`, `workspace.deleted`, `webhook.created`, `webhook.updated`, `webhook.secret_rotated`, `webhook.deleted` |

`event_types` in the API takes any event type name. `deployment.ready`, `deployment.failed` and `deployment.crashed` reach a webhook that subscribes to all events or names them through the API.

**Send test event** on a webhook's row, or `SendTestEvent`, records a `webhook.test` event and delivers it to that webhook only, whatever event types it subscribes to.

### Rotate the signing secret

Choose **Rotate signing secret** in the webhook's menu and pick how long the old secret keeps working: **None**, **1 hour**, **24 hours** or **7 days**. The dialog shows the new secret once. `RotateWebhookSecret` takes the same choice as `grace_period_seconds`, from `0` to `604800`, and returns the new secret in `secret`.

During the grace period `Webhook-Signature` carries one signature per secret. The webhook's `previous_secret_expires_at` and the **Signing** row of its detail panel show when the old secret stops. With `0` the old secret stops at once. Rotating again during a grace period drops the older of the two secrets.

### Deliveries and redelivery

Select a webhook on the **Webhooks** page to open its deliveries, newest first, and switch between **All** and **Failing**. A delivery succeeds when the endpoint answers with a 2xx status. A 4xx response other than 408 (request timeout) or 429 (rate limit) ends the delivery as **Failed** immediately. Other failures are **Retrying** while attempts remain in `WEBHOOK_RETRY_DELAYS` (by default 30 seconds, 2 minutes, 10 minutes, 1 hour, 6 hours and 24 hours apart), and **Failed** after the last one; each failing delivery shows its last error and attempt count.

The **Redeliver** button on a delivered or failed delivery sends it again with the same `Webhook-Id` and restarts its retry schedule. Through the API, `ListWebhookDeliveries` lists deliveries with an optional `status` (`pending`, `succeeded` or `failed`) and `failing` filter, and `RedeliverWebhookDelivery` takes the delivery ID. Redelivering a pending delivery fails with `FailedPrecondition`.

### Verify a signature

Any [Standard Webhooks](https://www.standardwebhooks.com/) library verifies Loco deliveries. Without one, this Go function checks a request against the webhook's signing secret and rejects timestamps more than five minutes off:

```go
func verify(secret string, h http.Header, body []byte) bool {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return false
	}
	ts, err := strconv.ParseInt(h.Get("Webhook-Timestamp"), 10, 64)
	if err != nil || time.Since(time.Unix(ts, 0)).Abs() > 5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%s.%d.", h.Get("Webhook-Id"), ts)
	mac.Write(body)
	want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	for _, sig := range strings.Fields(h.Get("Webhook-Signature")) {
		if hmac.Equal([]byte(sig), []byte(want)) {
			return true
		}
	}
	return false
}
```

## Invitation events

Core sends no email. A deployment that wants invitation email consumes `invitation.created` and `invitation.resent` and renders the email itself. Both events carry the same `data` fields:

| Field | Value |
| --- | --- |
| `email` | Invitee's normalized email address |
| `org_name` | Organization name |
| `inviter_name` | Original inviter's name, or an empty string when unset |
| `inviter_email` | Original inviter's email address |
| `expires_at` | Invitation expiry in UTC, formatted as RFC3339 |
| `link` | Sign-in URL: `WEB_URL` plus `/login?login_hint=` and the URL-encoded invitee email |

The enclosing event includes `orgId`, `subject.type` (`invitation`), and `subject.id` (the invitation ID). Its `actor` identifies the user who performed the action; a resend can have a different actor from the original inviter.

`invitation.created` records a new invitation. `invitation.resent` extends an open invitation's expiry, including an expired invitation that has not been revoked or claimed, and records the renewed expiry. Both changes and their events commit together. Claimed or revoked invitations cannot be resent. Invitation responses always include the link to copy; webhook delivery results do not change an invitation.

## Failure events

`deployment.failed` records a deployment entering the failed status, which happens once a region of it fails. `deployment.crashed` records a deployment whose replica restarted more often than its failure policy allows: a first version Loco stopped during its rollout, or a running version whose replica keeps restarting, once per transition; its data matches `deployment.failed`. When the previous version takes over, because its replicas still run or it proved itself by serving for the health window, the stopped deployment fails and records `deployment.failed` instead, as does a deployment that missed its deploy timeout. Both carry `restarts`, the replica's restarts since it last stayed up for the health window, when Loco stopped the version for restarts. `build.failed` records a failed build. Their `actor` is whoever created the deployment or build: `user` with the user ID, or `api_token` with the token ID. `build.succeeded` carries the same actor. Both carry these `data` fields:

| Field | Value |
| --- | --- |
| `resourceId` | Resource ID |
| `resource_name` | Resource name |
| `buildId` | Build ID; on `deployment.failed`, only when the deployment runs a build |
| `message` | Failure message |
| `creator_id` | ID of the user who created it, or of the user who created the API token |
| `creator_name` | That user's name, or an empty string when unset |
| `creator_email` | That user's email address |

The `creator_*` fields are absent when that user no longer exists, and on `deployment.failed` also when the API token no longer exists. `deployment.failed` also carries `deploymentId`, `environmentId` and `regions`, the regions that failed. `deployment.ready` carries the same fields, with every region the deployment runs in, and `deployment.created` carries `regions`, the regions it targets. `build.failed` also carries `reason`: `source_upload_expired` when the source was not uploaded before its upload URL expired, otherwise `build_failed`.

## Cancellation events

`deployment.canceled` records a deployment that ended `canceled`, once. Its `subject.id` is the deployment ID and `data.deploymentId` repeats it. When a user or API token canceled it, `actor` is that caller, and `data.regionsReverted` and `data.regionsKept` list the regions that went back to the previous deployment and the regions that kept this one, and `data.rollbackDeploymentIds` lists the rollback deployments queued for the kept regions. When a newer deployment replaced it, `actor.type` is `system` and `data.supersededBy` is the newer deployment's ID.

## User deletion

`user.deleted` records a deleted Loco user. Its `subject.id` is the user ID, and its `data.identity` is the sign-in identity removed with the user, with the `issuer` and `subject` that the identity provider put in its tokens. Loco erases that identity when the user is deleted, so a consumer that removes the matching account at an identity provider takes it from this object. A retried delivery carries the same object.

## Cluster events

Cluster events belong to the installation, not to an organization, so install webhooks receive them and workspace webhooks do not. Each names the cluster in `name` and `region`, and has `subject.type` `cluster` with the cluster ID as `subject.id`. A paused or removed cluster records no `cluster.silent`; a paused cluster's heartbeats still record the other two.

| Event | Recorded when | Extra `data` fields |
| --- | --- | --- |
| `cluster.silent` | A cluster's agent has not reported for three heartbeat intervals (`LOCO_AGENT_HEARTBEAT_INTERVAL`, 30 seconds by default), the point at which the API stops placing work there | none |
| `cluster.recovered` | A silent cluster's agent reports again | `silentSeconds`, the time from the last heartbeat before the silence to the heartbeat that ended it |
| `cluster.health_changed` | The agent reports a different health than its previous heartbeat: `healthy`, `degraded` or `unhealthy` | `old`, `new` |

Each transition records one event, whatever the number of API replicas or restarts. A cluster's first heartbeat records no `cluster.health_changed`.
