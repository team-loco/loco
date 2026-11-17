---
title: "Cluster enrollment"
---

Cluster enrollment binds a worker's agent identity to its API registration. Flux installs the worker software; `locoadm` registers the cluster and issues the single-use join token.

## Register a worker

Bootstrap the worker’s Flux against its repository directory first and wait for the bundle to create `loco-system`. Sign in as a system administrator, then deliver the one-time join-token Secret directly to the worker:

```sh
locoadm login --api https://api.example.com --web https://app.example.com
locoadm cluster create --name worker-staging --region example-east \
  --kube-context worker-staging
```

`--kube-context` creates or updates the Secret through the Kubernetes client and prints only a confirmation. A missing namespace fails with instructions to bootstrap Flux first. `-o secret` prints a plaintext Kubernetes Secret manifest for operator-managed delivery and cannot be combined with `--kube-context`. `--namespace` defaults to `loco-system`; `--secret-name` defaults to `loco-agent-join-token`. Reference that Secret through the worker chart's `agent.joinToken.existingSecret`. The token is bootstrap material outside the operator repository. Core requires no SOPS executable or recipient flags.

Default output is the JSON API response, including the token. Keep both forms out of logs.

Tokens default to one hour. A positive `--ttl` is capped by `LOCO_CLUSTER_JOIN_TOKEN_MAX_TTL`, which defaults to seven days. Operators configure the default through `LOCO_CLUSTER_JOIN_TOKEN_TTL`. The API stores a hash of each token and accepts it once.

For fully unattended installs, an encrypted token committed to the worker configuration is an option. Use a longer `--ttl`, encrypt the manifest with operator tooling, add its resource entry and configure Flux decryption. Remove the spent file after enrollment.

## Reconcile and inspect

Provision any decryption key your chosen Secret tooling requires. The bundle's worker entry point waits for infrastructure and cluster configuration. The agent reads the mounted token file and retries enrollment with backoff while the token is missing, empty or rejected. Its retained identity supports restarts without another join token.

```sh
locoadm cluster list
locoadm status
```

Status reports pending or enrolled clusters, last heartbeats, API and agent versions and unclaimed administrators. Read the gateway’s load balancer address from Kubernetes or the provider:

```sh
kubectl --context worker-staging get gateway -n loco-system
```

Point the application wildcard, such as `*.apps.example.com`, at that address once, or run external-dns. Wait for certificates. Application environments can share a worker cluster. Each keeps separate workloads, secrets and deployment history. Loco’s own staging and production installations use separate clusters.

## Renew a pending token

```sh
locoadm cluster join-token CLUSTER_UUID --kube-context worker-staging
```

If a pending token expires, re-run `locoadm cluster join-token CLUSTER_UUID --kube-context worker-staging`. A fresh token invalidates the preceding one. Apply the new Secret directly to the pending worker. The agent identity remains in the cluster after enrollment.

`CreateCluster` requires a UUID request ID. Programmatic callers persist it and reuse it after a lost response: retries recover the same registration, while changed identity fields fail. Each CLI create invocation generates a fresh request ID. Use `join-token` for an existing registration.

Cluster pause, resume, remove and restore remain API operations. Removing an API registration does not provision or destroy Kubernetes infrastructure. Manage the cluster and its software through the operator's infrastructure and Flux workflows.
