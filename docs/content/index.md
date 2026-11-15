---
title: "Deploy with Loco"
---

Loco builds, deploys, and scales containerized applications on Kubernetes. Use the CLI to work with your applications and the dashboard to inspect their state.

> From your terminal. Onto Kubernetes.
>
> Loco handles application orchestration across regions, with HTTPS routing, resource controls, and observability.

[Install the CLI](getting-started/install.md) · [Choose a deployment mode](deployment/modes.md)

## Where to start

| Your task | Read |
| --- | --- |
| Connect to a Loco instance | [Install](getting-started/install.md) and [connect](getting-started/connect.md) |
| Choose who operates the platform | [Multi-tenant SaaS, dedicated, and self-hosted](deployment/modes.md) |
| Declare services and deploy them | [loco.yaml](deployment/loco-yaml.md) |
| Deploy one service with API defaults | Run `loco deploy` from the application directory |
| Inspect running applications | [Application operations](operations/applications.md) |
| Run Loco yourself | [Self-hosting](operations/self-hosting.md) |
| Contribute to the platform | [Local development](contributing/development.md) |

## One platform, three deployment modes

Loco's deployment model covers multi-tenant SaaS, dedicated, and self-hosted installations. The [deployment modes](deployment/modes.md) guide separates platform operation from application deployment and records current availability.

## Documentation and releases

Pages describe the behaviour of the main branch. Run `loco help` to see the commands your installed release supports.

For AI tools, use [Markdown exports and the documentation index](reference/ai.md).
