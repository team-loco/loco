# Secrets key provider

The key provider wraps the data key that encrypts each environment's secrets. The API stores secret values and wrapped data keys in PostgreSQL, so a copy of the database reveals nothing without the provider.

`LOCO_SECRETS_KEY_PROVIDER` selects one of two providers:

- `local` reads the key-encryption keys from `LOCO_SECRETS_LOCAL_KEYS` in the API's own configuration.
- `transit` wraps data keys with a key in the Transit secrets engine of OpenBao or Vault. Use it when your installation already runs one and you want the key-encryption key, its access policy and its audit log outside the API.

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
| `LOCO_SECRETS_KEY_PROVIDER` | none | `transit` |
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

CI runs the provider against the OpenBao and Vault dev server images pinned in `env/transit-conformance/compose.yaml`: OpenBao 2.7 and Vault 2.1. Run the same suite locally with `mise run test:transit-conformance`. Other versions are untested.
