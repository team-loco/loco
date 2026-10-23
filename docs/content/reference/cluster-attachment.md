# Cluster attachment

Each cluster's agent holds its own Ed25519 key and authenticates to the control plane with it. An operator creates the cluster record first and gives the agent a one-time join token; the agent's first start binds its public key to that record.

## Create and enroll a cluster

Cluster administration requires the `system:admin` scope. `locoadm`, the operator CLI, shares the credentials and API host `loco login` stores. Install it with `curl -fsSL https://loco.build/install.sh | sh -s -- --binary locoadm`.

```sh
locoadm cluster create --name production-east --region us-east-1 --tier production
```

The tier is `staging` or `production`. The command prints the cluster ID and a join token. The token works once and expires after `LOCO_CLUSTER_JOIN_TOKEN_TTL` (API configuration, default `1h`); the API stores only its SHA-256 hash. Pass it to the agent's first start:

```yaml
agent:
  joinToken:
    value: <join-token>
env:
  CONTROL_PLANE_URL: https://api.example.com
```

`agent.joinToken.existingSecret` and `agent.joinToken.secretKey` name a Secret you create instead of `value`.

On first start the agent generates a key pair and stores the private key in the `<release>-agent-key` Secret in its namespace. It sends the join token, its public key and its cluster key, the UID of Kubernetes' `kube-system` namespace, to the API. The API consumes the token and binds the key and cluster key to the record. A Kubernetes cluster can be enrolled into one record only. New clusters become eligible for deployment after healthy heartbeats arrive.

After enrollment the agent ignores the join token. Every registration and sync stream carries a JWT the agent signs with its key: `iss` and `sub` are the cluster ID, `aud` is `loco-api`, and `exp` is `agent.assertionLifetime` (default `1m`) after `iat`. The API verifies it against the bound public key, rejects assertions that live longer than `LOCO_AGENT_ASSERTION_MAX_LIFETIME` (default `5m`), and records each `jti` in PostgreSQL so every API replica rejects a reused assertion. The agent needs no credential issued by the API, so it reconnects after an outage of any length.

`locoadm cluster list` shows each cluster's `agentKeyFingerprint`, the SHA-256 of its public key; a pending cluster has none.

## Platform domains

`global.domain.apps` in the `loco-core` values lists the platform domains a cluster serves. The chart renders `*.<domain>` HTTP and HTTPS listeners on the `eg` Gateway and a certificate for each, and passes the list to the agent. On registration and every heartbeat, the agent reports the domains whose listeners are accepted and programmed and whose certificate is ready and names `*.<domain>`. With `certManager.existingTLSSecret` set, the chart renders no certificate and the listeners alone decide. The agent reads the Gateway and the Certificate with `get` on those two names only.

## Re-enroll

To replace a lost key or rotate it, issue a new join token, delete the key Secret and restart the agent with the token:

```sh
locoadm cluster join-token <cluster-id>
kubectl -n loco-system delete secret loco-core-agent-key
```

Enrollment replaces the bound key and closes the stream that used the old key. A new token replaces any unused one. Re-enrollment must come from the same Kubernetes cluster; a replacement Kubernetes cluster needs its own record.

## Pause, remove and restore

```sh
locoadm cluster list
locoadm cluster pause <cluster-id>
locoadm cluster remove <cluster-id>
locoadm cluster restore <cluster-id>
locoadm cluster resume <cluster-id>
```

Pausing prevents new deployments and queued builds. Existing workloads and their agent connection remain available for inspection and cleanup. An assignment already committing finishes before pause returns.

Removal requires a paused cluster and refuses while placements, active deployments or queued/running builds remain. Remove applications through the normal resource configuration flow and wait for their deletion acknowledgments before retrying. A refused removal returns the blocking workload IDs as typed error details; the CLI prints them.

For a permanently lost cluster, `locoadm cluster remove <cluster-id> --force` retains those records and prints the workloads whose state cannot be confirmed. Removal closes the sync stream, rejects the agent's registrations and streams, and invalidates an unused join token. It does not uninstall the agent or stop Kubernetes pods.

Restoring clears the removal marker and stale health reports, and leaves the cluster paused. The agent reconnects with its existing key. Resume it explicitly; fresh healthy heartbeats are required before deployment.

The account needs the `system:admin` grant; organization and workspace admins do not receive it. `locoadm bootstrap` grants it to an installation's first admin, as described in [self-hosting](../operations/self-hosting.md#first-system-admin).

## Verification

`mise run e2e:attachment` builds an isolated kind environment, creates the cluster with `locoadm`, enrolls the agent and deploys an application. `--no-teardown` keeps the environment; `--skip-build` reuses its binaries and images on another run.
