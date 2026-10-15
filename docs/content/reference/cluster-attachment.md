# Cluster attachment

The agent authenticates to the control plane with an operator-configured shared secret. The control plane creates its cluster record on the first authenticated registration. No cluster rows or agent tokens need to be seeded.

Set `LOCO_AGENT_CONNECTION_SECRETS` on the API process and `AGENT_TOKEN` on each agent to the same value. The API accepts a comma-separated list during rotation. An unset list disables agent authentication. Secrets must not contain commas.

Configure the agent's display name, region and tier:

```yaml
agent:
  clusterName: production-east
  clusterRegion: us-east-1
  clusterTier: production
env:
  CONTROL_PLANE_URL: https://api.example.com
  AGENT_TOKEN: <connection-secret>
```

The tier is `dev`, `staging` or `production`. The agent reads the UID of Kubernetes' `kube-system` namespace as its cluster key. The Helm chart grants read access to that namespace. Restarts, upgrades and agent reinstalls reuse the record. A replacement Kubernetes cluster gets a new record, even with the same display name.

Registration updates the display name, capacity and agent version. A changed region or tier is rejected. New clusters become eligible for deployment after healthy heartbeats arrive. Registration and heartbeats preserve the control plane's paused state.

All holders of a shared secret are trusted to assert a cluster identity. Use TLS outside a local test environment.

## Pause, remove and restore

Run the API binary with the control plane's `DATABASE_URL`. These commands use operator database access and do not require a user login:

```sh
loco-api cluster list
loco-api cluster pause <cluster-id>
loco-api cluster remove <cluster-id>
loco-api cluster restore <cluster-id>
loco-api cluster resume <cluster-id>
```

Pausing prevents new deployments and queued builds. Existing workloads and their agent connection remain available for inspection and cleanup. An assignment already committing finishes before pause returns.

Removal requires a paused cluster and refuses while placements, active deployments or queued/running builds remain. Remove applications through the normal resource configuration flow and wait for their deletion acknowledgments before retrying. The command prints the blocking workload IDs.

For a permanently lost cluster, `loco-api cluster remove <cluster-id> --force` retains those records and prints the workloads whose state cannot be confirmed. Removal keeps cluster identity and history, disconnects the stream, and rejects registration and stream reconnects. It does not uninstall the agent or stop Kubernetes pods.

Restoring clears the removal marker and stale health reports, and leaves the cluster paused. Resume it explicitly; fresh healthy heartbeats are required before deployment. An offline cluster retains its record and can reconnect without restoration.

`LOCO_CLUSTER_OPERATION_TIMEOUT` sets the operator command timeout; the default is 30 seconds.

## Rotation

Configuration is read at process startup. There is no credential reload endpoint.

1. Set `LOCO_AGENT_CONNECTION_SECRETS=old-secret,new-secret` and roll every API replica.
2. Replace each agent's `AGENT_TOKEN` with `new-secret` and restart its pods.
3. Use `loco-api cluster list` to check each recent connection's `connectionCredentialFingerprint` against the SHA-256 fingerprint of the replacement secret. Account for offline clusters before retiring the old value.
4. Set `LOCO_AGENT_CONNECTION_SECRETS=new-secret` and roll every API replica again.

The fingerprint records the credential used to initiate the latest stream; registration requests do not change it. An offline cluster retains its last fingerprint and heartbeat.

The last rollout closes connections on the old processes. Rotation completes only after all old API processes have stopped; during a rolling update, old replicas still accept their configured credentials. The cluster key does not change.

## Verification

`mise run e2e:attachment` builds an isolated kind environment, starts with no cluster rows, attaches the agent and deploys an application. `--no-teardown` keeps the environment; `--skip-build` reuses its binaries and images on another run.

Existing installations must associate each retained cluster row with its Kubernetes `kube-system` UID before switching agent protocols. This repository edits the initial schema in place and requires a fresh development database when the schema changes.
