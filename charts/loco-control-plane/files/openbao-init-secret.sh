#!/bin/sh
set -eu

request_timeout=10
store_deadline=3600
store_retry=5

: "${INIT_SECRET:?}" "${NAMESPACE:?}" "${INIT_SECRET_PATCH:?}"

account=/var/run/secrets/kubernetes.io/serviceaccount
secrets=https://kubernetes.default.svc/api/v1/namespaces/$NAMESPACE/secrets
response=$INIT_SECRET_PATCH.response

fail() {
	echo "$*" >&2
	exit 1
}

request() {
	rm -f "$response"
	curl --silent --show-error --max-time "$request_timeout" --output "$response" --write-out '%{http_code}' \
		--cacert "$account/ca.crt" -H "Authorization: Bearer $(cat "$account/token")" \
		-X "$1" -H "Content-Type: $2" --data-binary "$3" "$4" || true
}

response_body() {
	cat "$response" 2>/dev/null || true
}

prepare() {
	status=$(request POST application/json \
		"{\"apiVersion\":\"v1\",\"kind\":\"Secret\",\"metadata\":{\"name\":\"$INIT_SECRET\"},\"type\":\"Opaque\"}" "$secrets")
	[ "$status" = 201 ] || [ "$status" = 409 ] ||
		fail "could not create the Secret $INIT_SECRET (HTTP $status): $(response_body)"
	status=$(request PATCH application/merge-patch+json '{}' "$secrets/$INIT_SECRET")
	[ "$status" = 200 ] || fail "could not patch the Secret $INIT_SECRET (HTTP $status): $(response_body)"
	echo "The Secret $INIT_SECRET is writable"
}

store() {
	[ -s "$INIT_SECRET_PATCH" ] || return 0
	deadline=$(($(date +%s) + store_deadline))
	while status=$(request PATCH application/merge-patch+json "@$INIT_SECRET_PATCH" "$secrets/$INIT_SECRET"); [ "$status" != 200 ]; do
		echo "could not write the init output to the Secret $INIT_SECRET (HTTP $status): $(response_body)" >&2
		[ "$(date +%s)" -lt "$deadline" ] ||
			fail "gave up writing the init output to the Secret $INIT_SECRET after ${store_deadline}s"
		sleep "$store_retry"
	done
	rm -f "$INIT_SECRET_PATCH" "$response"
	echo "Wrote the init output to the Secret $INIT_SECRET"
}

[ "$#" -gt 0 ] || fail "usage: $0 prepare|store..."
for phase in "$@"; do
	case "$phase" in
	prepare) prepare ;;
	store) store ;;
	*) fail "unknown phase $phase" ;;
	esac
done
