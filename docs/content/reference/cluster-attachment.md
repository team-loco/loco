---
title: "Cluster enrollment"
---

Cluster enrollment binds a worker's agent identity to its API registration. Flux installs the worker software; `locoadm` registers the cluster and issues the single-use join token.

## Register a worker

Sign in as a system administrator, then print a join-token Secret manifest:

```sh
locoadm login --api https://api.example.com --web https://app.example.com
locoadm cluster create --name worker-staging --region example-east --tier staging \
  --ttl 72h -o secret > clusters/worker-staging/config/join-token.yaml
```

`-o secret` prints a plaintext Kubernetes Secret manifest. `--namespace` defaults to `loco-system`; `--secret-name` defaults to `loco-agent-join-token`. Reference that Secret through the worker chart's `agent.joinToken.existingSecret`. Encrypt the file with your own tooling before committing and configure Flux decryption, or supply the same Secret through External Secrets. Core requires no SOPS executable or recipient flags.

Default output is the JSON API response, including the token. Keep both forms out of logs.

Tokens default to one hour. A positive `--ttl` is capped by `LOCO_CLUSTER_JOIN_TOKEN_MAX_TTL`, which defaults to seven days. Operators configure the default through `LOCO_CLUSTER_JOIN_TOKEN_TTL`. The API stores a hash of each token and accepts it once.

## Reconcile and inspect

Bootstrap the worker's Flux against its own repository directory and provision any decryption key your chosen Secret tooling requires. The bundle's worker entry point waits for infrastructure and cluster configuration. The agent reads the mounted token file and retries enrollment with backoff while the token is missing, empty or rejected. Its retained identity supports restarts without another join token.

```sh
locoadm cluster list
locoadm status
```

Status reports pending or enrolled clusters, last heartbeats, API and agent versions, unclaimed administrators and gateway DNS targets. DNS targets are reported before certificate readiness. Point the required application names at those targets, then wait for certificates. Each environment runs on its own clusters; staging and production do not share a worker.

## Renew a pending token

```sh
locoadm cluster join-token CLUSTER_UUID --ttl 72h \
  -o secret > clusters/worker-staging/config/join-token.yaml
```

A fresh token invalidates the preceding one. Encrypt the file before committing and let Flux deliver it, or apply it through your own Secret delivery tooling. After enrollment, remove the consumed token manifest and its resource entry when desired; the agent identity remains in the cluster.

`CreateCluster` requires a UUID request ID. Programmatic callers persist it and reuse it after a lost response: retries recover the same registration, while changed identity fields fail. Each CLI create invocation generates a fresh request ID. Use `join-token` for an existing registration.

Cluster pause, resume, remove and restore remain API operations. Removing an API registration does not provision or destroy Kubernetes infrastructure. Manage the cluster and its software through the operator's infrastructure and Flux workflows.
