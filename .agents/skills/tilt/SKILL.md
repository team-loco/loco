---
name: tilt
description: Run the full local stack (kind cluster, compose services, API, web UI) with Tilt, from the main checkout or a git worktree, and check a change in the browser. Load before running `mise run tilt`, looking at the web UI on localhost:5173, or taking PR screenshots.
---

# Running the local stack with Tilt

`mise run tilt` starts everything: the kind cluster, Postgres, Valkey, the registry and
the source bucket from `compose.yaml`, the helm releases, the API on :8000 and the web UI
on :5173. The `obs-proxy-forward` resource forwards :8091 to the observability proxy, the
address the agent records for the dashboard's logs, metrics and events. The Tilt UI is on :10350.

`mise run stop` stops Tilt, the compose services and the kind nodes and keeps their state;
`mise run start` (an alias of `mise run tilt`) starts the stopped nodes and brings the rest back.
Don't run `tilt down`; it removes the compose containers other checkouts share.

## One stack at a time

Every checkout shares the compose project `loco-dev`, its volumes (`loco-dev_postgres-data`
holds the database) and the same ports. Before starting Tilt, check for one already
running with `pgrep -fl 'tilt up'`. Stop a Tilt you started with `TaskStop` on its
background task. A Tilt another session started belongs to that session: leave it
running, and skip `mise run stop`, which stops every Tilt on the local cluster. When it
blocks screenshots, open the PR as a draft without them and say so in the reply to the
user.

## Worktree setup

A new worktree has no `.env` and no `node_modules`, and mise does not trust its config.

1. `ln -s <main checkout>/.env .env` in the worktree root. The task refuses to start
   without `.env`, and mise reads it once at start, so a link added later needs a restart.
2. `mise trust`, then `mise run setup` in the worktree root. The root `bun install` it runs
   covers `web/`, `gen/ts` and the buf plugins that `mise run gen` needs.

## Start and wait

Run `mise run tilt` with `run_in_background` and `timeout: 7200000`; it never exits, and
the default background limit of 30 minutes stops it, and the stack with it, mid-task.
Wait for the API in a second background command instead of polling in the foreground:

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

- Use Chrome on http://localhost:5173.
- Sign in yourself; never ask the user to. Click Sign in on the landing page, then on the
  Dex login page enter `dev@loco.test` and `loco-dev`, the static user in
  `env/local/dex.yaml`. Dex keeps its sessions in memory, so restarting Dex, the compose
  services or OrbStack signs every browser out.
- The CLI signs in through the same page: `loco login --host http://localhost:8000`.
- Never start an ad-hoc local server (for example `python3 -m http.server`) to preview
  files. Auto mode then blocks the Chrome tools for the session.
- The theme lives in `localStorage` key `loco:theme:v1` (`light` or `dark`). Set it, then
  navigate to reload.
- After `navigate`, the first click only focuses the tab. Click once more to open a menu or
  dropdown, and take a throwaway screenshot before the saved one so fade-in animations
  finish.
- `resize_window` cannot reach a phone width: Chrome keeps a page zoom for localhost:5173
  and enforces a minimum window width. For a 390px screenshot, append a same-origin
  `<iframe>` 390px wide with the page URL, screenshot it and crop the image with
  `sips --cropOffset <y> <x> -c <height> <width>` (the offset goes before `-c`).
- For "before" screenshots, commit the branch, run `git checkout origin/main -- web/src`
  in the worktree and let Bun reload; `git checkout HEAD -- web/src` restores the branch.
