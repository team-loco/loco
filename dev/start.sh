#!/usr/bin/env bash
set -euo pipefail

orbstack_sock="$HOME/.orbstack/run/docker.sock"
if [ -z "${DOCKER_HOST:-}" ] && [ -S "$orbstack_sock" ]; then
  export DOCKER_HOST="unix://$orbstack_sock"
fi
mise run doctor

set -- bash -c 'go run ./tools/localstack install && mise run ui'
infisical="--projectId 01f016e0-4b6e-49fd-bbc0-072b89cfc699 --env dev --silent --log-level warn"
if infisical export $infisical >/dev/null 2>&1 </dev/null; then
  exec infisical run $infisical -- "$@"
fi
if [ -z "${GH_OAUTH_CLIENT_ID:-}" ]; then
  echo "the Dex login page has no GitHub button: run infisical login with an account that can read the Loco Infisical project" >&2
fi
exec "$@"
