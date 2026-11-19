---
title: "Secrets key provider"
---

The API wraps the data key that encrypts each environment's secrets, and the data key that encrypts each webhook's signing secrets, with a key in the Transit secrets engine of OpenBao or Vault. It stores secret values, webhook signing secrets and wrapped data keys in PostgreSQL, so a copy of the database reveals nothing without the Transit key.

The API refuses to start without a valid transit configuration, and stops at startup unless it can log in to the server and read both of its keys with the types below.

## Application secrets

`loco env` and the dashboard store a service's secrets through `SecretService`. A secret belongs to one service in one environment. `ListSecrets` returns each name with its version and who set it; no API returns a stored value.

### Storage

Each environment has one 32-byte data key. The first `SetSecrets` call in an environment generates it, wraps it with the Transit key using `loco/dek/v1|environment=<environment id>` as the derivation context, and stores the wrapped key in the `environment_keys` table. The API encrypts each value with AES-256-GCM under that data key and a fresh 12-byte nonce, and stores the nonce and ciphertext in the `secrets` table, one row per service, environment and name. The additional authenticated data names the workspace, environment, service, secret name and version, so a ciphertext copied into another row fails to decrypt. Setting a name to its stored value changes nothing; any other value gets the next version. `LOCO_SECRETS_MAX_VALUE_BYTES` and `LOCO_SECRETS_MAX_PLACEMENT_BYTES` in the [API environment](../reference/api-environment.md) limit the size of one value and of the values one placement delivers.

### Delivery

A deployment names the secrets the service reads: `secrets` in [`loco.yaml`](../deployment/loco-yaml.md#variables-and-secrets), or `secret_bindings` in `CreateDeployment`. Each placement of that deployment records those names. When the API sends a placement to its cluster's agent over the Sync stream, it reads the ciphertexts in the same read-only transaction as the placement, unwraps the environment's data key, decrypts the values and sends them in `Apply.env_secret` beside the `Application`, tagged with the placement revision. The `Application` holds no secret value. A declared name with no stored value, or a value that fails to decrypt, records an apply error on the placement and nothing is sent.

The agent writes the values to the Secret `env-<placement id>` in its own namespace, labelled with the placement ID and revision and owned by the `Application`, and sets the `Application`'s `envSecretRef` to that Secret and revision. The controller copies the Secret's data into `resource-<resource id>-env` in the environment's namespace, adds it to the container with `envFrom`, and writes the revision to the pod template annotation `loco.io/env-secret-revision`. Until the staging Secret carries the referenced revision, the controller requeues every 5 seconds and the agent's inventory reports the placement as pending, so the API sends it again. Every `agent.inventoryInterval` (10 minutes by default) the agent deletes staging Secrets that no `Application` references. A placement that declares no secrets has neither Secret.

Plaintext values exist in the API's memory while it sends a placement, in the two worker Secrets and in the running container. A copy of the database holds only ciphertext and wrapped keys.

### When values reach pods

Saving or deleting a secret starts no rollout. The API decrypts values each time it sends a placement, so the next deploy or `loco resource restart` delivers the current values. While a deployment of the service is building or rolling out in the environment, the API rejects secret changes with `failed_precondition`. `DeleteSecrets` rejects a name the service's active deployment declares.

## OpenBao in the control-plane chart

`loco-control-plane` runs OpenBao as the StatefulSet `<release>-openbao`, `openbao.replicas` nodes (3 by default) in one raft cluster with a PersistentVolumeClaim each, and points the API at it. OpenBao serves TLS with a certificate cert-manager issues from a CA the chart creates in the release namespace (the Issuers `<release>-openbao-selfsigned` and `<release>-openbao-ca`); the API and the chart's Jobs verify it against that CA, and OpenBao reloads the certificate when cert-manager renews it. The cert-manager controller is required whatever `certManager.enabled` says. A NetworkPolicy admits only the API, the setup Job and the other OpenBao nodes. `openbao.enabled: false` uses an external OpenBao or Vault at `openbao.external.address` instead, with the CA bundle in the Secret `openbao.external.caSecret`.

`openbao.seal.type` selects the seal. `static`, the default, reads a 32-byte key from the Secret `openbao.seal.static.existingSecret`, which you create before the first install; the install fails while it is missing. Keep a copy offline: OpenBao's data and its snapshots are unreadable without it.

```sh
kubectl -n loco-system create secret generic openbao-seal --from-literal=key="$(openssl rand -base64 32)"
```

`awskms` seals with the KMS key `openbao.seal.awskms.kmsKeyId` in `openbao.seal.awskms.region`, with AWS credentials from the pod identity or the Secret `openbao.seal.awskms.existingSecret`. `transit` seals with the key `openbao.seal.transit.key` of another OpenBao at `openbao.seal.transit.address`, with the token in the Secret `openbao.seal.transit.existingSecret`.

Every install and upgrade creates the Job `<release>-openbao-setup-<revision>`, which configures OpenBao. Kubernetes deletes the finished Job after `openbao.setup.ttlSecondsAfterFinished` seconds (3600 by default). Each run first creates the Secret `<release>-openbao-init`, empty, unless it exists, and patches it with no change; the Job's Role allows creating Secrets and patching only that one, and a run that cannot do both fails before it touches OpenBao. The first run then initializes OpenBao with `openbao.recovery.shares` recovery keys, `openbao.recovery.threshold` of them needed, and patches the recovery keys, one per line, and a token that can only run root token generation into that Secret (keys `recovery-keys` and `generate-root-token`). The pod keeps the init output in memory and retries the patch every 5 seconds for up to an hour, logging each failure. Move both keys offline beside the seal key, then delete the Secret; the next run creates it again, empty. With the root token from the initialization, the first run creates the Kubernetes auth role `openbao.kubernetesAuth.setupRole` for the Job's ServiceAccount, with the `loco-setup` policy, which covers only the Transit mount and keys, the `loco-api` and `loco-setup` policies and the roles of the Kubernetes auth mount. It then revokes the root token; later runs log in with that role. On every run the Job enables Transit at `openbao.transit.mount`, creates the key `openbao.transit.key` and the `ed25519` keys `openbao.transit.ingestKey` and `openbao.transit.registryKey`, writes the `loco-api` policy below, and binds the role `openbao.kubernetesAuth.role` at `openbao.kubernetesAuth.mount` to the API's ServiceAccount `<release>-api`. It fails instead of initializing when OpenBao is not initialized and the `<release>-openbao-init` Secret holds recovery keys. The Job waits for OpenBao to answer and to unseal until it has run for `openbao.setup.timeoutSeconds` (30 minutes by default), then fails. It runs alongside the API Deployment, so on a first install the API pods exit and restart until the Job has created the keys, each time logging which key is missing or why the login failed. The API logs in to its role with a projected ServiceAccount token. For an external server, create the same keys, policy and role, bound to `<release>-api` in the release namespace; when the server offers no Kubernetes auth, name a Secret holding a token in `openbao.external.tokenSecret` instead.

For anything beyond that, generate a root token inside `<release>-openbao-0` with the generate-root token in `BAO_TOKEN` and the recovery keys, and revoke it when done:

```sh
bao operator generate-root -generate-otp
bao operator generate-root -init -otp=OTP
bao operator generate-root -nonce=NONCE RECOVERY_KEY
bao operator generate-root -decode=ENCODED_TOKEN -otp=OTP
```

Run the third command once per recovery key, up to the threshold.

### Lost init output

When OpenBao is initialized, `<release>-openbao-init` holds no recovery keys and the setup role cannot log in, the Job fails with `OpenBao at ... is initialized, but the Secret ... holds no init output`. With the recovery keys and generate-root token offline, generate a root token as above and recreate the role `openbao.kubernetesAuth.setupRole` with the `loco-setup` policy, bound to the ServiceAccount `<release>-openbao-setup`. Without them, and before the API has stored a secret, delete the StatefulSet `<release>-openbao`, its PersistentVolumeClaims and the `<release>-openbao-init` Secret, and run `helm upgrade` again: the setup Job initializes a new OpenBao. Once the API has stored secrets, restore the latest snapshot as below instead; it brings back the Kubernetes auth roles it was taken with.

### Snapshots and restore

The CronJob `<release>-openbao-snapshot` takes a raft snapshot on `openbao.snapshots.schedule` (hourly) and uploads it to `openbao.snapshots.s3.bucket` under `openbao.snapshots.s3.prefix` at the S3-compatible `openbao.snapshots.s3.endpoint`, with the access key in the Secret `openbao.snapshots.s3.existingSecret`. It deletes snapshots older than `openbao.snapshots.retention` (`30d`). It logs in with its own Kubernetes auth role, `openbao.snapshots.role`, whose policy allows only reading `sys/storage/raft/snapshot`. The install fails without the bucket, the endpoint and the Secret; `openbao.snapshots.enabled: false` turns snapshots off.

A snapshot restores only into an OpenBao with the same seal key, and after the restore its recovery keys and Kubernetes auth roles are those of the snapshot. To restore one:

1. Delete the StatefulSet `<release>-openbao`, its PersistentVolumeClaims and the `<release>-openbao-init` Secret if it still exists.
2. Run `helm upgrade` with the same values. The setup Job initializes a new OpenBao and writes its recovery keys and generate-root token to a new `<release>-openbao-init` Secret.
3. Generate a root token with that Secret's generate-root token and recovery keys as above, copy the snapshot into `<release>-openbao-0` and restore it: `BAO_TOKEN=ROOT_TOKEN bao operator raft snapshot restore -force /tmp/openbao.snap`. The restore discards the generated token.
4. Delete the new `<release>-openbao-init` Secret; the recovery keys and generate-root token from your offline copy apply again. Run `helm upgrade` again: the setup Job logs in with its restored role.

The API logs in again once the restored OpenBao rejects its token, without a restart.

`mise run e2e controlplane TestControlPlane` runs these steps and checks that the API still decrypts a secret written before the snapshot.

## Transit

The API calls three Transit endpoints on one key: `encrypt`, `decrypt` and a read of the key for its latest version. Each call sends the environment's identity as the key derivation context, so the key must be an `aes256-gcm96` key created with `derived=true`. A data key wrapped for one environment does not unwrap with another environment's context. The API refuses any other key type.

### Create the key and the policy

The commands use the OpenBao CLI; the Vault CLI takes the same arguments with `vault` in place of `bao`.

```sh
bao secrets enable transit
bao write -f transit/keys/loco type=aes256-gcm96 derived=true
bao write -f transit/keys/loco-ingest type=ed25519
bao write -f transit/keys/loco-registry type=ed25519
bao policy write loco-api - <<'EOF'
path "transit/encrypt/loco" {
  capabilities = ["update"]
}
path "transit/decrypt/loco" {
  capabilities = ["update"]
}
path "transit/keys/loco" {
  capabilities = ["read"]
}
path "transit/sign/loco-ingest" {
  capabilities = ["update"]
}
path "transit/keys/loco-ingest" {
  capabilities = ["read"]
}
path "transit/sign/loco-registry" {
  capabilities = ["update"]
}
path "transit/keys/loco-registry" {
  capabilities = ["read"]
}
EOF
bao token create -policy=loco-api -period=24h
```

`loco-ingest` signs telemetry ingest tokens and `loco-registry` signs registry access tokens; neither leaves Transit. The policy grants nothing beyond the three keys. The API authenticates with Kubernetes auth or a token. For Kubernetes auth, bind a role to the API's ServiceAccount:

```sh
bao auth enable kubernetes
bao write auth/kubernetes/config kubernetes_host=https://kubernetes.default.svc
bao write auth/kubernetes/role/loco-api bound_service_account_names=loco-api \
  bound_service_account_namespaces=loco-system token_policies=loco-api token_ttl=1h
```

A token from `bao token create` is renewed through `auth/token/lookup-self` and `auth/token/renew-self`, which the built-in `default` policy allows, so do not create it with `-no-default-policy`. A periodic token like the one above can be renewed indefinitely; a token with a maximum TTL stops working at that maximum.

### Configure the API

| Variable | Default | Meaning |
| --- | --- | --- |
| `LOCO_SECRETS_TRANSIT_ADDR` | required | Base URL of the server, such as `https://bao.example.internal:8200`, with no path |
| `LOCO_SECRETS_TRANSIT_MOUNT` | `transit` | Mount path of the Transit secrets engine |
| `LOCO_SECRETS_TRANSIT_KEY` | required | Name of the key |
| `LOCO_INGEST_TRANSIT_KEY` | required | Name of the `ed25519` key that signs ingest tokens, on the same mount |
| `LOCO_REGISTRY_AUTH` | `static` | `static` or `token`; `token` makes the API sign registry access tokens and serve the public key at `/.well-known/loco-registry-key.pem` |
| `LOCO_REGISTRY_TRANSIT_KEY` | required for `token` | Name of the `ed25519` key that signs registry access tokens, on the same mount; it must differ from `LOCO_SECRETS_TRANSIT_KEY` and `LOCO_INGEST_TRANSIT_KEY`, because the registry does not check a token's audience or issuer |
| `LOCO_REGISTRY_TRANSIT_KEY_VERSION` | required for `token` | Version of `LOCO_REGISTRY_TRANSIT_KEY` the API signs with, 1 or more |
| `LOCO_SECRETS_TRANSIT_AUTH` | required | `kubernetes` or `token` |
| `LOCO_SECRETS_TRANSIT_KUBERNETES_MOUNT` | required for `kubernetes` | Mount path of the Kubernetes auth method |
| `LOCO_SECRETS_TRANSIT_KUBERNETES_ROLE` | required for `kubernetes` | Role bound to the API's ServiceAccount |
| `LOCO_SECRETS_TRANSIT_KUBERNETES_TOKEN_FILE` | required for `kubernetes` | Projected ServiceAccount token the API logs in with |
| `LOCO_SECRETS_TRANSIT_TOKEN` | required for `token` | Token holding the policy above |
| `LOCO_SECRETS_TRANSIT_CA_FILE` | system roots | PEM bundle that signs the server's TLS certificate |
| `LOCO_SECRETS_TRANSIT_TIMEOUT` | `10s` | Limit on each request to the server |
| `LOCO_SECRETS_TRANSIT_STARTUP_TIMEOUT` | `30s` | Limit on the first login and the key checks at startup |
| `LOCO_SECRETS_TRANSIT_RENEW_MARGIN` | `5m` | How long before the token expires the API renews it or logs in again |
| `LOCO_SECRETS_TRANSIT_RENEW_RETRY` | `10s` | Wait after a failed token lookup, renewal or login before the next attempt, and between login attempts at startup while the server is unreachable or answers with a server error |
| `LOCO_SECRETS_TRANSIT_CACHE_TTL` | `5m` | How long the API keeps an unwrapped data key in memory |

The API verifies the server's TLS certificate and has no setting to skip it. An invalid value stops the API at startup.

### Startup

At startup the API looks its token up, with token auth, or logs in, with Kubernetes auth. While the server is unreachable or answers with a server error, a sealed OpenBao for example, or, with Kubernetes auth, refuses the login, as it does before the role exists, it logs `transit is unavailable; logging in again` and tries again every `LOCO_SECRETS_TRANSIT_RENEW_RETRY`. It then reads `LOCO_SECRETS_TRANSIT_KEY`, which must be an `aes256-gcm96` key created with `derived=true`, and `LOCO_INGEST_TRANSIT_KEY`, which must be an `ed25519` key with a public key. With `LOCO_REGISTRY_AUTH=token` it also reads `LOCO_REGISTRY_TRANSIT_KEY`, which must be an `ed25519` key on which version `LOCO_REGISTRY_TRANSIT_KEY_VERSION` can sign: at least the key's `min_decryption_version` and `min_encryption_version` and at most its `latest_version`. In token mode the API needs no registry credentials, and setting `LOCO_REGISTRY_USERNAME` or `LOCO_REGISTRY_PASSWORD`, or leaving `LOCO_REGISTRY_HOST` empty, stops the API at startup. A rejected token, a missing key, a key of another type, a login still refused or no answer within `LOCO_SECRETS_TRANSIT_STARTUP_TIMEOUT` stops the API with the error `failed to verify the transit keys`, naming the server or the key and the reason.

### Token renewal

With Kubernetes auth the API logs in again `LOCO_SECRETS_TRANSIT_RENEW_MARGIN` before each token expires, or at half its TTL when the TTL is shorter than the margin, reading the ServiceAccount token file each time. When the server rejects its token, after a snapshot restore for example, it logs `transit rejected the token; logging in again` and logs in again after `LOCO_SECRETS_TRANSIT_RENEW_RETRY`, doubling the wait for each further rejection up to `LOCO_SECRETS_TRANSIT_RENEW_MARGIN`. A failed login is logged as `failed to refresh the transit token` and retried every `LOCO_SECRETS_TRANSIT_RENEW_RETRY`.

With token auth the API renews its token `LOCO_SECRETS_TRANSIT_RENEW_MARGIN` before each expiry, or at half its TTL when the TTL is shorter than the margin. A failed renewal is logged as `failed to refresh the transit token` and retried every `LOCO_SECRETS_TRANSIT_RENEW_RETRY` until it succeeds. A token without a TTL is not renewed. Once the token has expired, every secret operation fails with `transit token has expired` until the API restarts with a new token.

### Outages

The API keeps each unwrapped data key in memory for `LOCO_SECRETS_TRANSIT_CACHE_TTL`, so delivering secrets to clusters keeps working through a Transit outage shorter than that. Setting the first secret of an environment, or any secret in an environment whose data key is not cached, fails with `Unavailable` until the server is back. A shorter TTL bounds how long a data key stays in API memory; a longer one rides out longer outages.

Each webhook delivery opens the webhook's signing secrets with its data key, through the same cache. While Transit is unreachable and that key is not cached, the delivery stays pending, uses none of its attempts and is retried after the first `WEBHOOK_RETRY_DELAYS` delay. Creating a webhook fails with `Unavailable`, as does rotating a secret whose data key is not cached, and the API stops at startup when it cannot seal the `INSTALL_WEBHOOKS` secrets.

### Rotation

`bao write -f transit/keys/loco/rotate` creates a new key version. New data keys are wrapped with it, and data keys wrapped with older versions still unwrap. Call `SecretService.RewrapEnvironmentKeys` to rewrap every environment's data key with the new version. With an environment ID and `new_dek` set, it replaces that environment's data key and re-encrypts every value under the new one, keeping each version. A webhook's data key stays wrapped with the version that was current when the webhook was created; each API start wraps the install webhooks' data keys with the latest version. Raise the key's `min_decryption_version` only past versions that no environment key, no webhook key and no retained database backup still uses.

`bao write -f transit/keys/loco-ingest/rotate` rotates the ingest signing key. The API signs new tokens with the latest version and publishes every version from the key's `min_decryption_version` in its JWKS, so tokens signed with older versions keep verifying until the minimum is raised past them. Raise it once the token lifetime has passed since the rotation.

The registry trusts one public key, which it loads from `/.well-known/loco-registry-key.pem` at startup, so the API signs registry access tokens with the pinned `LOCO_REGISTRY_TRANSIT_KEY_VERSION` and rotating `loco-registry` is a coordinated swap:

1. `bao write -f transit/keys/loco-registry/rotate`. The new version exists, and nothing signs with it yet.
2. Deploy the API with `LOCO_REGISTRY_TRANSIT_KEY_VERSION` set to the new version.
3. Restart the registry so it loads the new key. Until both the API and the registry have restarted, tokens signed on one side of the swap fail to verify on the other. A running build fetches a new token when the registry rejects one and gives up after about 15 seconds, so a build that pushes during a longer gap fails.
4. Deploy from source and restart a pod to check that builds push and pods pull.

### Supported versions

CI runs the provider against the OpenBao image the control-plane chart pins in `openbao.image` and the Vault dev server image pinned in `tests/conformance/transit/compose.yaml`. Run the same suite locally with `mise run test:transit-conformance`. Other versions are untested.
