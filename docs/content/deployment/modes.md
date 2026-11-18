---
title: "Deployment modes"
---

A deployment mode determines who operates Loco and whether the platform serves one customer or multiple customers. Application environments such as staging and production exist within that platform.

Loco runs in three modes, which differ in who operates the platform and who shares it.

| Mode | Platform operator | Platform tenancy | Application operator |
| --- | --- | --- | --- |
| Multi-tenant SaaS | Loco | Multiple customers share the platform | Your team |
| Dedicated | Loco | A platform installation for one customer | Your team |
| Self-hosted | Your team | Your team controls the installation and tenancy | Your team |

## Multi-tenant SaaS

Connect to Loco's managed API and dashboard. Your team works with organizations, workspaces, and applications. Loco operates the platform's control plane and Kubernetes infrastructure.

```sh
loco login
loco web
```

See [architecture](../reference/architecture.md) for how the control plane and regional components fit together.

## Dedicated

A dedicated installation gives one customer a Loco-operated platform. Use the installation's API and dashboard URLs. Instance provisioning, capacity, and operational responsibilities need an agreement with the operator; these docs do not define pricing or service guarantees.

## Self-hosted

Your team operates the control plane, Kubernetes clusters, networking, certificates, registry, database, and observability. Your team also owns upgrades, backups, capacity, and incident response.

Start with the [self-hosting guide](../operations/self-hosting.md). [Installation](../reference/installation.md) scaffolds the platform repository with `locoadm init` and installs it through Flux.

## Environments within a mode

Staging and production are application targets, independent of deployment mode. A dedicated or self-hosted installation can also have staging and production environments. Keep each instance's credentials and endpoints separate. See [environments](environments.md).
