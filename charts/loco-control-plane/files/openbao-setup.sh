#!/bin/sh
set -eu

ready_attempts=60
api_policy=loco-api
api_token_period=24h
api_login_ttl=1h
setup_policy=loco-setup
generate_root_policy=loco-generate-root
generate_root_ttl=87600h
setup_login_ttl=15m
snapshot_policy=loco-snapshot
snapshot_login_ttl=15m

: "${BAO_ADDR:?}" "${OPENBAO_INIT_FILE:?}"

fail() {
	echo "$*" >&2
	exit 1
}

wait_reachable() {
	attempt=0
	until [ "$(bao read -field=initialized sys/init 2>/dev/null || true)" != "" ]; do
		attempt=$((attempt + 1))
		[ "$attempt" -lt "$ready_attempts" ] || fail "OpenBao at $BAO_ADDR did not answer"
		sleep 1
	done
}

wait_unsealed() {
	attempt=0
	until bao status >/dev/null 2>&1; do
		attempt=$((attempt + 1))
		[ "$attempt" -lt "$ready_attempts" ] || fail "OpenBao at $BAO_ADDR did not unseal"
		sleep 1
	done
}

init() {
	: "${OPENBAO_RECOVERY_SHARES:?}" "${OPENBAO_RECOVERY_THRESHOLD:?}"
	previous=${OPENBAO_PREVIOUS_INIT:-$OPENBAO_INIT_FILE}
	wait_reachable
	if [ "$(bao read -field=initialized sys/init)" != true ]; then
		[ ! -s "$previous" ] ||
			fail "OpenBao at $BAO_ADDR is not initialized, but $previous holds the init output of an earlier one; restore its snapshot or remove that output"
		umask 077
		bao operator init -recovery-shares="$OPENBAO_RECOVERY_SHARES" \
			-recovery-threshold="$OPENBAO_RECOVERY_THRESHOLD" -format=json >"$OPENBAO_INIT_FILE.new"
		mv "$OPENBAO_INIT_FILE.new" "$OPENBAO_INIT_FILE"
		echo "Initialized OpenBao at $BAO_ADDR"
	fi
	if [ -n "${OPENBAO_GENERATE_ROOT_TOKEN_FILE:-}" ] && [ ! -s "$OPENBAO_GENERATE_ROOT_TOKEN_FILE" ]; then
		wait_unsealed
		root=$(root_token)
		[ -z "$root" ] || issue_generate_root_token "$root"
	fi
}

issue_generate_root_token() {
	export BAO_TOKEN="$1"
	bao policy write "$generate_root_policy" - >/dev/null <<EOF
path "sys/generate-root-token/*" {
  capabilities = ["create", "read", "update", "delete"]
}
EOF
	bao auth tune -max-lease-ttl="$generate_root_ttl" token/ >/dev/null
	bao write "auth/token/roles/$generate_root_policy" allowed_policies="$generate_root_policy" orphan=true \
		renewable=false token_explicit_max_ttl="$generate_root_ttl" token_no_default_policy=true >/dev/null
	umask 077
	bao token create -role="$generate_root_policy" -ttl="$generate_root_ttl" -field=token >"$OPENBAO_GENERATE_ROOT_TOKEN_FILE"
	unset BAO_TOKEN
}

root_token() {
	[ -s "$OPENBAO_INIT_FILE" ] || return 0
	token=$(sed -n 's/^ *"root_token": "\([^"]*\)".*/\1/p' "$OPENBAO_INIT_FILE")
	if [ -n "$token" ] && BAO_TOKEN=$token bao token lookup >/dev/null 2>&1; then
		echo "$token"
	fi
}

login() {
	: "${OPENBAO_TOKEN_FILE:?}"
	bao write -field=token "auth/$OPENBAO_KUBERNETES_MOUNT/login" role="$OPENBAO_SETUP_ROLE" jwt=@"$OPENBAO_TOKEN_FILE"
}

enable_kubernetes_auth() {
	auth=$OPENBAO_KUBERNETES_MOUNT
	if [ "$(bao read -field=type "sys/auth/$auth" 2>/dev/null || true)" != kubernetes ]; then
		bao auth enable -path="$auth" kubernetes >/dev/null
	fi
	bao write "auth/$auth/config" kubernetes_host="$OPENBAO_KUBERNETES_HOST" >/dev/null
}

write_role() {
	bao write "auth/$OPENBAO_KUBERNETES_MOUNT/role/$1" bound_service_account_names="$2" \
		bound_service_account_namespaces="$OPENBAO_NAMESPACE" token_policies="$3" token_no_default_policy="$4" \
		token_ttl="$5" token_max_ttl="$5" >/dev/null
}

write_setup_role() {
	: "${OPENBAO_SETUP_SERVICE_ACCOUNT:?}"
	bao policy write "$setup_policy" - >/dev/null <<EOF
path "sys/mounts/$mount" {
  capabilities = ["create", "read", "update"]
}
path "$mount/keys/$key" {
  capabilities = ["create", "read", "update"]
}
path "sys/policies/acl/$api_policy" {
  capabilities = ["create", "read", "update"]
}
path "sys/policies/acl/$setup_policy" {
  capabilities = ["create", "read", "update"]
}
path "sys/policies/acl/$snapshot_policy" {
  capabilities = ["create", "read", "update"]
}
path "sys/auth/$OPENBAO_KUBERNETES_MOUNT" {
  capabilities = ["create", "read", "update", "sudo"]
}
path "auth/$OPENBAO_KUBERNETES_MOUNT/config" {
  capabilities = ["create", "read", "update"]
}
path "auth/$OPENBAO_KUBERNETES_MOUNT/role/*" {
  capabilities = ["create", "read", "update"]
}
EOF
	write_role "$OPENBAO_SETUP_ROLE" "$OPENBAO_SETUP_SERVICE_ACCOUNT" "$setup_policy" true "$setup_login_ttl"
}

write_api_policy() {
	bao policy write "$api_policy" - >/dev/null <<EOF
path "$mount/encrypt/$key" {
  capabilities = ["update"]
}
path "$mount/decrypt/$key" {
  capabilities = ["update"]
}
path "$mount/keys/$key" {
  capabilities = ["read"]
}
EOF
}

configure() {
	: "${LOCO_SECRETS_TRANSIT_MOUNT:?}" "${LOCO_SECRETS_TRANSIT_KEY:?}"
	mount=$LOCO_SECRETS_TRANSIT_MOUNT
	key=$LOCO_SECRETS_TRANSIT_KEY
	kubernetes=${OPENBAO_KUBERNETES_HOST:-}
	[ -z "${OPENBAO_SETUP_ROLE:-}" ] || [ -n "$kubernetes" ] || fail "OPENBAO_SETUP_ROLE needs OPENBAO_KUBERNETES_HOST"
	if [ -n "$kubernetes" ]; then
		: "${OPENBAO_KUBERNETES_MOUNT:?}" "${OPENBAO_API_ROLE:?}" "${OPENBAO_API_SERVICE_ACCOUNT:?}" "${OPENBAO_NAMESPACE:?}"
	fi
	wait_unsealed
	root=$(root_token)
	if [ -n "$root" ]; then
		export BAO_TOKEN="$root"
		write_api_policy
		if [ -n "${LOCO_SECRETS_TRANSIT_TOKEN:-}" ] && ! bao token lookup "$LOCO_SECRETS_TRANSIT_TOKEN" >/dev/null 2>&1; then
			bao token create -id="$LOCO_SECRETS_TRANSIT_TOKEN" -orphan -policy="$api_policy" -period="$api_token_period" >/dev/null
		fi
		if [ -n "${OPENBAO_SETUP_ROLE:-}" ]; then
			enable_kubernetes_auth
			write_setup_role
			BAO_TOKEN=$(login)
			BAO_TOKEN=$root bao token revoke -self >/dev/null
			echo "Revoked the root token; later runs log in as $OPENBAO_SETUP_ROLE"
		fi
	elif [ -n "${OPENBAO_SETUP_ROLE:-}" ]; then
		if ! BAO_TOKEN=$(login); then
			[ -s "${OPENBAO_PREVIOUS_INIT:?}" ] ||
				fail "OpenBao at $BAO_ADDR is initialized, but the Secret ${INIT_SECRET:?} holds no init output and the $OPENBAO_SETUP_ROLE role cannot log in; recover it as \"Lost init output\" in docs/content/operations/secrets-key-provider.md describes"
			fail "the $OPENBAO_SETUP_ROLE role cannot log in to OpenBao at $BAO_ADDR"
		fi
		export BAO_TOKEN
	else
		fail "$OPENBAO_INIT_FILE holds no valid root token"
	fi

	if [ "$(bao read -field=type "sys/mounts/$mount" 2>/dev/null || true)" != transit ]; then
		bao secrets enable -path="$mount" transit >/dev/null
	fi
	if ! bao read "$mount/keys/$key" >/dev/null 2>&1; then
		bao write -f "$mount/keys/$key" type=aes256-gcm96 derived=true >/dev/null
	fi
	write_api_policy
	if [ -n "$kubernetes" ]; then
		enable_kubernetes_auth
		[ -z "${OPENBAO_SETUP_ROLE:-}" ] || write_setup_role
		write_role "$OPENBAO_API_ROLE" "$OPENBAO_API_SERVICE_ACCOUNT" "$api_policy" false "$api_login_ttl"
		if [ -n "${OPENBAO_SNAPSHOT_ROLE:-}" ]; then
			: "${OPENBAO_SNAPSHOT_SERVICE_ACCOUNT:?}"
			bao policy write "$snapshot_policy" - >/dev/null <<EOF
path "sys/storage/raft/snapshot" {
  capabilities = ["read"]
}
EOF
			write_role "$OPENBAO_SNAPSHOT_ROLE" "$OPENBAO_SNAPSHOT_SERVICE_ACCOUNT" "$snapshot_policy" true "$snapshot_login_ttl"
		fi
	fi
	echo "OpenBao transit key $mount/$key and the $api_policy policy are ready"
}

[ "$#" -gt 0 ] || fail "usage: $0 init|configure..."
for phase in "$@"; do
	case "$phase" in
	init) init ;;
	configure) configure ;;
	*) fail "unknown phase $phase" ;;
	esac
done
