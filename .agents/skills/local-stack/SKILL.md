---
name: local-stack
description: Run the local installation with mise, deploy platform changes through locoadm, inspect workloads and check the host dashboard.
---

# Local stack

`mise run dev` installs the control plane and one production worker through `mise run stack:install`, then runs `mise run ui` on the host. `locoadm` owns installation and upgrades through Helm. Postgres, Dex, the registry and the source bucket use `dev/compose.local.yaml`; managed OpenBao, ClickHouse and gateways run in Kubernetes.

## Resources and ownership

Run at most one of your own stacks. Leave other sessions' stacks, worktrees and the demo alone. Check `docker stats --no-stream` and `df -h ~` before builds. Start only with at least 5 GiB available in the container VM and 20 GB free disk; when memory is low, finish independent work and recheck every few minutes. Read `MemAvailable` from `/proc/meminfo` in a running container, including reclaimable cache.

One idle production-worker stack measured 2.765 GiB in Docker: 2.632 GiB for the kind node and 136.427 MiB for fixtures. Pod metrics totaled 2367 MiB. The VM capacity was 15.66 GiB. Host UI memory and build transients are excluded. Plan one stack on an 8 GB VM and require build headroom.

## Slots and retained state

`dev/stack.sh` assigns slot 0 to the main checkout and slots 1 through 9 to worktrees. Slot N uses `loco-dev-N.localhost` with a 100×N port offset. `mise run stack` prints URLs, the private kubeconfig and state directory under the shared git directory's `loco-stacks/state/<slot>/`.

Use `mise run loco` and `mise run locoadm`: both supply the stack's credential home, `LOCO_CREDENTIAL_STORE=file` and kubeconfig. The operator wrapper selects the rendered installation file. Keep the developer's ordinary session and kube context unchanged.

## Start, deploy, stop and reset

Copy `CLAUDE.local.md` into a new worktree, then run `mise trust` and `mise run setup`. Run `mise run dev` as a long-lived process. Installation approves device login for `dev@loco.test` and attaches the worker automatically. Check `mise run locoadm status` for running, worker ready and admins claimed before opening the dashboard.

With the stack running, `mise run dev:deploy` builds a release from the checkout, applies it and upgrades the worker through the installer. It does not overwrite workload images outside Helm. It retains fixture processes and data. An interrupted deploy resumes through `mise run dev` with its published release; do not discard state automatically.

`mise run stop` stops the host dashboard, fixtures and kind node, preserving installation state. `mise run start` resumes without another release or Helm install. Ordinary startup rejects changed installation inputs. `dev:deploy` explicitly applies installed-stack changes; fixture or topology changes require `mise run dev:reset` and then `mise run dev`. Reset deletes the owned cluster, volumes and credentials while keeping its slot; `mise run down` also releases the slot.

Inspect logs with the printed kubeconfig and `kubectl -n loco-control-plane logs deployment/loco-control-plane-api --follow` or `kubectl -n loco-system logs deployment/loco-worker-agent --follow`. Use an installed `k9s` for interactive inspection. Prefix pinned tools with `mise exec --` when outside mise tasks.

## Browser and certificates

Sign in to the host dashboard as `dev@loco.test` with password `loco-dev`. The account claims system admin during installation; signup is open. The worker targets production and region `local`. Dex sessions are in memory; restarting Dex signs browsers out.

Apps use `https://<name>.apps.<stack-domain>:<APPS_PORT>` with self-signed certificates. Accept the browser warning. `mise run stack:ca` exports the apps CA. `locoadm verify` targets installs serving apps on port 443 and is not used against this stack.

Use Chrome when available, or the repository's isolated Playwright tooling for a browser flow. Do not start an ad-hoc static server.
