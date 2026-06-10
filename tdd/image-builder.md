# TDD: OCI Image Builder Abstraction

## Problem

`internal/docker/client.go` hardwires the Docker SDK throughout the deploy flow.
Users without Docker (e.g. running Podman, nerdctl, or any other OCI-compatible
runtime) get confusing errors. The version check is Docker-specific and would
produce wrong results against other runtimes.

## Goal

- Support any OCI-compliant runtime that exposes a Docker-compatible socket
- User can point loco at any socket via flag or env var
- Auto-detect the right socket when nothing is specified
- Version enforcement only applies where it makes sense (Docker only)

---

## User-facing interface

### Flag

```
--docker-socket unix:///path/to/socket
```

### Env var (consistent with other loco env vars)

```
LOCO__DOCKER_SOCKET=unix:///path/to/socket
```

### Resolution order

`--docker-socket` flag → `LOCO__DOCKER_SOCKET` env var → auto-detect

### Auto-detection order

1. Docker socket (`/var/run/docker.sock`, `~/.docker/run/docker.sock` on macOS)
2. Podman socket (`/run/user/$UID/podman/podman.sock`)
3. Error with actionable message suggesting `--image`

---

## Why a socket is enough

Podman, nerdctl, and Rancher Desktop all expose a Docker-compatible REST API on
their socket. The existing Docker SDK (`github.com/docker/docker/client`) can be
pointed at any of these via `client.WithHost("unix:///path/to/socket")` — no
new SDK or interface abstraction needed.

---

## Version check behaviour

`ServerVersion` returns both a version string and a `Platform.Name`. We branch on
the platform name:

| `Platform.Name` | Behaviour |
|---|---|
| `"Docker Engine - Community"` | enforce minimum version (`28.0.0`) |
| `"Podman Engine"` | skip — Podman uses its own version scheme |
| anything else | skip — unknown runtime, don't enforce Docker versions |

This is checked immediately after connecting, before any build or push operation.

---

## Changes required

### `internal/docker/client.go`

- `NewClient(cfg, socket string)` — add `socket` parameter
- If `socket != ""`: pass it to `client.WithHost(socket)`, skip filesystem
  socket-existence check (user is explicit, trust them)
- If `socket == ""`: run auto-detection, check socket exists before connecting
- After `ServerVersion` succeeds, branch on `Platform.Name` for version enforcement
- Remove the raw `v.Version < MINIMUM` string comparison (use `semver` or
  split on `.` for a proper numeric comparison)

### `cmd/loco/cmdutil/flags.go`

Add `GetDockerSocket(cmd) (string, error)` following the same flag → env → default
pattern as `GetHost`:

```go
func GetDockerSocket(cmd *cobra.Command) (string, error) {
    // 1. --docker-socket flag
    // 2. LOCO__DOCKER_SOCKET env var
    // 3. return "" (triggers auto-detect in NewClient)
}
```

### `cmd/loco/resource/deploy.go`

- `deployDeps.NewDockerClient` signature becomes `func(cfg, socket string)`
- Add `--docker-socket` flag to the deploy command
- Resolve socket via `cmdutil.GetDockerSocket` and pass through

---

## Error messages

| Situation | Message |
|---|---|
| Custom socket provided, daemon not responding | `"socket <path> is not responding — is the runtime running?\n  Or deploy a pre-built image with: --image <your-image>"` |
| Auto-detect, no socket found on macOS | `"Docker does not appear to be running — please start Docker Desktop\n  Or deploy a pre-built image with: --image <your-image>"` |
| Auto-detect, no socket found on Linux | `"no container runtime socket found — is Docker or Podman running?\n  Or deploy a pre-built image with: --image <your-image>"` |
| Daemon responds, Docker version too old | `"Docker <version> is not supported, minimum required is 28.0.0 — please update Docker"` |

---

## Out of scope

- Supporting runtimes that do not expose a Docker-compatible API (e.g. raw containerd)
- Server-side / remote builds (separate TDD)
- Buildpack / Nixpacks support (separate TDD)
