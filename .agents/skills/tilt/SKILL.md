---
name: tilt
description: Run the full local stack (kind cluster, compose services, API, web UI) with Tilt, from the main checkout or a git worktree, and check a change in the browser. Load before running `mise run tilt`, looking at the web UI on localhost:5173, or taking PR screenshots.
---

# Running the local stack with Tilt

`mise run tilt` starts everything: the kind cluster, Postgres, Valkey, the registry and
the source bucket from `compose.yaml`, the helm releases, the API on :8000 and the web UI
on :5173. The Tilt UI is on :10350.

## One stack at a time

Every checkout shares the compose project `loco-dev`, its volumes (`loco-dev_postgres-data`
holds the database) and the same ports. Before starting Tilt, stop any Tilt already
running: `TaskStop` on its background task, or `tilt down` in its checkout.

## Worktree setup

A new worktree has neither `.env` nor `web/node_modules`.

1. `ln -s <main checkout>/.env .env` in the worktree root. The task refuses to start
   without `.env`, and mise reads it once at start, so a link added later needs a restart.
2. `bun install --frozen-lockfile` in `web/`.

## Start and wait

Run `mise run tilt` with `run_in_background`; it never exits. Wait for the API in a
second background command instead of polling in the foreground:

```sh
until s=$(tilt get uiresource api -o jsonpath='{.status.runtimeStatus}' 2>/dev/null); [ "$s" = ok ] || [ "$s" = error ]; do sleep 3; done; echo "$s"
```

`tilt logs <resource>` shows why a resource failed; `tilt trigger <resource>` reruns it.

## Docker

The task points `DOCKER_HOST` at OrbStack when `~/.orbstack/run/docker.sock` exists, so
the active `docker context` does not matter. Without OrbStack, start Docker Desktop.

## Stale local database

Migrations are edited in place, so goose treats an edited migration as applied and the
local schema falls behind. The symptom is a Postgres error such as
`relation "builds" does not exist` in `tilt logs api`. Resetting deletes all local data,
so ask the user first, then stop Tilt and run:

```sh
docker compose -p loco-dev rm -sf postgres
docker volume rm loco-dev_postgres-data
```

Start Tilt again; `db-migrate` recreates the schema and seeds `api/seed/local.sql`.

## Browser checks

- Use Chrome on http://localhost:5173. The user's sign-in persists there.
- Never start an ad-hoc local server (for example `python3 -m http.server`) to preview
  files. Auto mode then blocks the Chrome tools for the session.
- The theme lives in `localStorage` key `loco:theme:v1` (`light` or `dark`). Set it, then
  navigate to reload.
- For "before" screenshots, commit the branch, run `git checkout origin/main -- web/src`
  in the worktree and let Vite reload; `git checkout HEAD -- web/src` restores the branch.
