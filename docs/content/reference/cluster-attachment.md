---
title: "Cluster enrollment"
---

Cluster enrollment binds a worker's agent identity to its API registration. Flux installs the worker software; `locoadm` registers the cluster and issues the single-use join token.

## Register a worker

Sign in as a system administrator, then apply the one-time join-token Secret directly to the worker:

```sh
locoadm login --api https://api.example.com --web https://app.example.com
locoadm cluster create --name worker-staging --region example-east --tier staging \
  -o secret | kubectl --context worker-staging apply -f -
```

`-o secret` prints a plaintext Kubernetes Secret manifest. `--namespace` defaults to `loco-system`; `--secret-name` defaults to `loco-agent-join-token`. Reference that Secret through the worker chart's `agent.joinToken.existingSecret`. Apply it to the worker with kubectl; it is bootstrap material outside the operator repository. Core requires no SOPS executable or recipient flags.

Default output is the JSON API response, including the token. Keep both forms out of logs.

Tokens default to one hour. A positive `--ttl` is capped by `LOCO_CLUSTER_JOIN_TOKEN_MAX_TTL`, which defaults to seven days. Operators configure the default through `LOCO_CLUSTER_JOIN_TOKEN_TTL`. The API stores a hash of each token and accepts it once.

For fully unattended installs, an encrypted token committed to the worker configuration is an option. Use a longer `--ttl`, encrypt the manifest with operator tooling, add its resource entry and configure Flux decryption. Remove the spent file after enrollment.

## Reconcile and inspect

Bootstrap the worker's Flux against its own repository directory and provision any decryption key your chosen Secret tooling requires. The bundle's worker entry point waits for infrastructure and cluster configuration. The agent reads the mounted token file and retries enrollment with backoff while the token is missing, empty or rejected. Its retained identity supports restarts without another join token.

```sh
locoadm cluster list
locoadm status
```

Status reports pending or enrolled clusters, last heartbeats, API and agent versions, unclaimed administrators and gateway DNS targets. DNS targets are reported before certificate readiness. Point the required application names at those targets, then wait for certificates. Each environment runs on its own clusters; staging and production do not share a worker.

## Renew a pending token

```sh
locoadm cluster join-token CLUSTER_UUID -o secret | \
  kubectl --context worker-staging apply -f -
```

A fresh token invalidates the preceding one. Apply the new Secret directly to the pending worker. The agent identity remains in the cluster after enrollment.

`CreateCluster` requires a UUID request ID. Programmatic callers persist it and reuse it after a lost response: retries recover the same registration, while changed identity fields fail. Each CLI create invocation generates a fresh request ID. Use `join-token` for an existing registration.

Cluster pause, resume, remove and restore remain API operations. Removing an API registration does not provision or destroy Kubernetes infrastructure. Manage the cluster and its software through the operator's infrastructure and Flux workflows.
