#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
export ROOT_DIR
RUN_HASH=$(printf '%s' "$ROOT_DIR" | cksum | awk '{print $1}')
CLUSTER_NAME="loco-cp-${E2E_RUN_ID:-$RUN_HASH}"
RUN_DIR="$ROOT_DIR/e2e/runs/$CLUSTER_NAME"
NAMESPACE=loco-control-plane
GATEWAY_HTTP_NODE_PORT=30080
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
    docker build -t loco-api-failover:e2e --build-arg VERSION=e2e -f "$ROOT_DIR/e2e/fixtures/api-failover/Dockerfile" "$ROOT_DIR"
    docker build -t loco-api:e2e --build-arg VERSION=e2e -f "$ROOT_DIR/api/Dockerfile" "$ROOT_DIR"
    docker build -t loco-ui:e2e --build-arg VERSION=e2e --build-arg PUBLIC_API_URL=https://api.loco.build --build-arg PUBLIC_APP_ENV=PRODUCTION -f "$ROOT_DIR/web/Dockerfile" "$ROOT_DIR"
    docker build -t loco-obs-proxy:e2e --build-arg VERSION=e2e -f "$ROOT_DIR/observability-proxy/Dockerfile" "$ROOT_DIR"
fi
if ! kind get clusters | rg -x "$CLUSTER_NAME" >/dev/null; then
    kind create cluster --name "$CLUSTER_NAME" --kubeconfig "$KUBECONFIG" --wait "$TIMEOUT"
fi
kind load docker-image --name "$CLUSTER_NAME" loco-api:e2e loco-obs-proxy:e2e loco-ui:e2e loco-api-failover:e2e
GATEWAY_VERSION=$(yq '.dependencies[] | select(.name == "gateway-helm") | .version' "$ROOT_DIR/charts/loco-core/Chart.yaml")
helm upgrade --install eg "$ROOT_DIR/charts/loco-core/charts/gateway-helm-$GATEWAY_VERSION.tgz" \
    --namespace "$NAMESPACE" --create-namespace --wait --timeout "$TIMEOUT"
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -
umask 077
if [[ ! -s "$RUN_DIR/password" ]]; then
    openssl rand -hex 24 | tr -d '\n' >"$RUN_DIR/password"
fi
kubectl -n "$NAMESPACE" create secret generic fixture-password --from-file=password="$RUN_DIR/password" --dry-run=client -o yaml | kubectl apply -f -
yq -n '.postgresImage = load(strenv(ROOT_DIR) + "/compose.yaml").services.postgres.image |
    .clickhouseImage = (load(strenv(ROOT_DIR) + "/charts/loco-obs/values.yaml").clickhouse.clickhouse.image | .repository + ":" + .tag) |
    .keeperConfig = load(strenv(ROOT_DIR) + "/charts/loco-obs/values.yaml").clickhouse.clickhouse.keeperConfig' >"$RUN_DIR/fixtures.yaml"
helm template gateway "$ROOT_DIR/charts/loco-core" --namespace "$NAMESPACE" \
    -f "$ROOT_DIR/e2e/gateway-values.yaml" --set gateway-helm.enabled=false \
    --set-json 'envoyProxy.service={"type":"NodePort","externalTrafficPolicy":"Cluster","patch":{"type":"StrategicMerge","value":{"spec":{"ports":[{"port":80,"nodePort":'"$GATEWAY_HTTP_NODE_PORT"'}]}}}}' \
    --show-only templates/gateway.yaml | yq 'select(.kind == "EnvoyProxy")' | kubectl -n "$NAMESPACE" apply -f -
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
    --set api.existingSecret=api-env --set api.replicaCount=2 --set 'api.platformDomains={apps.e2e.test.local}' \
    --set gatewayNamespace="$NAMESPACE" --set controlPlaneHostname=api.e2e.test.local \
    --set ui.hostname=app.e2e.test.local --set ui.image.repository=loco-ui --set ui.image.tag=e2e --set ui.imagePullPolicy=Never \
    --set obsProxy.image.repository=loco-obs-proxy --set obsProxy.image.tag=e2e --set obsProxy.imagePullPolicy=Never \
    --set obsProxy.clickhouse.existingSecret.name=clickhouse-reader \
    --set obsProxy.clickhouse.migratorExistingSecret.name=clickhouse-migrator \
    --wait --timeout "$TIMEOUT"
ENVOY_DEPLOYMENT=""
for _attempt in $(seq 1 60); do
    ENVOY_DEPLOYMENT=$(kubectl -n "$NAMESPACE" get deployments -l gateway.envoyproxy.io/owning-gateway-name=eg -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    if [[ -n $ENVOY_DEPLOYMENT ]]; then break; fi
    sleep 1
done
kubectl -n "$NAMESPACE" rollout status "deployment/$ENVOY_DEPLOYMENT" --timeout "$TIMEOUT"
ENVOY_SERVICE=$(kubectl -n "$NAMESPACE" get services -l gateway.envoyproxy.io/owning-gateway-name=eg -o jsonpath='{.items[0].metadata.name}')
NODE_PORT=$(kubectl -n "$NAMESPACE" get "service/$ENVOY_SERVICE" -o jsonpath='{.spec.ports[?(@.port==80)].nodePort}')
if [[ $NODE_PORT != "$GATEWAY_HTTP_NODE_PORT" ]]; then
    printf 'Expected chart-configured HTTP NodePort %s, got %s\n' "$GATEWAY_HTTP_NODE_PORT" "$NODE_PORT" >&2
    exit 1
fi
kubectl -n "$NAMESPACE" port-forward "service/$ENVOY_SERVICE" :80 >"$RUN_DIR/port-forward.log" 2>&1 &
PORT_FORWARD_PID=$!
for _attempt in $(seq 1 30); do
    if rg 'Forwarding from 127.0.0.1:' "$RUN_DIR/port-forward.log" >/dev/null; then break; fi
    sleep 1
done
API_PORT=$(sed -n 's/Forwarding from 127.0.0.1:\([0-9]*\) .*/\1/p' "$RUN_DIR/port-forward.log" | head -1)
curl --fail --silent -H "Host: api.e2e.test.local" "http://127.0.0.1:$API_PORT/health"
curl --fail --silent -H "Host: app.e2e.test.local" "http://127.0.0.1:$API_PORT/" >"$RUN_DIR/dashboard.html"
curl --fail --silent --compressed -D "$RUN_DIR/runtime-headers.txt" -H "Host: app.e2e.test.local" "http://127.0.0.1:$API_PORT/runtime-config.js" >"$RUN_DIR/runtime-config.js"
curl --fail --silent -H 'Host: app.e2e.test.local' -H 'Content-Type: application/json' -d '{}' \
    "http://127.0.0.1:$API_PORT/api/loco.config.v1.ConfigService/GetConfig" >"$RUN_DIR/config.json"
curl --silent -H 'Host: app.e2e.test.local' -H 'Content-Type: application/json' -H 'Authorization: Bearer invalid-e2e-token' \
    --data-binary @"$RUN_DIR/query.json" \
    "http://127.0.0.1:$API_PORT/api/loco.observability.v1.ObservabilityProxyService/QueryLogs" >"$RUN_DIR/observability.json"
python3 - "$RUN_DIR" <<'PY'
import json
import pathlib
import sys
root = pathlib.Path(sys.argv[1])
config = json.loads((root / 'config.json').read_text())
assert config['serviceDefaults']['platformDomain'] == 'apps.e2e.test.local', config
html = (root / 'dashboard.html').read_text()
assert '<script src="/runtime-config.js"></script>' in html, html
assert html.index('/runtime-config.js') < html.index('type="module"'), html
assert (root / 'runtime-config.js').read_text().strip() == 'window.locoConfig = {"apiURL":"/api"};'
assert 'immutable' not in (root / 'runtime-headers.txt').read_text().lower()
response = json.loads((root / 'observability.json').read_text())
assert response.get('code') == 'permission_denied', response
PY
kubectl -n "$NAMESPACE" exec deployment/postgres -- psql -U loco -d loco -Atc 'SELECT count(*) FROM goose_db_version' >"$RUN_DIR/postgres-migrations.txt"
printf 'Two API replicas started on an empty database; dashboard and same-origin API routing work; private proxy permission check passed.\n'

python3 "$ROOT_DIR/e2e/api-failover.py" --namespace "$NAMESPACE" --gateway-namespace "$NAMESPACE" \
    --gateway-url "http://127.0.0.1:$API_PORT" \
    --hostname-suffix "$(yq '.global.domain.platform' "$ROOT_DIR/e2e/gateway-values.yaml")" --image loco-api-failover:e2e --output "$RUN_DIR/api-failover"
