# Secrets key provider

The API wraps the data key that encrypts each environment's secrets with a key in the Transit secrets engine of OpenBao or Vault. It stores secret values and wrapped data keys in PostgreSQL, so a copy of the database reveals nothing without the Transit key.

The API refuses to start without a valid transit configuration, and stops at startup unless it can log in to the server and read both of its keys with the types below.

## OpenBao in the control-plane chart

`loco-control-plane` runs OpenBao as the StatefulSet `<release>-openbao`, `openbao.replicas` nodes (3 by default) in one raft cluster with a PersistentVolumeClaim each, and points the API at it. OpenBao serves TLS with a certificate cert-manager issues from a CA the chart creates in the release namespace (the Issuers `<release>-openbao-selfsigned` and `<release>-openbao-ca`); the API and the chart's Jobs verify it against that CA, and OpenBao reloads the certificate when cert-manager renews it. The cert-manager controller is required whatever `certManager.enabled` says. A NetworkPolicy admits only the API, the setup Job and the other OpenBao nodes. `openbao.enabled: false` uses an external OpenBao or Vault at `openbao.external.address` instead, with the CA bundle in the Secret `openbao.external.caSecret`.

`openbao.seal.type` selects the seal. `static`, the default, reads a 32-byte key from the Secret `openbao.seal.static.existingSecret`, which you create before the first install; the install fails while it is missing. Keep a copy offline: OpenBao's data and its snapshots are unreadable without it.

```sh
kubectl -n loco-system create secret generic openbao-seal --from-literal=key="$(openssl rand -base64 32)"
```

`awskms` seals with the KMS key `openbao.seal.awskms.kmsKeyId` in `openbao.seal.awskms.region`, with AWS credentials from the pod identity or the Secret `openbao.seal.awskms.existingSecret`. `transit` seals with the key `openbao.seal.transit.key` of another OpenBao at `openbao.seal.transit.address`, with the token in the Secret `openbao.seal.transit.existingSecret`.

Every install and upgrade creates the Job `<release>-openbao-setup-<revision>`, which configures OpenBao and stays until the next upgrade replaces it. Each run first creates the Secret `<release>-openbao-init`, empty, unless it exists, and patches it with no change; the Job's Role allows creating Secrets and patching only that one, and a run that cannot do both fails before it touches OpenBao. The first run then initializes OpenBao with `openbao.recovery.shares` recovery keys, `openbao.recovery.threshold` of them needed, and patches the recovery keys, one per line, and a token that can only run root token generation into that Secret (keys `recovery-keys` and `generate-root-token`). The pod keeps the init output in memory and retries the patch every 5 seconds for up to an hour, logging each failure. Move both keys offline beside the seal key, then delete the Secret; the next run creates it again, empty. With the root token from the initialization, the first run creates the Kubernetes auth role `openbao.kubernetesAuth.setupRole` for the Job's ServiceAccount, with the `loco-setup` policy, which covers only the Transit mount and keys, the `loco-api` and `loco-setup` policies and the roles of the Kubernetes auth mount. It then revokes the root token; later runs log in with that role. On every run the Job enables Transit at `openbao.transit.mount`, creates the key `openbao.transit.key` and the `ed25519` key `openbao.transit.ingestKey`, writes the `loco-api` policy below, and binds the role `openbao.kubernetesAuth.role` at `openbao.kubernetesAuth.mount` to the API's ServiceAccount `<release>-api`. It fails instead of initializing when OpenBao is not initialized and the `<release>-openbao-init` Secret holds recovery keys. The Job runs alongside the API Deployment, so on a first install the API pods exit and restart until the Job has created the keys, each time logging which key is missing or why the login failed. The API logs in to its role with a projected ServiceAccount token. For an external server, create the same keys, policy and role, bound to `<release>-api` in the release namespace; when the server offers no Kubernetes auth, name a Secret holding a token in `openbao.external.tokenSecret` instead.

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

`mise run e2e:control-plane` runs these steps and checks that the API still decrypts a secret written before the snapshot.

## Transit

The API calls three Transit endpoints on one key: `encrypt`, `decrypt` and a read of the key for its latest version. Each call sends the environment's identity as the key derivation context, so the key must be an `aes256-gcm96` key created with `derived=true`. A data key wrapped for one environment does not unwrap with another environment's context. The API refuses any other key type.

### Create the key and the policy

The commands use the OpenBao CLI; the Vault CLI takes the same arguments with `vault` in place of `bao`.

```sh
bao secrets enable transit
bao write -f transit/keys/loco type=aes256-gcm96 derived=true
bao write -f transit/keys/loco-ingest type=ed25519
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
EOF
bao token create -policy=loco-api -period=24h
```

`loco-ingest` signs telemetry ingest tokens and never leaves Transit. The policy grants nothing beyond the two keys. The API authenticates with Kubernetes auth or a token. For Kubernetes auth, bind a role to the API's ServiceAccount:

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

At startup the API looks its token up, with token auth, or logs in, with Kubernetes auth. While the server is unreachable or answers with a server error, a sealed OpenBao for example, it logs `transit is unavailable; logging in again` and tries again every `LOCO_SECRETS_TRANSIT_RENEW_RETRY`. It then reads `LOCO_SECRETS_TRANSIT_KEY`, which must be an `aes256-gcm96` key created with `derived=true`, and `LOCO_INGEST_TRANSIT_KEY`, which must be an `ed25519` key with a public key. A rejected token or login, a missing key, a key of another type or no answer within `LOCO_SECRETS_TRANSIT_STARTUP_TIMEOUT` stops the API with the error `failed to verify the transit keys`, naming the server or the key and the reason.

### Token renewal

With Kubernetes auth the API logs in again `LOCO_SECRETS_TRANSIT_RENEW_MARGIN` before each token expires, or at half its TTL when the TTL is shorter than the margin, reading the ServiceAccount token file each time. When the server rejects its token, after a snapshot restore for example, it logs `transit rejected the token; logging in again` and logs in again after `LOCO_SECRETS_TRANSIT_RENEW_RETRY`, doubling the wait for each further rejection up to `LOCO_SECRETS_TRANSIT_RENEW_MARGIN`. A failed login is logged as `failed to refresh the transit token` and retried every `LOCO_SECRETS_TRANSIT_RENEW_RETRY`.

With token auth the API renews its token `LOCO_SECRETS_TRANSIT_RENEW_MARGIN` before each expiry, or at half its TTL when the TTL is shorter than the margin. A failed renewal is logged as `failed to refresh the transit token` and retried every `LOCO_SECRETS_TRANSIT_RENEW_RETRY` until it succeeds. A token without a TTL is not renewed. Once the token has expired, every secret operation fails with `transit token has expired` until the API restarts with a new token.

### Outages

The API keeps each unwrapped data key in memory for `LOCO_SECRETS_TRANSIT_CACHE_TTL`, so delivering secrets to clusters keeps working through a Transit outage shorter than that. Setting the first secret of an environment, or any secret in an environment whose data key is not cached, fails with `Unavailable` until the server is back. A shorter TTL bounds how long a data key stays in API memory; a longer one rides out longer outages.

### Rotation

`bao write -f transit/keys/loco/rotate` creates a new key version. New data keys are wrapped with it, and data keys wrapped with older versions still unwrap. Call `SecretService.RewrapEnvironmentKeys` to rewrap every environment's data key with the new version. Raise the key's `min_decryption_version` only past versions that no environment key and no retained database backup still uses.

`bao write -f transit/keys/loco-ingest/rotate` rotates the ingest signing key. The API signs new tokens with the latest version and publishes every version from the key's `min_decryption_version` in its JWKS, so tokens signed with older versions keep verifying until the minimum is raised past them. Raise it once the token lifetime has passed since the rotation.

### Supported versions

CI runs the provider against the OpenBao and Vault dev server images pinned in `tests/conformance/transit/compose.yaml`: OpenBao 2.7 and Vault 2.1. Run the same suite locally with `mise run test:transit-conformance`. Other versions are untested.
