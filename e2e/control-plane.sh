#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
export ROOT_DIR
RUN_HASH=$(printf '%s' "$ROOT_DIR" | cksum | awk '{print $1}')
CLUSTER_NAME="loco-cp-${E2E_RUN_ID:-$RUN_HASH}"
RUN_DIR="$ROOT_DIR/e2e/runs/$CLUSTER_NAME"
NAMESPACE=loco-control-plane
TIMEOUT=300s
KEEP=false
SKIP_BUILD=false
for option in "$@"; do
    case "$option" in
        --no-teardown) KEEP=true ;;
        --skip-build) SKIP_BUILD=true ;;
        *) printf 'Unknown option: %s\n' "$option" >&2; exit 2 ;;
    esac
done
mkdir -p "$RUN_DIR"
export KUBECONFIG="$RUN_DIR/kubeconfig"
PORT_FORWARD_PID=""

cleanup() {
    local result=$?
    if [[ -n $PORT_FORWARD_PID ]]; then
        kill "$PORT_FORWARD_PID" 2>/dev/null || true
    fi
    if [[ $result -ne 0 ]]; then
        kubectl -n "$NAMESPACE" get pods,events -o wide >"$RUN_DIR/diagnostics.txt" 2>&1 || true
        kubectl -n "$NAMESPACE" logs -l app=regional-api --all-pods=true --tail=200 >>"$RUN_DIR/diagnostics.txt" 2>&1 || true
        kubectl -n "$NAMESPACE" logs deployment/regional-obs-proxy --tail=200 >>"$RUN_DIR/diagnostics.txt" 2>&1 || true
    fi
    if [[ $KEEP == false ]]; then
        kind delete cluster --name "$CLUSTER_NAME"
    fi
    exit "$result"
}
trap cleanup EXIT

if [[ $SKIP_BUILD == false ]]; then
    docker build -t loco-api:e2e --build-arg VERSION=e2e -f "$ROOT_DIR/api/Dockerfile" "$ROOT_DIR"
    docker build -t loco-obs-proxy:e2e --build-arg VERSION=e2e -f "$ROOT_DIR/observability-proxy/Dockerfile" "$ROOT_DIR"
fi
if ! kind get clusters | rg -x "$CLUSTER_NAME" >/dev/null; then
    kind create cluster --name "$CLUSTER_NAME" --kubeconfig "$KUBECONFIG" --wait "$TIMEOUT"
fi
kind load docker-image --name "$CLUSTER_NAME" loco-api:e2e loco-obs-proxy:e2e
GATEWAY_VERSION=$(yq '.dependencies[] | select(.name == "gateway-helm") | .version' "$ROOT_DIR/charts/loco-core/Chart.yaml")
helm show crds "$ROOT_DIR/charts/loco-core/charts/gateway-helm-$GATEWAY_VERSION.tgz" | kubectl apply --server-side -f -
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -
umask 077
if [[ ! -s "$RUN_DIR/password" ]]; then
    openssl rand -hex 24 | tr -d '\n' >"$RUN_DIR/password"
fi
kubectl -n "$NAMESPACE" create secret generic fixture-password --from-file=password="$RUN_DIR/password" --dry-run=client -o yaml | kubectl apply -f -
yq -n '.postgresImage = load(strenv(ROOT_DIR) + "/compose.yaml").services.postgres.image |
    .clickhouseImage = (load(strenv(ROOT_DIR) + "/charts/loco-obs/values.yaml").clickhouse.clickhouse.image | .repository + ":" + .tag) |
    .keeperConfig = load(strenv(ROOT_DIR) + "/charts/loco-obs/values.yaml").clickhouse.clickhouse.keeperConfig' >"$RUN_DIR/fixtures.yaml"
helm upgrade --install fixtures "$ROOT_DIR/e2e/fixtures/control-plane" --namespace "$NAMESPACE" \
    -f "$RUN_DIR/fixtures.yaml" --wait --timeout "$TIMEOUT"
python3 - "$RUN_DIR" <<'PY'
import datetime
import json
import pathlib
import sys
root = pathlib.Path(sys.argv[1])
password = (root / 'password').read_text().strip()
(root / 'api.env').write_text(f'DATABASE_URL=postgresql://loco:{password}@postgres:5432/loco?sslmode=disable\nLOCO_AGENT_CONNECTION_SECRETS={password}\nLOCO_IMAGE_RETENTION=0\n')
(root / 'reader').write_text(f'clickhouse://loco:{password}@clickhouse:9000/loco_obs')
now = datetime.datetime.now(datetime.UTC)
(root / 'query.json').write_text(json.dumps({'workspaceId': '00000000-0000-0000-0000-000000000001', 'startTime': (now - datetime.timedelta(minutes=5)).isoformat(), 'endTime': now.isoformat()}))
PY
kubectl -n "$NAMESPACE" create secret generic api-env --from-env-file="$RUN_DIR/api.env" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$NAMESPACE" create secret generic clickhouse-reader --from-file=url="$RUN_DIR/reader" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$NAMESPACE" create secret generic clickhouse-migrator --from-file=url="$RUN_DIR/reader" --dry-run=client -o yaml | kubectl apply -f -
helm upgrade --install regional "$ROOT_DIR/charts/loco-control-plane" --namespace "$NAMESPACE" \
    --set api.image.repository=loco-api --set api.image.tag=e2e --set api.imagePullPolicy=Never \
    --set api.existingSecret=api-env --set api.replicaCount=2 \
    --set obsProxy.image.repository=loco-obs-proxy --set obsProxy.image.tag=e2e --set obsProxy.imagePullPolicy=Never \
    --set obsProxy.clickhouse.existingSecret.name=clickhouse-reader \
    --set obsProxy.clickhouse.migratorExistingSecret.name=clickhouse-migrator \
    --wait --timeout "$TIMEOUT"
kubectl -n "$NAMESPACE" port-forward service/regional-api :80 >"$RUN_DIR/port-forward.log" 2>&1 &
PORT_FORWARD_PID=$!
for attempt in $(seq 1 30); do
    if rg 'Forwarding from 127.0.0.1:' "$RUN_DIR/port-forward.log" >/dev/null; then break; fi
    sleep 1
done
API_PORT=$(sed -n 's/Forwarding from 127.0.0.1:\([0-9]*\) .*/\1/p' "$RUN_DIR/port-forward.log" | head -1)
curl --fail --silent "http://127.0.0.1:$API_PORT/health"
curl --fail --silent -H 'Content-Type: application/json' -d '{}' \
    "http://127.0.0.1:$API_PORT/loco.config.v1.ConfigService/GetConfig" >"$RUN_DIR/config.json"
curl --silent -H 'Content-Type: application/json' -H 'Authorization: Bearer invalid-e2e-token' \
    --data-binary @"$RUN_DIR/query.json" \
    "http://127.0.0.1:$API_PORT/loco.observability.v1.ObservabilityProxyService/QueryLogs" >"$RUN_DIR/observability.json"
python3 - "$RUN_DIR" <<'PY'
import json
import pathlib
import sys
root = pathlib.Path(sys.argv[1])
config = json.loads((root / 'config.json').read_text())
assert 'serviceDefaults' in config, config
response = json.loads((root / 'observability.json').read_text())
assert response.get('code') == 'permission_denied', response
PY
kubectl -n "$NAMESPACE" exec deployment/postgres -- psql -U loco -d loco -Atc 'SELECT count(*) FROM goose_db_version' >"$RUN_DIR/postgres-migrations.txt"
printf 'Two API replicas started on an empty database; private proxy is ready and its API permission check passed.\n'
