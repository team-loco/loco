#!/usr/bin/env bash

max_slot=9
port_stride=100

stack_root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
stack_claims="$(git -C "$stack_root" rev-parse --path-format=absolute --git-common-dir)/loco-stacks"
stack_env="$stack_root/dev/api.env"

claim_slot() {
	local claim slot
	if [ "$stack_root" = "$(dirname "$(dirname "$stack_claims")")" ]; then
		echo 0
		return
	fi
	mkdir -p "$stack_claims"
	for claim in "$stack_claims"/*; do
		if [ -f "$claim" ] && [ "$(cat "$claim")" = "$stack_root" ]; then
			echo "${claim##*/}"
			return
		fi
	done
	for slot in $(seq "$max_slot"); do
		claim="$stack_claims/$slot"
		if [ -f "$claim" ] && [ ! -d "$(cat "$claim")" ]; then
			rm -f "$claim"
		fi
		if (set -o noclobber && echo "$stack_root" >"$claim") 2>/dev/null; then
			echo "$slot"
			return
		fi
	done
	echo "every local stack slot from 1 to $max_slot is claimed under $stack_claims; run mise run down in a worktree that no longer needs its stack" >&2
	return 1
}

env_value() {
	local line
	line=$(grep -E "^$1=" "$stack_env" | tail -n 1)
	line=${line#*=}
	line=${line#[\'\"]}
	echo "${line%[\'\"]}"
}

shift_ports() {
	local value=$1 shifted="" match pattern='(localhost|127\.0\.0\.1):([0-9]+)'
	while [[ $value =~ $pattern ]]; do
		match=${BASH_REMATCH[0]}
		shifted+="${value%%"$match"*}${BASH_REMATCH[1]}:$((BASH_REMATCH[2] + port_offset))"
		value=${value#*"$match"}
	done
	echo "$shifted$value"
}

LOCO_STACK_SLOT=${LOCO_STACK_SLOT:-$(claim_slot)}
if [[ ! $LOCO_STACK_SLOT =~ ^[0-9]$ ]]; then
	echo "LOCO_STACK_SLOT must be a slot from 0 to $max_slot, got '$LOCO_STACK_SLOT'" >&2
	exit 1
fi

stack_suffix=""
stack_claim=""
if [ "$LOCO_STACK_SLOT" -ne 0 ]; then
	stack_suffix="-$LOCO_STACK_SLOT"
	stack_claim="$stack_claims/$LOCO_STACK_SLOT"
fi
port_offset=$((LOCO_STACK_SLOT * port_stride))

export LOCO_STACK_SLOT
export STACK_DOMAIN="loco-dev$stack_suffix.localhost"
export APPS_PORT=$((8443 + port_offset))
export STACK_STATE="$stack_claims/state/$LOCO_STACK_SLOT"
export LOCO_STACK_CLAIM="$stack_claim"
export KIND_CLUSTER="loco-cluster-local$stack_suffix"
export COMPOSE_PROJECT_NAME="loco-dev$stack_suffix"
export COMPOSE_FILE="$stack_root/dev/compose.local.yaml"
export POSTGRES_PORT=$((5432 + port_offset))
export REGISTRY_PORT=$((5001 + port_offset))
export S3_PORT=$((9000 + port_offset))
export DEX_PORT=$((5556 + port_offset))

app_port=$(env_value APP_PORT)
export APP_PORT="${app_port%:*}:$((${app_port##*:} + port_offset))"
export PUBLIC_API_URL="http://api.$STACK_DOMAIN:${APP_PORT##*:}"
WEB_URL=$(shift_ports "$(env_value WEB_URL)")
export WEB_URL
export WEB_PORT=${WEB_URL##*:}
