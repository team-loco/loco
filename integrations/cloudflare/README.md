# Cloudflare public routing publisher

This optional process translates Loco's public routing intents into owned, proxied CNAME records. Install it separately from the control plane. Loco core holds exact cluster membership and publication acknowledgements; this module holds provider credentials and DNS ownership.

> Unreleased. Validate a disposable zone and public worker gateways before handing it production DNS ownership. Local protocol tests use synthetic provider responses and do not prove geographic steering.

## Shared destinations

Create destinations once per exact cluster set. For two workers, configure these three destinations:

| Eligible clusters | Shared destination | Operator configuration |
| --- | --- | --- |
| CA | `infra-ca.apps.example.com` | DNS-only origin record for the CA NodeBalancer |
| NY | `infra-ny.apps.example.com` | DNS-only origin record for the NY NodeBalancer |
| CA and NY | `infra-ca-ny.apps.example.com` | One shared proxied load balancer with CA and NY pools and proximity steering |

An application's CNAME points to one of these destinations. Adding apps creates DNS records, without creating more load balancers. A new cluster combination needs its own shared destination before the publisher can use it. Unknown combinations report an error and preserve the previous record.

Use a full authoritative Cloudflare DNS zone. A proxied CNAME can refer to the shared load balancer in that zone; the application hostname must also be proxied. Pool endpoints must use direct NodeBalancer addresses or DNS-only origin records, so a pool does not point back through the public proxy. Keep the original application HTTP Host and origin TLS server name when forwarding through shared destinations. Configure Full (strict) encryption and validate the gateway's certificate for the original app hostname. Avoid Host-header or origin-server-name overrides that replace it with the infrastructure alias. Cloudflare's [load balancer setup](https://developers.cloudflare.com/load-balancing/load-balancers/create-load-balancer/), [proxy modes](https://developers.cloudflare.com/load-balancing/understand-basics/proxy-modes/) and [proximity steering](https://developers.cloudflare.com/load-balancing/understand-basics/traffic-steering/steering-policies/proximity-steering/) describe the provider-side requirements.

Proximity prefers a nearby eligible region. It does not check each application's health or implement application-level regional failover. Shared infrastructure health monitors must not use one customer's application as the health signal for every customer. This publisher does not create pools, load balancers, monitors or TLS settings.

## Configure ownership

1. Reserve the infrastructure aliases before assigning application names. As an installation administrator, bind the app domain through `RoutingService.BindPublisher` with `reservedPrefixes: ["infra-"]`. Use the returned publisher UUID. See the [core protocol](../../docs/content/reference/public-routing.md).
2. Issue an owned Loco API token for that publisher with read and write scopes. It needs no application permissions.
3. Issue a Cloudflare API token with DNS read/edit permission restricted to the chosen zone. Obtain its zone ID from the provider dashboard. Keep both tokens in the publisher's Secret.
4. Copy `deploy/publisher.example.json` outside the checkout. Replace every example UUID, zone ID, API URL and destination. Use actual worker cluster UUIDs, not region names. Ordering inside a cluster set does not matter; duplicate IDs or duplicate sets fail startup.

`PUBLISHER_CONFIG_FILE` is the only environment setting. The JSON file is read once at startup, rejects unknown fields and specifies mounted token file paths. Tokens are read once; restart the single publisher after rotation. Run one replica with a Recreate strategy. The core writer lease also rejects overlapping sessions.

```sh
mise run test:cloudflare-publisher
mise exec -- go -C integrations/cloudflare build -o /tmp/loco-cloudflare-publisher .
PUBLISHER_CONFIG_FILE=/absolute/path/publisher.json /tmp/loco-cloudflare-publisher
```

The checked-in deployment is an operator template. Supply its configuration and token Secrets in your chosen namespace. Render `deploy/deployment.yaml` with `PUBLISHER_IMAGE` set to an explicitly selected published tag and digest, review the rendered YAML, then apply it through your operator repository. The main Loco charts do not install it. CI publishes the optional image as `ghcr.io/team-loco/loco-cloudflare-publisher:ci-COMMIT`; use its registry digest when deploying.

## Publication and recovery

The process starts from a full core snapshot and then follows its paginated change sequence. Before each provider operation it renews its writer session and bounds the operation and acknowledgement by the returned lease deadline. Each record carries `comment: loco-managed:PUBLICATION_UUID`. A conflicting record owned by another writer blocks changes, including withdrawal. Never adopt a wildcard or preexisting record by guessing its owner.

After a create or patch, the process reads the exact hostname back and checks type, target, proxy status, automatic TTL and ownership before acknowledging the current revision. Withdrawal deletes only its owned record and reads back absence. A lost create response is recovered through lookup on retry. A lost acknowledgement retries the existing record without creating another one. Superseded revisions cannot authorize retirement; the next feed update repairs provider state from the newer revision.

The default poll is five seconds with at most three publications per cycle across all configured bindings. Provider 429 responses pause provider work for Retry-After; other failures retry on later polls. Pending items are ordered by their next attempt to avoid a broken hostname starving later work. Confirmed records are audited once per day by default. An external change can remain undetected until that audit. Tune `auditSeconds` for your record count and provider quota; ordinary feed polling does not read every DNS record on every cycle.

Errors sent to core contain safe descriptions, excluding raw provider bodies, tokens and record identifiers. Stopping the process leaves durable intents and retirement deadlines in core. Restart replays them idempotently. In-flight provider requests can complete after cancellation; writer deadlines and subsequent reconciliation bound and repair this case rather than claiming atomicity across two services.

The implementation uses the provider's [DNS list](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/list/), [create](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/create/), [edit](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/edit/) and [delete](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/delete/) APIs.

## Pilot checks

Use a disposable domain and two public worker clusters. Record the app hostname, exact core revision, provider record readback and request result for CA-only, NY-only and both-region deployments. Send requests from both geographies and confirm the returned worker identity. Confirm the original Host and TLS name at each gateway. Then remove one placement and verify it continues serving through provider acknowledgement and drain, and disappears afterward. Repeat a hostname withdrawal with a publisher restart and a provider error.

No credentials or public zone have been configured by the local test workflow. Do not count synthetic protocol responses as completion of these pilot checks.
