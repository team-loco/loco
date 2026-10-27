# Local development

The local environment runs Loco's control plane and cluster components for development. Install mise and Docker or OrbStack, then use the pinned repository tools:

```sh
mise run setup
```

Tool versions live in `mise.toml` and `mise.lock`. The local API reads its configuration from `dev/api.env`, which holds only dev values, so a fresh clone needs no `.env` file.

```sh
mise run tilt
```

Tilt starts the local cluster and services. The API listens at `http://localhost:8000`, and the dashboard at `http://localhost:5173`.

Each git worktree gets its own stack, so several can run at once. A worktree takes the first free slot N from 1 to 9, and its ports move up by 100×N: slot 1 serves the API at `http://localhost:8100` and the dashboard at `http://localhost:5273`. Run `mise run stack` for the current checkout's URLs, and `mise run down` to delete its stack before removing the worktree.

## Connect the CLI

```sh
mise run build
./bin/loco login --host http://localhost:8000
./bin/loco config set webHost http://localhost:5173
```

Make the signed-in user the local installation's system admin with the `LOCO_BOOTSTRAP_TOKEN` that `dev/api.env` sets:

```sh
mise run build:locoadm
./bin/locoadm bootstrap --host http://localhost:8000 --token "$(sed -n 's/^LOCO_BOOTSTRAP_TOKEN=//p' dev/api.env)"
```

## Attach the local cluster

The local agent enrolls with a join token. With an account that has `system:admin`, create the cluster, stop Tilt, and start it again with the printed token in `LOCO_JOIN_TOKEN` so it redeploys `loco-worker` with it:

```sh
mise run build:locoadm
./bin/locoadm cluster create --name loco-local --region us-east-1 --tier production
mise run stop
LOCO_JOIN_TOKEN=<printed token> mise run tilt
```

The agent keeps its key in the `loco-worker-agent-key` Secret, so the token is needed again only after the kind cluster or that Secret is deleted.

## Tasks

```sh
mise tasks
mise run test:cli
mise run lint:go
mise run docs:serve
```

Use the repository's tasks for builds, tests, code generation, and linting so local tools match CI. See [documentation maintenance](documentation.md) to edit this site.
