#!/usr/bin/env bash
set -euo pipefail

modules=""
for file in "$@"; do
	dir=$(dirname "$file")
	while [[ $dir != . && ! -f $dir/go.mod ]]; do
		dir=$(dirname "$dir")
	done
	case $dir in
	gen/*) continue ;;
	esac
	if [[ " $modules " != *" $dir "* ]]; then
		modules="$modules $dir"
	fi
done

if [[ -n $modules ]]; then
	exec make lint-go GO_MODULES="${modules# }"
fi
