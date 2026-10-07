# Install the CLI

The Loco CLI deploys and manages applications from your terminal. Install the released binary for Linux or macOS on amd64 or arm64:

```sh
curl -fsSL https://loco.build/install.sh | sh
```

The installer verifies the release checksum and places the binary in `~/.local/bin`. Add that directory to your shell's `PATH` if the installer reports it is missing.

```sh
loco version
loco help
```

## Select a release

Set `LOCO_VERSION` to an existing release tag to install a specific version. Set `LOCO_INSTALL_DIR` or pass `--bin-dir` to change the destination. See the [published releases](https://github.com/team-loco/loco/releases) for available versions.

```sh
loco update
```

`loco update` replaces the installed binary with the latest release.

## Shell completions

```sh
loco completion zsh
loco completion bash
```

Save the output using your shell's completion installation instructions.

Continue with [connecting to Loco](connect.md).
