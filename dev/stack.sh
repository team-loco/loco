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
export LOCO_STACK_CLAIM="$stack_claim"
export KIND_CLUSTER="loco-cluster-local$stack_suffix"
export COMPOSE_PROJECT_NAME="loco-dev$stack_suffix"
export COMPOSE_FILE="$stack_root/dev/compose.yaml"
export REGISTRY_ALIAS="loco-registry$stack_suffix"
export S3_ALIAS="loco-s3$stack_suffix.localhost"

export POSTGRES_PORT=$((5432 + port_offset))
export VALKEY_PORT=$((6379 + port_offset))
export REGISTRY_PORT=$((5001 + port_offset))
export S3_PORT=$((9000 + port_offset))
export DEX_PORT=$((5556 + port_offset))
export TILT_PORT=$((10350 + port_offset))
export OBS_PROXY_PORT=$((8050 + port_offset))
export CLICKHOUSE_NATIVE_PORT=$((19050 + port_offset))
export OBS_OTLP_GRPC_PORT=$((4317 + port_offset))
export OBS_OTLP_HTTP_PORT=$((4318 + port_offset))
export CLICKHOUSE_CONTAINER="$COMPOSE_PROJECT_NAME-clickhouse"
export OBSERVABILITY_PROXY_URL="http://127.0.0.1:$OBS_PROXY_PORT"

app_port=$(env_value APP_PORT)
export APP_PORT="${app_port%:*}:$((${app_port##*:} + port_offset))"
export PUBLIC_API_URL="http://localhost:${APP_PORT##*:}"
WEB_URL=$(shift_ports "$(env_value WEB_URL)")
export WEB_URL
export WEB_PORT=${WEB_URL##*:}

for name in DATABASE_URL CACHE_ADDR CORS_ALLOWED_ORIGINS AUTH_ISSUERS LOCO_REGISTRY_URL LOCO_SECRETS_TRANSIT_ADDR; do
	export "$name=$(shift_ports "$(env_value "$name")")"
done
bucket_endpoint=$(shift_ports "$(env_value LOCO_SOURCE_BUCKET_ENDPOINT)")
export LOCO_SOURCE_BUCKET_ENDPOINT="${bucket_endpoint/loco-s3.localhost/$S3_ALIAS}"
registry_host=$(env_value LOCO_REGISTRY_HOST)
export LOCO_REGISTRY_HOST="${registry_host/#loco-registry:/$REGISTRY_ALIAS:}"
export OPENBAO_PORT=${LOCO_SECRETS_TRANSIT_ADDR##*:}
OPENBAO_IMAGE=$(yq '.openbao.image' "$stack_root/charts/loco-control-plane/values.yaml")
export OPENBAO_IMAGE
