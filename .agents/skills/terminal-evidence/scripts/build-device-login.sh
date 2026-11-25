#!/usr/bin/env bash
# Build the real loco CLI with a pending device-login fixture, without editing the checkout.
set -euo pipefail

if [[ $# -ne 2 ]]; then
	printf 'Usage: %s CHECKOUT OUTPUT_BINARY\n' "$0" >&2
	exit 2
fi

checkout=$(cd "$1" && pwd)
mkdir -p "$(dirname "$2")"
output=$(cd "$(dirname "$2")" && pwd)/$(basename "$2")
fixture=$(cd "$(dirname "$0")/../examples" && pwd)/device_login_test.go.txt
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

cd "$checkout"
go_bin=$(mise which go)
python_bin=$(mise which python)
"$python_bin" - "$checkout/cmd/loco/terminal_evidence_test.go" "$fixture" > "$scratch/overlay.json" <<'PY'
import json
import sys

print(json.dumps({"Replace": {sys.argv[1]: sys.argv[2]}}))
PY

cd cmd
"$go_bin" test -overlay "$scratch/overlay.json" -c -o "$output" ./loco
