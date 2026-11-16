#!/bin/sh
set -eu

account=/var/run/secrets/kubernetes.io/serviceaccount
endpoint=https://kubernetes.default.svc/api/v1/namespaces/$NAMESPACE/secrets
response=/work/$SECRET_NAME
trap 'rm -f "$response"' EXIT

request() {
 curl --silent --show-error --max-time "$REQUEST_TIMEOUT" --output "$response" --write-out '%{http_code}' \
  --cacert "$account/ca.crt" -H "Authorization: Bearer $(cat "$account/token")" \
  -H 'Content-Type: application/json' "$@"
}

status=$(request "$endpoint/$SECRET_NAME")
case "$status" in
 200) exit 0 ;;
 404) ;;
 *) echo "read credential Secret failed (HTTP $status)" >&2; exit 1 ;;
esac

password=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')
[ "${#password}" -eq 64 ] || { echo "could not generate a credential" >&2; exit 1; }
if [ -n "$SERVER" ]; then
 data="{\"password\":\"$password\",\"url\":\"clickhouse://$URL_USERNAME:$password@$SERVER:9000\"}"
else
 data="{\"username\":$USERNAME_JSON,\"password\":\"$password\"}"
fi
status=$(request -X POST --data-binary \
 "{\"apiVersion\":\"v1\",\"kind\":\"Secret\",\"metadata\":{\"name\":\"$SECRET_NAME\"},\"type\":\"Opaque\",\"stringData\":$data}" "$endpoint")
case "$status" in
 201|409) ;;
 *) echo "create credential Secret failed (HTTP $status)" >&2; exit 1 ;;
esac
rm -f "$response"
