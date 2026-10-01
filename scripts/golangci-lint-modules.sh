#!/usr/bin/env bash
set -euo pipefail

root=$(git rev-parse --show-toplevel)
modules=(api controller agent k8sapi observability-proxy)

selected=""
for file in "$@"; do
	case $file in
	gen/*) continue ;;
	esac
	module=.
	for candidate in "${modules[@]}"; do
		if [[ $file == "$candidate/"* ]]; then
			module=$candidate
			break
		fi
	done
	if [[ " $selected " != *" $module "* ]]; then
		selected="$selected $module"
	fi
done

status=0
for module in $selected; do
	echo "golangci-lint: $module"
	(cd "$root/$module" && golangci-lint run --config="$root/.golangci.yml" --timeout=5m) || status=1
done
exit $status
