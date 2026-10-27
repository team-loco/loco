# Secrets key provider

The API wraps the data key that encrypts each environment's secrets with a key in the Transit secrets engine of OpenBao or Vault. It stores secret values and wrapped data keys in PostgreSQL, so a copy of the database reveals nothing without the Transit key.

The API refuses to start without a valid transit configuration.

## OpenBao in the control-plane chart

`loco-control-plane` runs OpenBao as the StatefulSet `<release>-openbao`, `openbao.replicas` nodes (3 by default) in one raft cluster with a PersistentVolumeClaim each, and points the API at it. OpenBao serves TLS with a certificate cert-manager issues from a CA the chart creates in the release namespace (the Issuers `<release>-openbao-selfsigned` and `<release>-openbao-ca`); the API and the chart's Jobs verify it against that CA, and OpenBao reloads the certificate when cert-manager renews it. The cert-manager controller is required whatever `certManager.enabled` says. A NetworkPolicy admits only the API, the setup Job and the other OpenBao nodes. `openbao.enabled: false` uses an external OpenBao or Vault at `openbao.external.address` instead, with the CA bundle in the Secret `openbao.external.caSecret`.

`openbao.seal.type` selects the seal. `static`, the default, reads a 32-byte key from the Secret `openbao.seal.static.existingSecret`, which you create before the first install; the install fails while it is missing. Keep a copy offline: OpenBao's data and its snapshots are unreadable without it.

```sh
kubectl -n loco-system create secret generic openbao-seal --from-literal=key="$(openssl rand -base64 32)"
```

`awskms` seals with the KMS key `openbao.seal.awskms.kmsKeyId` in `openbao.seal.awskms.region`, with AWS credentials from the pod identity or the Secret `openbao.seal.awskms.existingSecret`. `transit` seals with the key `openbao.seal.transit.key` of another OpenBao at `openbao.seal.transit.address`, with the token in the Secret `openbao.seal.transit.existingSecret`.

After every install and upgrade, the Job `<release>-openbao-setup` configures OpenBao. Each run first creates the Secret `<release>-openbao-init`, empty, unless it exists, and patches it with no change; the Job's Role allows creating Secrets and patching only that one, and a run that cannot do both fails before it touches OpenBao. The first run then initializes OpenBao with `openbao.recovery.shares` recovery keys, `openbao.recovery.threshold` of them needed, and patches the recovery keys, one per line, and a token that can only run root token generation into that Secret (keys `recovery-keys` and `generate-root-token`). The pod keeps the init output in memory and retries the patch every 5 seconds for up to an hour, logging each failure. Move both keys offline beside the seal key, then delete the Secret; the next run creates it again, empty. With the root token from the initialization, the first run creates the Kubernetes auth role `openbao.kubernetesAuth.setupRole` for the Job's ServiceAccount, with the `loco-setup` policy, which covers only the Transit mount and key, the `loco-api` and `loco-setup` policies and the roles of the Kubernetes auth mount. It then revokes the root token; later runs log in with that role. On every run the Job enables Transit at `openbao.transit.mount`, creates the key `openbao.transit.key`, writes the `loco-api` policy below, and binds the role `openbao.kubernetesAuth.role` at `openbao.kubernetesAuth.mount` to the API's ServiceAccount `<release>-api`. It fails instead of initializing when OpenBao is not initialized and the `<release>-openbao-init` Secret holds recovery keys. The first run also creates the token that `LOCO_SECRETS_TRANSIT_TOKEN` in the API environment Secret names.

For anything beyond that, generate a root token inside `<release>-openbao-0` with the generate-root token in `BAO_TOKEN` and the recovery keys, and revoke it when done:

```sh
bao operator generate-root -generate-otp
bao operator generate-root -init -otp=OTP
bao operator generate-root -nonce=NONCE RECOVERY_KEY
bao operator generate-root -decode=ENCODED_TOKEN -otp=OTP
```

Run the third command once per recovery key, up to the threshold.

### Lost init output

When OpenBao is initialized, `<release>-openbao-init` holds no recovery keys and the setup role cannot log in, the Job fails with `OpenBao at ... is initialized, but the Secret ... holds no init output`. With the recovery keys and generate-root token offline, generate a root token as above and recreate the role `openbao.kubernetesAuth.setupRole` with the `loco-setup` policy, bound to the ServiceAccount `<release>-openbao-setup`. Without them, and before the API has stored a secret, delete the StatefulSet `<release>-openbao`, its PersistentVolumeClaims and the `<release>-openbao-init` Secret, and run `helm upgrade` again: the setup Job initializes a new OpenBao.

## Transit

The API calls three Transit endpoints on one key: `encrypt`, `decrypt` and a read of the key for its latest version. Each call sends the environment's identity as the key derivation context, so the key must be an `aes256-gcm96` key created with `derived=true`. A data key wrapped for one environment does not unwrap with another environment's context. The API refuses any other key type.

### Create the key and the policy

The commands use the OpenBao CLI; the Vault CLI takes the same arguments with `vault` in place of `bao`.

```sh
bao secrets enable transit
bao write -f transit/keys/loco type=aes256-gcm96 derived=true
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
EOF
bao token create -policy=loco-api -period=24h
```

The policy grants nothing beyond that key. The API also renews its token through `auth/token/lookup-self` and `auth/token/renew-self`, which the built-in `default` policy allows, so do not create the token with `-no-default-policy`. A periodic token like the one above can be renewed indefinitely; a token with a maximum TTL stops working at that maximum.

### Configure the API

| Variable | Default | Meaning |
| --- | --- | --- |
| `LOCO_SECRETS_TRANSIT_ADDR` | required | Base URL of the server, such as `https://bao.example.internal:8200`, with no path |
| `LOCO_SECRETS_TRANSIT_MOUNT` | `transit` | Mount path of the Transit secrets engine |
| `LOCO_SECRETS_TRANSIT_KEY` | required | Name of the key |
| `LOCO_SECRETS_TRANSIT_TOKEN` | required | Token holding the policy above |
| `LOCO_SECRETS_TRANSIT_CA_FILE` | system roots | PEM bundle that signs the server's TLS certificate |
| `LOCO_SECRETS_TRANSIT_TIMEOUT` | `10s` | Limit on each request to the server |
| `LOCO_SECRETS_TRANSIT_RENEW_MARGIN` | `5m` | How long before the token expires the API renews it |
| `LOCO_SECRETS_TRANSIT_RENEW_RETRY` | `10s` | Wait after a failed token lookup or renewal before the next attempt |
| `LOCO_SECRETS_TRANSIT_CACHE_TTL` | `5m` | How long the API keeps an unwrapped data key in memory |

The API verifies the server's TLS certificate and has no setting to skip it. An invalid value stops the API at startup.

### Token renewal

At startup the API looks its token up and then renews it `LOCO_SECRETS_TRANSIT_RENEW_MARGIN` before each expiry, or at half its TTL when the TTL is shorter than the margin. A failed lookup or renewal is logged as `failed to refresh the transit token` and retried every `LOCO_SECRETS_TRANSIT_RENEW_RETRY` until it succeeds. A token without a TTL is not renewed. Once the token has expired, every secret operation fails with `transit token has expired` until the API restarts with a new token.

### Outages

The API keeps each unwrapped data key in memory for `LOCO_SECRETS_TRANSIT_CACHE_TTL`, so delivering secrets to clusters keeps working through a Transit outage shorter than that. Setting the first secret of an environment, or any secret in an environment whose data key is not cached, fails with `Unavailable` until the server is back. A shorter TTL bounds how long a data key stays in API memory; a longer one rides out longer outages.

### Rotation

`bao write -f transit/keys/loco/rotate` creates a new key version. New data keys are wrapped with it, and data keys wrapped with older versions still unwrap. Call `SecretService.RewrapEnvironmentKeys` to rewrap every environment's data key with the new version. Raise the key's `min_decryption_version` only past versions that no environment key and no retained database backup still uses.

### Supported versions

CI runs the provider against the OpenBao and Vault dev server images pinned in `tests/conformance/transit/compose.yaml`: OpenBao 2.7 and Vault 2.1. Run the same suite locally with `mise run test:transit-conformance`. Other versions are untested.
