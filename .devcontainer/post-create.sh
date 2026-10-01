#!/usr/bin/env bash
set -euo pipefail

curl -fsSL https://mise.run | MISE_VERSION=v2026.9.15 sh
export PATH="$HOME/.local/bin:$PATH"
echo 'eval "$(~/.local/bin/mise activate bash)"' >> ~/.bashrc

mise trust --yes .
mise run setup
