# Technical Design Document: Secrets

**Status:** Proposed.
**Replaces:** plaintext env values in `placements.desired_spec`; platform credentials held as plain environment variables.

## Summary

Loco handles two kinds of secrets, and this document covers both:

- **User secrets.** The env vars a customer sets on an app, such as `STRIPE_API_KEY`. Loco stores them, delivers them to the right cluster and never needs to read them otherwise.
- **Platform secrets.** The credentials Loco itself runs on: OAuth client secrets, registry and database credentials, the tokens that connect agents and services.

Both end up behind one OpenBao instance. User secrets use envelope encryption: each app has its own data key, OpenBao's Transit engine wraps that key, and Postgres stores only ciphertext. Platform secrets move into OpenBao's KV engine, and services fetch them with short-lived credentials instead of reading long-lived values from their environment.

The security boundary is stated plainly. A copy of Postgres alone reveals no secret. A compromised control-plane API that is authorized to use the Transit key can still decrypt everything it is allowed to. OpenBao governs and audits key use; it cannot make a compromised caller trustworthy.

## Where secrets live today

### User secrets

Env vars arrive through `UpdateResourceEnv` and the deploy and scale paths in `api/service/resource.go`. Since desired-state sync (#257) they are stored with the rest of the app's spec:

1. The API builds an `ApplicationPayload` (`api/service/deployment.go`) whose `app_spec.serviceSpec.deployment.env` holds the values, and upserts it into `placements.desired_spec` in the deploy transaction.
2. `deployments.spec` is written without env. Env lives only in the placement.
3. The Sync stream sends the payload as `Apply.application` (raw JSON bytes) to the agent.
4. The agent server-side applies it as an `Application` whose `spec.serviceSpec.deployment.env` contains the plaintext values.
5. The controller copies them into a Secret named `<app>-env` in the app's namespace (`ensureEnvSecret`) and wires it into the container with `envFrom`. It also grants the app's ServiceAccount read access to that Secret.

So plaintext env exists in Postgres, in the `Application` object in the worker's etcd, and in the tenant Secret. There is no API that reads env values back: env is write-only to users today, and this design keeps it that way.

### Platform secrets

| Secret | Read by | Held today |
|---|---|---|
| GitHub OAuth client secret | API | Railway service variable; also a CI secret for the deploy workflows |
| GitLab token, project, registry URL | API (registry service) and the controller on every worker | Railway variable; worker chart values |
| `DATABASE_URL` (Postgres password) | API, migrations | Railway reference variable; CI secret for the deploy workflows |
| Valkey password | API | Railway reference variable |
| Agent token | Agent | Worker Secret; the API stores only its SHA-256 hash (`clusters.agent_token_hash`) |
| User session and API tokens | API | Only SHA-256 hashes in Postgres (`access_token_hash`, `refresh_token_hash`, `token_hash`) |
| OAuth state markers | API | Valkey, 10-minute TTL |
| Observability proxy token | API and obs-proxy | Chart value / environment variable on both sides |
| ClickHouse credentials | obs-proxy, collectors, Grafana | One Secret per ClickHouse user (`loco_migrator`, `loco_ingest`, `loco_reader`), rendered by the `loco-obs` chart from values; the proxy expands them into `CLICKHOUSE_MIGRATOR_URL` and `CLICKHOUSE_URL` |
| Cloudflare API token | cert-manager (DNS-01) | Chart value rendered into a Secret |
| SOPS age keys | Flux on each cluster (planned) | In-cluster Secret plus an offline admin key, per the Flux TDD |
| CI tokens (Buf, Railway, Renovate, Terraform, DigitalOcean, Cloudflare) | GitHub Actions | Repository secrets |

Two properties are already right and stay as they are. Loco issues opaque tokens and stores only their hashes, so there are no signing keys to protect. OAuth state is short-lived and single-use.

## Goals

- No plaintext user secret in Postgres, in an `Application` object, in a log line or in a trace.
- A stolen Postgres dump or backup reveals no secret without separate access to OpenBao.
- Agents and workers never hold an encryption key, an OpenBao credential or a credential scoped wider than their own cluster.
- Every platform credential has a written owner, a rotation procedure and, where the provider supports it, a short lifetime.
- Already running apps keep running when OpenBao or the control plane is down.

## Non-goals

- Protecting secrets from a compromised, authorized control-plane API. Defence there is least privilege, audit and response, not cryptography.
- Rotating the user's own credentials. Re-encrypting `STRIPE_API_KEY` under a new key does not change it at Stripe.
- Secrets mounted as files, or secret references to external stores chosen by the user. Both can be built on this design later.

## User secrets

### Keys

| Key | What it is | Where it lives |
|---|---|---|
| User secret | `STRIPE_API_KEY=sk_live_…` | Encrypted in Postgres; plaintext in the API's memory while sending, in the tenant Secret and in the running container |
| App data key | 32 random bytes generated by the API, one per resource | Wrapped by Transit and stored in Postgres; plaintext only in the API's memory for the duration of one encrypt or decrypt |
| Master key | Transit key `loco-app-keys` | Inside OpenBao; never leaves it |

The data key is per resource, because env is set per resource today. If env becomes per environment, the key follows it (see open questions).

### Schema

```sql
CREATE TABLE secret_keys (
    resource_id   UUID PRIMARY KEY,
    wrapped_key   TEXT NOT NULL,          -- Transit ciphertext, "vault:v<n>:..."
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    rewrapped_at  TIMESTAMPTZ
);

CREATE TABLE secret_revisions (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    resource_id UUID NOT NULL,
    revision    BIGINT NOT NULL,
    ciphertext  BYTEA NOT NULL,           -- AES-256-GCM over the JSON env map
    nonce       BYTEA NOT NULL,           -- 12 random bytes, unique per row
    created_by  UUID NOT NULL REFERENCES users (id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (resource_id, revision)
);
```

Rows in `secret_revisions` are never updated. `placements` gains `secret_revision_id UUID REFERENCES secret_revisions (id)`, and `desired_spec` no longer contains env. `deployments` gains the same column, so history records which revision of env each release ran with, without storing env itself.

Neither table cascades from `resources`: a placement that is being deleted still references its revision until the agent confirms the delete. Revisions are pruned when no placement or retained deployment references them.

### Additional authenticated data

Every encryption binds the ciphertext to where it belongs:

```
loco/v1|org=<org_id>|workspace=<workspace_id>|resource=<resource_id>|revision=<revision>
```

A ciphertext copied into another resource's row, or replayed as a different revision, fails authentication on decrypt. The Transit wrap of the data key passes `loco/v1|resource=<resource_id>` as `associated_data`, so a wrapped key cannot be moved to another resource either.

### Writing env

`UpdateResourceEnv`, and deploys that set env:

1. Authorize the caller, as today.
2. Load the resource's `secret_keys` row. If none exists, generate a data key and wrap it with `transit/encrypt/loco-app-keys`. If one exists, unwrap it with `transit/decrypt/loco-app-keys`.
3. Encrypt the env map with AES-256-GCM, a fresh random nonce and the AAD above, using the next revision number.
4. Zero the plaintext data key.
5. Open the Postgres transaction: insert the `secret_keys` row if new, insert the `secret_revisions` row, upsert the placements to reference it (bumping their desired revision), write the deployment rows, `NOTIFY`.

Every OpenBao call happens before the transaction opens. If OpenBao is unavailable the request fails with `Unavailable` and nothing is committed. A concurrent first write for the same resource is resolved by the `secret_keys` primary key: the loser retries step 2 with the winner's key.

Scale and redeploys that do not change env reuse the placement's current `secret_revision_id`. This replaces `desiredEnv`, which today reads env back out of `desired_spec`.

### Delivering env

Env travels beside the `Application`, not inside it:

```proto
message Apply {
  string placement_id = 1;
  int64 revision = 2;
  string resource_id = 3;
  bytes application = 4;      // no env
  SecretData secret = 5;      // absent when the app has no env
}

message SecretData {
  string revision_id = 1;
  map<string, bytes> data = 2;
}
```

Keeping env out of `application` means the `Application` object, its server-side-apply history and anything that logs or diffs it never contain plaintext.

When the Sync handler sends a pending placement that references a secret revision, it unwraps the data key, decrypts the revision, builds `SecretData`, sends it, and drops the plaintext. On a full inventory resync it batches the unwraps with Transit's `batch_input`.

Unwrapped data keys are not cached. A cache would hold plaintext key material in memory for its lifetime and would have to be invalidated on every rewrap, rotation and revocation; a miss in that logic is a silent window where a revoked key keeps working. The cost of not caching is one Transit call per delivered placement, which is small at Loco's deploy rate and bounded by batching on resync. If profiling shows otherwise, the cache is a separate decision with its own invalidation design.

The stream is already authenticated per cluster, and a placement is only sent to the cluster it belongs to. TLS on the agent's connection becomes mandatory outside local development: the agent refuses a non-`https` `CONTROL_PLANE_URL` unless it runs with an explicit local-development flag.

### On the worker

The agent:

1. Applies a Secret `env-<placement id>-<revision>` in its own namespace with the `SecretData`, labelled with the placement ID and owned by the `Application` (owner reference), so it is collected when the `Application` is.
2. Applies the `Application` with `spec.envSecretRef.name` set to that Secret.
3. After the `Applied` ack for a revision, deletes that placement's staging Secrets for older revisions.

The controller copies the referenced Secret's data into the tenant Secret `<app>-env` in the app's namespace, as `ensureEnvSecret` does today, and stamps the secret revision on the pod template so a change rolls the pods. `ApplicationSpec.Env` is removed from the CRD.

The agent writes Secrets only in its own namespace, and the controller remains the only component that writes into tenant namespaces. The Role that grants the app's ServiceAccount read access to `<app>-env` is removed: `envFrom` is resolved by the kubelet and needs no access from the pod's identity.

Worker clusters must encrypt Secrets at rest: a KMS provider where the platform offers one, otherwise an `EncryptionConfiguration` with a static key managed like other cluster secrets. Managed offerings that encrypt etcd by default satisfy this. Read access to Secrets in tenant namespaces is limited to the controller.

### Logs and traces

- The Sync handler and the agent never log `Apply` messages or `SecretData`, only placement IDs and revisions.
- A `slog` `ReplaceAttr` in the API, agent and controller drops any attribute named `env`, `secret`, `data`, `token`, `password` or carrying a `SecretData` or `Secret` value.
- Connect interceptors and OpenTelemetry spans record procedure names and status codes, never request or response bodies.
- OpenBao's audit log hashes request and response fields by default (HMAC); that stays on.

### Rotation

| What | How | What changes in Postgres |
|---|---|---|
| Master key | `transit/keys/loco-app-keys/rotate`, then `transit/rewrap` every `secret_keys.wrapped_key` | `wrapped_key`, `rewrapped_at`; env ciphertext unchanged |
| App data key | New data key; re-encrypt the current revision as a new revision | New `secret_keys` row and revision; old revisions pruned once unreferenced |
| User secret | The user rotates it with the provider and saves the new value | A new revision |

`min_decryption_version` on `loco-app-keys` is never raised past the oldest key version referenced by a retained Postgres backup. Restoring a backup whose data keys were wrapped under a version OpenBao has since refused would make every secret in it unreadable.

## Platform secrets

### Target state

| Secret | Target |
|---|---|
| GitHub OAuth client secret | OpenBao KV `platform/api/github-oauth`, read by the API at startup |
| GitLab token | KV, read by the API only. Pull credentials for each app's image are minted by the control plane and delivered over Sync like env, so workers hold only per-app, short-lived pull credentials |
| Postgres credentials | KV with a rotation runbook while the database is on Railway. On the control-plane cluster: OpenBao's database secrets engine issues per-instance credentials with a lease, and the pool fetches fresh ones through pgx's `BeforeConnect` |
| Valkey password | KV |
| Agent token | Stays a per-cluster token whose hash is in Postgres and whose plaintext is SOPS-encrypted per cluster. Rotation adds a second valid hash for an overlap window |
| Observability proxy token | KV on the control-plane side; per-cluster SOPS on workers; rotated with an overlap window like agent tokens |
| ClickHouse credentials | KV; database engine later, as for Postgres |
| Cloudflare API token | Per-cluster SOPS, scoped to the zones that cluster issues certificates for |
| SOPS age keys | Unchanged; they sit below OpenBao in the bootstrap order |
| CI tokens | Deploy credentials disappear with the Flux migration. Remaining publish tokens are fetched per job through GitHub's OIDC token and OpenBao's JWT auth, with a role per workflow |

User sessions, API tokens and OAuth state need no change.

### How services authenticate to OpenBao

| Where | Method | Long-lived material |
|---|---|---|
| API on Railway (today) | AppRole. `role_id` is configuration; `secret_id` is a Railway variable with a TTL, re-issued by a scheduled CI job before it expires | One `secret_id` |
| API on the control-plane cluster | Kubernetes auth, bound to the API's ServiceAccount and namespace | None |
| CI | JWT auth against GitHub's OIDC issuer, bound to repository, workflow and ref | None |
| Agents, controller, workers | No OpenBao access | None |

Policies are per caller and per path. The API's policy allows `encrypt`, `decrypt` and `rewrap` on `transit/*/loco-app-keys` and `read` on `kv/data/platform/api/*`; it cannot read the key, export it, change its configuration or rotate it. Rotation runs under a separate operator policy.

## Running OpenBao

**Placement.** OpenBao runs beside the control plane and never on a worker. While the control plane is on Railway, it is a Railway service with integrated Raft storage on a volume, reachable only on the private network. When the control-plane cluster exists, it moves there as a three-node Raft cluster.

**Unsealing.** OpenBao starts sealed. Manual Shamir unsealing after every restart does not fit a platform that restarts services on deploy, so auto-unseal is required, through a cloud KMS or a second, minimal OpenBao's Transit engine. Which one is an open question, because the current providers do not include a KMS. Until it is decided, Shamir keys split among the admins and a documented unseal runbook are the fallback, with the understanding that an OpenBao restart needs a human.

**Configuration as code.** Mounts, Transit keys, policies, auth roles and audit devices are declared in the repository and applied with OpenTofu's Vault provider or a `bao` script under `mise-tasks/`, so a rebuilt instance converges to the same configuration.

**Audit.** A file audit device writes to stdout and joins the log pipeline. An unavailable audit device blocks requests by OpenBao's design; that is accepted.

**Backups.** Scheduled Raft snapshots, encrypted, to object storage outside the provider that runs OpenBao. A restore drill, including unsealing, runs at least quarterly against a scratch instance and is recorded. Postgres backups and OpenBao snapshots are retained on matching schedules, since each is useless without the other.

## Bootstrap order

1. **Outside OpenBao:** per-cluster age keys, the offline admin age key, and the unseal or recovery shares. These are generated by admins, stored offline, and never placed in OpenBao.
2. **Initialize OpenBao.** The root token configures mounts, policies and auth, then is revoked. A new root token is generated from recovery shares only for break-glass work.
3. **Issue the API's credentials** (AppRole on Railway, Kubernetes role later) and load platform secrets into KV.
4. **Start the API.** It reads platform secrets at startup and does not become healthy without them. Railway's health check keeps the previous deployment serving while a new one cannot start, so an OpenBao outage blocks deploys of the API, not the running API.

## Migration

The database has no users, so each step can change the schema in place.

1. **OpenBao up.** Service, unseal procedure, configuration as code, audit, snapshots, the `loco-app-keys` key, the API's role and policy. No behaviour change.
2. **User secrets.** `secret_keys`, `secret_revisions`, the `placements` and `deployments` references, encryption on write, decryption on send, `Apply.secret`, the agent's staging Secret, the controller copying from `envSecretRef`, removal of `ApplicationSpec.Env` and of env from `desired_spec`. One stack, because the proto, agent and controller changes must ship together.
3. **Platform secrets into KV**, read at API startup. Railway variables shrink to OpenBao's address and the AppRole `secret_id`.
4. **Registry credentials off the workers.** The control plane mints per-app pull credentials and delivers them over Sync; the GitLab token is removed from the controller and the worker charts.
5. **CI through OIDC.** Remove the remaining long-lived CI tokens.
6. **Control-plane cluster.** Kubernetes auth for the API, three-node Raft, dynamic database credentials.

## Failure modes

| Failure | Effect |
|---|---|
| OpenBao unavailable | Env updates fail before committing. Placements that need a secret stay pending and are sent once OpenBao is back. Running apps are unaffected; they read the tenant Secret. The API keeps running but cannot restart. |
| OpenBao sealed after a restart | Same as unavailable until unsealed. |
| Postgres dump or backup stolen | Ciphertext and wrapped keys only. |
| Ciphertext moved between rows | AAD check fails; the placement reports an apply error. |
| Postgres restored to an older backup | Its wrapped keys still decrypt, as long as `min_decryption_version` was not raised past them. |
| A worker cluster compromised | That cluster's tenant Secrets, which it needs to run its apps. No keys, no other cluster's secrets. |
| The API compromised | Every secret the Transit policy lets it decrypt. Visible in OpenBao's audit log; contained by revoking the API's role. |

## Alternatives considered

- **pgcrypto in Postgres.** The key has to reach the database session, so a database compromise with the key in a parameter or log reveals everything. It also keeps encryption out of the application, where AAD binding lives.
- **A cloud KMS directly.** Simpler to run, but it ties secrets to one provider, and the current infrastructure providers do not offer one. A KMS remains the preferred way to auto-unseal OpenBao.
- **SOPS for everything.** SOPS suits secrets that live in Git and change by pull request, which is why the Flux TDD uses it for cluster configuration. User secrets change through the API at runtime and cannot go through Git.
- **HashiCorp Vault.** Same API and design. OpenBao is the MPL-licensed fork maintained under the Linux Foundation; Vault's BSL licence is a concern for a platform that may be self-hosted by others.
- **External Secrets Operator.** It syncs secrets from a store into a cluster, which requires each worker to hold store credentials. That contradicts the goal that workers hold no OpenBao access. It remains an option for platform secrets on the control-plane cluster.
- **Caching unwrapped data keys in the API.** Rejected above; revisit only with profiling data and an invalidation design.

## Open questions

- **Auto-unseal provider.** A cloud KMS from an additional provider, or a second minimal OpenBao used only for Transit unsealing.
- **Key scope.** Per resource, as proposed, or per resource and environment if env becomes environment-specific.
- **Secret revision retention.** How many unreferenced revisions to keep for rollback, and for how long.
- **Registry credentials.** Whether per-app pull credentials keep coming from GitLab deploy tokens, or the registry moves to one that issues short-lived tokens natively.
- **Showing env to users.** Env is write-only today. If the UI ever shows values, the read path decrypts on demand and is audited per read; masked display is the default either way.
