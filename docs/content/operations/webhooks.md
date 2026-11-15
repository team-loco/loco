# Webhooks

Install webhooks deliver platform events to operator-configured HTTP(S) consumers. Configure `INSTALL_WEBHOOKS` on the API with a JSON array of objects containing `url`, `secret`, and `eventTypes`; an empty `eventTypes` array receives every event. Each signing secret is `whsec_` followed by standard base64 encoding of exactly 32 bytes. Keep these credentials in the API environment Secret.

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

`deployment.failed` records a deployment entering the failed status in one region, once per transition. `build.failed` records a failed build. Their `actor` is whoever created the deployment or build: `user` with the user ID, or `api_token` with the token ID. `build.succeeded` carries the same actor. Both carry these `data` fields:

| Field | Value |
| --- | --- |
| `resourceId` | Resource ID |
| `resource_name` | Resource name |
| `buildId` | Build ID; on `deployment.failed`, only when the deployment runs a build |
| `message` | Failure message |
| `creator_id` | ID of the user who created it, or of the user who created the API token |
| `creator_name` | That user's name, or an empty string when unset |
| `creator_email` | That user's email address |

The `creator_*` fields are absent when that user no longer exists, and on `deployment.failed` also when the API token no longer exists. `deployment.failed` also carries `deploymentId`, `environmentId` and `region`. `build.failed` also carries `reason`: `source_upload_expired` when the source was not uploaded before its upload URL expired, otherwise `build_failed`.

## User deletion

`user.deleted` records a deleted Loco user. Its `subject.id` is the user ID, and its `data.identities` lists the sign-in identities removed with the user, each with the `issuer` and `subject` that the identity provider put in its tokens. Loco erases those identities when the user is deleted, so a consumer that removes the matching accounts at an identity provider takes them from this list. A retried delivery carries the same list.
