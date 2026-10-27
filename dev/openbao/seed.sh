#!/bin/sh
set -eu

root_token_file=/openbao/file/root-token
ready_attempts=60
policy=loco-api
token_period=24h

: "${LOCO_SECRETS_TRANSIT_MOUNT:?}" "${LOCO_SECRETS_TRANSIT_KEY:?}" "${LOCO_SECRETS_TRANSIT_TOKEN:?}"
mount=$LOCO_SECRETS_TRANSIT_MOUNT
key=$LOCO_SECRETS_TRANSIT_KEY

wait_unsealed() {
	attempt=0
	until bao status >/dev/null 2>&1; do
		attempt=$((attempt + 1))
		if [ "$attempt" -ge "$ready_attempts" ]; then
			echo "OpenBao at $BAO_ADDR did not unseal" >&2
			exit 1
		fi
		sleep 1
	done
}

if [ "$(bao read -field=initialized sys/init)" != true ]; then
	bao operator init -recovery-shares=1 -recovery-threshold=1 -format=json |
		sed -n 's/^ *"root_token": "\([^"]*\)".*/\1/p' >"$root_token_file"
fi
BAO_TOKEN=$(cat "$root_token_file")
export BAO_TOKEN
wait_unsealed

if [ "$(bao read -field=type "sys/mounts/$mount" 2>/dev/null || true)" != transit ]; then
	bao secrets enable -path="$mount" transit >/dev/null
fi
if ! bao read "$mount/keys/$key" >/dev/null 2>&1; then
	bao write -f "$mount/keys/$key" type=aes256-gcm96 derived=true >/dev/null
fi
bao policy write "$policy" - >/dev/null <<EOF
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
if ! bao token lookup "$LOCO_SECRETS_TRANSIT_TOKEN" >/dev/null 2>&1; then
	bao token create -id="$LOCO_SECRETS_TRANSIT_TOKEN" -orphan -policy="$policy" -period="$token_period" >/dev/null
fi
echo "OpenBao transit key $mount/$key and the $policy token are ready"
