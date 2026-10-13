# Local development

The local environment runs Loco's control plane and cluster components for development. Install mise and Docker or OrbStack, then use the pinned repository tools:

```sh
mise run setup
```

Copy `.env.example` to `.env` and supply the credentials required by that revision. Tool versions live in `mise.toml` and `mise.lock`.

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

## Tasks

```sh
mise tasks
mise run test:cli
mise run lint:go
mise run docs:serve
```

Use the repository's tasks for builds, tests, code generation, and linting so local tools match CI. See [documentation maintenance](documentation.md) to edit this site.
