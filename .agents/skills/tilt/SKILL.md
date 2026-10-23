---
name: tilt
description: Run the full local stack (kind cluster, compose services, API, web UI) with Tilt, from the main checkout or a git worktree, alongside other checkouts' stacks, and check a change in the browser. Load before running `mise run tilt` or looking at the local web UI. PR screenshots come from the ui-evidence skill, which needs no Tilt.
---

# Running the local stack with Tilt

`mise run tilt` starts everything: the kind cluster, Postgres, Valkey, the registry and
the source bucket from `compose.yaml`, the helm releases, the API and the web UI.

`mise run stop` stops Tilt, the compose services and the kind nodes and keeps their state;
`mise run start` (an alias of `mise run tilt`) starts the stopped nodes and brings the rest back.
`mise run down` deletes the stack: its kind cluster, its compose containers and volumes.

## One stack per checkout

Each checkout runs its own stack, so several can run at once. `env/local/stack.sh` gives
the main checkout slot 0 and each worktree the first free slot from 1 to 9, recorded in
`.git/loco-stacks/` of the main checkout. Slot N names the kind cluster
`loco-cluster-local-N` and the compose project `loco-dev-N`, and adds 100×N to every
local port: slot 2 serves the API on :8200, the web UI on :5373, Dex on :5756 and the
Tilt UI on :10550. The main checkout keeps `loco-cluster-local`, `loco-dev` and the ports
in `.env`. `mise run stack` prints the current checkout's URLs, kube context and compose
project.

The mise tasks pin the stack's kube context and compose project themselves. Commands you
run by hand do not: pass `--context kind-loco-cluster-local-N` to kubectl, prefix
`tilt get` and `tilt logs` with `TILT_PORT=<port>`, and use
`docker compose -p loco-dev-N`. A worktree's `mise run tilt` leaves the current kubectl
context alone.

`mise run stop` and `mise run down` only touch the current checkout's stack. Stop a Tilt
you started with `TaskStop` on its background task, and leave another session's stack
running. Run `mise run down` before removing a worktree; a removed worktree's slot goes to
the next worktree that asks, together with any cluster and volumes left behind.

A running stack takes 2 to 4 GB of Docker memory. An 8 GB Docker VM fits two; a third
runs the VM out of memory and the kernel kills pods in every cluster, including stacks
other sessions own. Check `docker stats --no-stream` before starting one next to others,
and stop yours with `mise run stop` when you are done with it.

## Worktree setup

A new worktree has no `.env` and no `node_modules`, and mise does not trust its config.

1. `ln -s <main checkout>/.env .env` in the worktree root. The task refuses to start
   without `.env`, and mise reads it once at start, so a link added later needs a restart.
2. `mise trust`, then `mise run setup` in the worktree root. The root `bun install` it runs
   covers `web/`, `gen/ts` and the buf plugins that `mise run gen` needs.

## Start and wait

Run `mise run tilt` with `run_in_background` and `timeout: 7200000`; it never exits, and
the default background limit of 30 minutes stops it, and the stack with it, mid-task.
Wait for the API in a second background command instead of polling in the foreground,
with the Tilt UI port from `mise run stack`:

```sh
until s=$(TILT_PORT=<port> tilt get uiresource api -o jsonpath='{.status.runtimeStatus}' 2>/dev/null); [ "$s" = ok ] || [ "$s" = error ]; do sleep 3; done; echo "$s"
```

`tilt logs <resource>` shows why a resource failed; `tilt trigger <resource>` reruns it.

## Docker

The task points `DOCKER_HOST` at OrbStack when `~/.orbstack/run/docker.sock` exists, so
the active `docker context` does not matter. Without OrbStack, start Docker Desktop.

## Stale local database

Migrations are edited in place, so goose treats an edited migration as applied and the
local schema falls behind. The symptom is a Postgres error such as
`relation "builds" does not exist` in `tilt logs api`. Resetting deletes all local data,
so ask the user first, then stop Tilt and run, with the compose project from
`mise run stack` in place of `loco-dev`:

```sh
docker compose -p loco-dev rm -sf postgres
docker volume rm loco-dev_postgres-data
```

Start Tilt again; `db-migrate` recreates the schema and the API recreates the platform domains in `LOCO_PLATFORM_DOMAINS` from `.env` when it starts.

## Browser checks

- Use Chrome on the web UI URL from `mise run stack`.
- Sign in yourself; never ask the user to. Click Sign in on the landing page, then on the
  Dex login page enter `dev@loco.test` and `loco-dev`, the static user in
  `env/local/dex.yaml`. Dex keeps its sessions in memory, so restarting Dex, the compose
  services or OrbStack signs every browser out.
- The CLI signs in through the same page: `loco login --host <API URL>`. The CLI keeps one
  host in its config for every stack, so log in again after switching stacks.
- Never start an ad-hoc local server (for example `python3 -m http.server`) to preview
  files. Auto mode then blocks the Chrome tools for the session.
- The theme lives in `localStorage` key `loco:theme:v1` (`light` or `dark`). Set it, then
  navigate to reload.
- After `navigate`, the first click only focuses the tab. Click once more to open a menu or
  dropdown, and take a throwaway screenshot before the saved one so fade-in animations
  finish.
