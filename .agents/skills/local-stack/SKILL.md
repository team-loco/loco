---
name: local-stack
description: Run the local platform through Flux and inspect the host dashboard.
---

# Local stack

Run `mise run dev` to start the isolated kind cluster, Postgres, registry, source bucket and Dex fixtures, reconcile the platform bundle through Flux, approve device login and enroll the production worker. The web dev server runs on the host. Each checkout keeps its private kubeconfig, credentials, seal key and image-input cache in its stack state directory.

Run at most one of your own stacks. Leave other sessions' stacks, worktrees and the demo alone. Before building, require 5 GiB available Docker memory and 20 GB free disk.

`mise run stack` prints this checkout's ports and context. `mise run loco` and `mise run locoadm` use the stack's credential home. Sign in as `dev@loco.test` with password `loco-dev`.

`mise run dev:deploy` rebuilds images whose Docker inputs changed, pushes the bundle and local configuration artifact, and reconciles Flux. `mise run dev:deploy --suspend` pauses the platform HelmReleases for manual iteration. Resume them with `flux resume helmrelease` using the stack kubeconfig.

`mise run stop` stops the dashboard, fixtures and kind node while retaining state. `mise run down` removes the stack and releases its slot. Export the apps CA with `mise run stack:ca`.

Inspect API logs in namespace `loco-system` at deployment `loco-control-plane-api`, and agent logs at deployment `loco-worker-agent`. Use the stack kubeconfig without changing the user's current context.
