# Loco GitOps repository

This repository layout installs a control plane and a staging worker through versioned Loco bundles. Each cluster runs its own Flux and reads its own directory. Use Flux 2.10.0 or later; core pins the validation CLI in `mise.toml`.

## Configure

Copy this directory into your own repository, or use `locoadm init` with the release that includes GitOps scaffolding. Supply Kubernetes clusters with a CNI that enforces NetworkPolicy, external Postgres, an identity provider and a registry configured for Loco's token endpoint. Source builds also need object storage. The control-plane chart supplies ClickHouse and OpenBao; the static seal key belongs in offline custody with recovery material.

The Secret files contain plaintext example values. Supply your own values and encrypt them before committing, or deliver the same Secret names through External Secrets. Core requires no encryption tool. Set the production digest pins in `releases/control-plane.yaml` and `releases/workers-production.yaml` to a bundle v2 release. The zero digests are placeholders. Staging's `*` range follows stable releases after bundle v2 is published.

Set API and dashboard hostnames, the administrator email, region, application domains, registry service and telemetry endpoint in each cluster's values. Keep credentials in standalone Secrets delivered by your own tooling. The API environment Secret needs `DATABASE_URL`, `AUTH_ISSUERS`, registry settings and, for source builds, bucket credentials. The chart's default certificate issuer needs the DNS provider token in `cert-manager/cloudflare-api-token-secret`, key `api-token`, or configure your own solver or issuer. Enable OpenBao snapshots with your object storage and a standalone credentials Secret for production backups.

## Bootstrap

Bootstrap Flux to `clusters/control-plane` and `clusters/worker-staging`, using the Git host and bootstrap method you operate. Bootstrap writes each cluster's `flux-system/` directory. Git access is a bootstrap input outside reconciliation.

`loco.yaml` selects the bundle entry point and declares `loco-cluster-config`, which applies configuration after namespaces exist. The bundle's role layer waits for that configuration and infrastructure. Values ConfigMaps are generated without a name suffix so Helm releases can reference them.

Sign in as the configured administrator and register the worker:

```sh
locoadm login --api https://api.example.com --web https://app.example.com
locoadm cluster create --name worker-staging --region example-east --tier staging \
  -o secret | kubectl --context worker-staging apply -f -
```

A join token is one-time bootstrap material. Apply it immediately with the default one-hour TTL and keep it out of Git. The agent waits for delivery and retains its identity after enrollment.

For fully unattended installation, an encrypted token file committed to the worker configuration is an option. Issue it with a longer `--ttl`, encrypt it with operator tooling, add it to that configuration's resources, and configure Flux decryption. Remove the spent file after enrollment. Other credential Secrets can use SOPS or External Secrets; core requires neither tool.

For production workers, select `releases/workers-production.yaml` from their cluster directory and pin a digest. Upgrade the control plane first, then staging workers, then production workers, checking each cohort's health before proceeding. Each cluster's Flux reconciles only its own configuration.

## Validate

From core, run `mise run test:gitops`. The check runs `flux build` for both cluster roots and configuration layers, validates resources with kubeconform and Flux CRD schemas, and renders both charts with the example values. It needs no kubeconfig or decryption keys. CI runs it on example, chart and bundle changes.
