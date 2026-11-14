#!/usr/bin/env bash
# E2E test orchestrator for Loco
# Usage: ./tests/e2e/run.sh [--no-teardown] [--skip-build] [--build-only] [--teardown-only] [--builds-disabled] [--observability] [--registry-token] [test-filter...]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

ROOT_HASH=$(cksum <<<"$ROOT_DIR" | awk '{print $1}')
DEFAULT_RUN_ID=$(printf '%08x' "$ROOT_HASH")
RUN_ID="${E2E_RUN_ID:-$DEFAULT_RUN_ID}"
RUN_ID_PATTERN='^[a-z0-9]([a-z0-9-]{0,14}[a-z0-9])?$'
if [[ ! $RUN_ID =~ $RUN_ID_PATTERN ]]; then
    echo "E2E_RUN_ID must be 1-16 lowercase letters, digits or inner hyphens, got '${RUN_ID}'" >&2
    exit 1
fi
RUN_NAME="loco-e2e-${RUN_ID}"
RUN_DIR="$SCRIPT_DIR/runs/$RUN_ID"
BIN_DIR="$RUN_DIR/bin"
LOG_DIR="$RUN_DIR/logs"
PID_DIR="$RUN_DIR/pids"
AGENT_EXPECTED_ERRORS="$LOG_DIR/agent-expected-errors.log"
JOB_PID_DIR="$PID_DIR/setup-$$"
KUBECONFIG_FILE="$RUN_DIR/kubeconfig"

PORT_RANGE_START=20000
PORT_BLOCKS=1000
PORT_BLOCK_SIZE=10
RUN_HASH=$(cksum <<<"$RUN_ID" | awk '{print $1}')
PORT_BASE=$((PORT_RANGE_START + RUN_HASH % PORT_BLOCKS * PORT_BLOCK_SIZE))
API_PORT=$PORT_BASE
API_REPLICA_PORT=$((PORT_BASE + 5))
AUTH_PORT=$((PORT_BASE + 9))
OTLP_GRPC_PORT=$((PORT_BASE + 6))
OTLP_HTTP_PORT=$((PORT_BASE + 7))
OBS_PROXY_PORT=$((PORT_BASE + 1))
S3_PORT=$((PORT_BASE + 2))
CLICKHOUSE_PORT=$((PORT_BASE + 3))
GATEWAY_PORT=$((PORT_BASE + 4))
REGISTRY_HOST_PORT=$((PORT_BASE + 8))
READY_TIMEOUT=120
OBS_READY_TIMEOUT=600
HELM_TIMEOUT=10m
CLICKHOUSE_NATIVE_PORT=9000
GATEWAY_HTTP_PORT=80
LOG_TAIL_LINES=200
BUILD_NICENESS=10

KIND_CLUSTER_NAME="$RUN_NAME"
KUBE_CONTEXT="kind-${KIND_CLUSTER_NAME}"
PG_USER="loco_e2e"
PG_PASS="loco_e2e_pass"
PG_DB="loco_e2e"
E2E_VERSION="e2e"
CONTROLLER_REPOSITORY="loco-controller"
BUILDER_REPOSITORY="loco-builder"
CONTROL_PLANE_VALUES="$ROOT_DIR/charts/loco-control-plane/values.yaml"
OBS_E2E_VALUES="$SCRIPT_DIR/obs-values.yaml"
WORKER_RELEASE="loco-worker"
WORKER_CHART="$ROOT_DIR/charts/loco-worker"
WORKER_VALUES="$WORKER_CHART/values.yaml"
WORKER_E2E_VALUES="$SCRIPT_DIR/worker-values.yaml"
# keep in sync with loco-worker.gateway in charts/loco-worker/templates/_helpers.tpl
GATEWAY_NAME="eg"
E2E_WORKSPACE_PREFIX=$(yq .global.workspaceNamespacePrefix "$WORKER_VALUES")
export E2E_WORKSPACE_PREFIX
OBS_NAMESPACE=$(yq '.["otel-col-deploy"].namespaceOverride' "$WORKER_VALUES")
OBS_NODE_NAMESPACE=$(yq '.["otel-col-daemon"].namespaceOverride' "$WORKER_VALUES")
PLATFORM_DOMAIN="e2e.test.local"
GATEWAY_TLS_SECRET="e2e-gateway-tls"
GATEWAY_TLS_DAYS=1
CLICKHOUSE_IMAGE=$(yq '.clickhouse.clickhouse.image.repository + ":" + .clickhouse.clickhouse.image.tag' "$CONTROL_PLANE_VALUES")
CLICKHOUSE_CONTAINER="${RUN_NAME}-clickhouse"
OBS_PROXY_REPOSITORY="loco-obs-proxy"
INGEST_CONTAINER="${RUN_NAME}-obs-ingest"
REALM_CONTAINER="${RUN_NAME}-realm"
INGEST_OTLP_HTTP_PORT=$(yq '.obsProxy.ingest.httpPort' "$CONTROL_PLANE_VALUES")
OBS_DATABASE=$(yq '.clickhouse.clickhouse.database' "$CONTROL_PLANE_VALUES")
CLICKHOUSE_USER="loco_e2e"
CLICKHOUSE_PASS="loco_e2e_pass"
BUILDKIT_IMAGE=$(yq '.builds.buildkitImage.repository + ":" + .builds.buildkitImage.tag' "$WORKER_VALUES")

CLUSTER_ADMIN_HOME="$RUN_DIR/cluster-admin-home"
AGENT_KEY_SECRET="e2e-agent-key"
INGEST_TOKEN_SECRET=$(yq '.global.exporter.ingestToken.secretName' "$WORKER_VALUES")
INGEST_TOKEN_SECRET_KEY=$(yq '.global.exporter.ingestToken.secretKey' "$WORKER_VALUES")
E2E_CLUSTER_NAME="e2e-test-cluster"
E2E_CLUSTER_REGION="us-east-1"
E2E_CLUSTER_TIER="production"
BOOTSTRAP_TOKEN=$(openssl rand -hex 32)

LOCO_NAMESPACE="loco-system"
IMAGE_RETENTION=1
IMAGE_SWEEP_INTERVAL=5s
REGISTRY_TOKEN_KEY_VERSION=1
REGISTRY_ACCESS_TOKEN_LIFETIME=1m
REGISTRY_PULL_CREDENTIAL_LIFETIME=2m
REGISTRY_CREDENTIAL_CHECK_INTERVAL=10s
E2E_CLUSTER_ID=""

export E2E_ROOT_DIR="$ROOT_DIR"
export E2E_VERSION
export E2E_RUN_ID="$RUN_ID"
export E2E_BIN_DIR="$BIN_DIR"
export E2E_LOG_DIR="$LOG_DIR"
export E2E_COMPOSE_PROJECT="$RUN_NAME"
export POSTGRES_USER="$PG_USER" POSTGRES_PASSWORD="$PG_PASS" POSTGRES_DB="$PG_DB"
export POSTGRES_PORT="127.0.0.1:" OPENBAO_PORT="127.0.0.1:" REGISTRY_PORT="127.0.0.1:${REGISTRY_HOST_PORT}" S3_PORT
OPENBAO_IMAGE=$(yq '.openbao.image' "$ROOT_DIR/charts/loco-control-plane/values.yaml")
export OPENBAO_IMAGE
export E2E_API_URL="http://127.0.0.1:${API_PORT}"
export E2E_AUTH_ISSUER="http://127.0.0.1:${AUTH_PORT}/dex"
export E2E_AUTH_STATE="$RUN_DIR/auth-state.json"
export DEX_PORT="127.0.0.1:${AUTH_PORT}"
export E2E_AGENT_KEY_SECRET="$AGENT_KEY_SECRET"
export E2E_BOOTSTRAP_TOKEN="$BOOTSTRAP_TOKEN"
export E2E_INGEST_TOKEN_SECRET="$INGEST_TOKEN_SECRET"
export E2E_INGEST_TOKEN_SECRET_KEY="$INGEST_TOKEN_SECRET_KEY"

export E2E_KIND_CLUSTER="$KIND_CLUSTER_NAME"
export E2E_LOCO_NAMESPACE="$LOCO_NAMESPACE"
export E2E_OBS_PROXY_PORT="$OBS_PROXY_PORT"
export E2E_OTLP_HTTP_PORT="$OTLP_HTTP_PORT"
export E2E_GATEWAY_URL="http://127.0.0.1:${GATEWAY_PORT}"
export E2E_OBS_NAMESPACE="$OBS_NAMESPACE"
export E2E_OBS_NODE_NAMESPACE="$OBS_NODE_NAMESPACE"
export E2E_CLICKHOUSE_CONTAINER="$CLICKHOUSE_CONTAINER"
export E2E_OBS_INGEST_CONTAINER="$INGEST_CONTAINER"
export E2E_CLICKHOUSE_USER="$CLICKHOUSE_USER"
export E2E_CLICKHOUSE_PASSWORD="$CLICKHOUSE_PASS"
export E2E_PLATFORM_DOMAIN="$PLATFORM_DOMAIN"
export LOCO_SOURCE_BUCKET="loco-e2e-sources"
export LOCO_SOURCE_BUCKET_ACCESS_KEY_ID="loco-e2e"
export LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY="loco-e2e-secret"
export LOCO_SOURCE_BUCKET_REGION="us-east-1"
export E2E_PUBLIC_REGISTRY_ALIAS="${RUN_NAME}-public-image"
export E2E_REGISTRY_ALIAS="${RUN_NAME}-registry"
export E2E_REGISTRY_HOST="${E2E_REGISTRY_ALIAS}:5000"
export E2E_REGISTRY_URL="http://127.0.0.1:${REGISTRY_HOST_PORT}"
export E2E_REGISTRY_TOKEN_SERVICE="$E2E_REGISTRY_ALIAS"
export E2E_REGISTRY_TOKEN_DIR="$RUN_DIR/registry"
export E2E_REGISTRY_API_USER="api:loco-dev-api"
export E2E_REGISTRY_NODES_USER="nodes:loco-dev-nodes"
export E2E_REGISTRY_BUILDER_USER="builder:loco-dev-builder"
export E2E_REGISTRY_REALM="http://${REALM_CONTAINER}:${API_PORT}/registry/token"
export E2E_S3_ALIAS="${RUN_NAME}-s3.localhost"
export E2E_S3_ENDPOINT="http://${E2E_S3_ALIAS}:${S3_PORT}"
E2E_BUILD_NAMESPACE=$(yq '.builds.namespace' "$WORKER_VALUES")
export E2E_BUILD_NAMESPACE
export E2E_BUILD_WORK_DIR="$LOG_DIR/builds"
E2E_AWS_CLI_IMAGE=$(awk '$1 == "FROM" { print $2 }' "$SCRIPT_DIR/fixtures/aws-cli/Dockerfile")
PUBLIC_FIXTURE_IMAGE=$(awk '$1 == "FROM" { print $2 }' "$SCRIPT_DIR/fixtures/public-image/Dockerfile")
export E2E_AWS_CLI_IMAGE

source "$SCRIPT_DIR/lib.sh"
source "$SCRIPT_DIR/cli.sh"

KIND_NODE_IMAGE=$(yq -r ".${E2E_KUBERNETES_VERSION:-newest}" "$ROOT_DIR/agent/pkg/cluster/kubernetes.json")
if [[ $KIND_NODE_IMAGE == null ]]; then
    log_error "E2E_KUBERNETES_VERSION must be oldest or newest"
    exit 1
fi

# Parse flags
NO_TEARDOWN=false
SKIP_BUILD=false
BUILD_ONLY=false
TEARDOWN_ONLY=false
BUILDS_ENABLED=true
OBSERVABILITY=false
REGISTRY_AUTH=static
TEST_FILTERS=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-teardown)  NO_TEARDOWN=true; shift ;;
        --skip-build)   SKIP_BUILD=true; shift ;;
        --build-only)   BUILD_ONLY=true; shift ;;
        --teardown-only) TEARDOWN_ONLY=true; shift ;;
        --builds-disabled) BUILDS_ENABLED=false; shift ;;
        --observability) OBSERVABILITY=true; shift ;;
        --registry-token) REGISTRY_AUTH=token; shift ;;
        *)              TEST_FILTERS+=("$1"); shift ;;
    esac
done
export E2E_REGISTRY_AUTH="$REGISTRY_AUTH"

kube() {
    kubectl --kubeconfig "$KUBECONFIG_FILE" --context "$KUBE_CONTEXT" "$@"
}

cluster_exists() {
    kind get clusters 2>/dev/null | grep -qx "$KIND_CLUSTER_NAME"
}

kill_tree() {
    local pid=$1 child
    kill -STOP "$pid" 2>/dev/null || return 0
    for child in $(pgrep -P "$pid" || true); do
        kill_tree "$child"
    done
    kill "$pid" 2>/dev/null || true
    kill -CONT "$pid" 2>/dev/null || true
}

job_log() {
    echo "$LOG_DIR/setup-$1.log"
}

start_job() {
    local name=$1
    shift
    "$@" >"$(job_log "$name")" 2>&1 &
    echo $! >"$JOB_PID_DIR/${name}.pid"
    log_info "Started ${name} in the background, logging to $(job_log "$name")"
}

await_job() {
    local name=$1 pid_file="$JOB_PID_DIR/$1.pid" pid
    if [ ! -f "$pid_file" ]; then
        return 0
    fi
    pid=$(cat "$pid_file")
    rm "$pid_file"
    log_info "Waiting for ${name}..."
    if ! wait "$pid"; then
        log_error "${name} failed; its log follows"
        log_group "${name} log (last ${LOG_TAIL_LINES} lines)"
        tail -n "$LOG_TAIL_LINES" "$(job_log "$name")"
        log_group_end
        exit 1
    fi
    log_ok "${name} done"
}

stop_jobs() {
    local pid_file pid
    for pid_file in "$JOB_PID_DIR"/*.pid; do
        [ -f "$pid_file" ] || continue
        pid=$(cat "$pid_file")
        rm "$pid_file"
        kill_tree "$pid"
        wait "$pid" 2>/dev/null || true
    done
    rm -rf "$JOB_PID_DIR"
}

stop_processes() {
    kill_pid_file "$PID_DIR/obs-proxy.pid"
    kill_pid_file "$PID_DIR/gateway-forward.pid"
    kill_pid_file "$PID_DIR/agent.pid"
    kill_pid_file "$PID_DIR/api-replica/api.pid"
    kill_pid_file "$PID_DIR/api.pid"
}

# ─── Teardown ───────────────────────────────────────────────────────────────

teardown() {
    log_step "Tearing down e2e run ${RUN_ID}..."

    stop_processes

    if cluster_exists; then
        log_info "Deleting Kind cluster ${KIND_CLUSTER_NAME}..."
        kind delete cluster --name "$KIND_CLUSTER_NAME" --kubeconfig "$KUBECONFIG_FILE"
    fi

    docker rm -fv "$INGEST_CONTAINER" "$CLICKHOUSE_CONTAINER" "$REALM_CONTAINER" >/dev/null 2>&1 || true

    log_info "Removing compose project ${E2E_COMPOSE_PROJECT}..."
    e2e_compose --profile tools down -v --remove-orphans >/dev/null 2>&1 || true

    local repository
    for repository in "$CONTROLLER_REPOSITORY" "$BUILDER_REPOSITORY" "$OBS_PROXY_REPOSITORY"; do
        docker image rm "${repository}:e2e-${RUN_ID}" >/dev/null 2>&1 || true
    done

    rm -rf "$RUN_DIR"

    log_ok "Teardown complete"
}

if [ "$TEARDOWN_ONLY" = true ]; then
    teardown
    exit 0
fi

# ─── Diagnostics ────────────────────────────────────────────────────────────

dump_logs() {
    local name
    for name in api agent obs-proxy; do
        if [ -s "$LOG_DIR/$name.log" ]; then
            log_group "$name log (last ${LOG_TAIL_LINES} lines)"
            tail -n "$LOG_TAIL_LINES" "$LOG_DIR/$name.log"
            log_group_end
        fi
    done
    log_group "compose services"
    e2e_compose ps -a 2>&1 || true
    e2e_compose logs --no-color --tail "$LOG_TAIL_LINES" 2>&1 || true
    log_group_end
    if [ -s "$KUBECONFIG_FILE" ] && kube get namespace "$LOCO_NAMESPACE" >/dev/null 2>&1; then
        log_group "${LOCO_NAMESPACE} pods"
        kube -n "$LOCO_NAMESPACE" get pods -o wide 2>&1 || true
        log_group_end
        local deployment
        for deployment in $(kube -n "$LOCO_NAMESPACE" get deployments -o name 2>/dev/null); do
            log_group "$deployment"
            kube -n "$LOCO_NAMESPACE" logs "$deployment" --all-containers --tail "$LOG_TAIL_LINES" 2>&1 || true
            log_group_end
        done
    fi
    if [ "$OBSERVABILITY" = true ]; then
        log_group "obs-proxy ingest container"
        docker logs --tail "$LOG_TAIL_LINES" "$INGEST_CONTAINER" 2>&1 || true
        log_group_end
    fi
    if [ "$REGISTRY_AUTH" = token ]; then
        log_group "registry token endpoint proxy"
        docker logs --tail "$LOG_TAIL_LINES" "$REALM_CONTAINER" 2>&1 || true
        log_group_end
    fi
    if [ "$OBSERVABILITY" = true ] && [ -s "$KUBECONFIG_FILE" ]; then
        local namespace workload
        for namespace in "$OBS_NAMESPACE" "$OBS_NODE_NAMESPACE"; do
            log_group "${namespace} pods"
            kube get pods -n "$namespace" -o wide 2>&1 || true
            log_group_end
            for workload in $(kube -n "$namespace" get deployments,statefulsets,daemonsets -o name 2>/dev/null); do
                log_group "${namespace}/${workload}"
                kube -n "$namespace" logs "$workload" --all-containers --tail "$LOG_TAIL_LINES" 2>&1 || true
                log_group_end
            done
        done
    fi
}

on_exit() {
    local status=$?
    stop_jobs
    if [ "$status" -ne 0 ]; then
        log_error "Run ${RUN_ID} failed (exit ${status}); component logs follow"
        dump_logs
    fi
    if [ "$NO_TEARDOWN" = true ]; then
        log_info "Leaving run ${RUN_ID} running (cluster ${KIND_CLUSTER_NAME}, compose project ${E2E_COMPOSE_PROJECT})."
        log_info "Rerun against it: ./tests/e2e/run.sh --no-teardown [--skip-build] <test-filter>"
        log_info "Remove it: E2E_RUN_ID=${RUN_ID} mise run e2e:teardown"
    else
        teardown
    fi
    exit "$status"
}

# ─── Prerequisites ──────────────────────────────────────────────────────────

check_prerequisites() {
    log_step "Checking prerequisites..."
    local missing=()

    for cmd in kind docker kubectl helm go; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            missing+=("$cmd")
        fi
    done

    if [ ${#missing[@]} -gt 0 ]; then
        log_error "Missing required tools: ${missing[*]}"
        exit 1
    fi

    mise run doctor

    log_ok "All prerequisites found"
}

port_in_use() {
    (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

require_free_port() {
    local port=$1 purpose=$2
    if port_in_use "$port"; then
        log_error "Port ${port} (${purpose}) is in use; set E2E_RUN_ID to move run ${RUN_ID} to another port block"
        exit 1
    fi
}

check_ports() {
    log_step "Checking run ${RUN_ID}'s ports ${PORT_BASE}-$((PORT_BASE + PORT_BLOCK_SIZE - 1))..."
    require_free_port "$API_PORT" "API"
    require_free_port "$API_REPLICA_PORT" "API replica"
    if ! e2e_compose port dex 5556 >/dev/null 2>&1; then
        require_free_port "$AUTH_PORT" "identity provider"
    fi
    require_free_port "$OBS_PROXY_PORT" "observability proxy"
    require_free_port "$OTLP_GRPC_PORT" "OTLP/gRPC ingest"
    require_free_port "$OTLP_HTTP_PORT" "OTLP/HTTP ingest"
    if [ "$OBSERVABILITY" = true ]; then
        require_free_port "$GATEWAY_PORT" "gateway"
    fi
    if ! docker port "$CLICKHOUSE_CONTAINER" "${CLICKHOUSE_NATIVE_PORT}/tcp" >/dev/null 2>&1; then
        require_free_port "$CLICKHOUSE_PORT" "ClickHouse"
    fi
    if ! e2e_compose port s3 "$S3_PORT" >/dev/null 2>&1; then
        require_free_port "$S3_PORT" "source bucket"
    fi
    if ! e2e_compose port registry 5000 >/dev/null 2>&1; then
        require_free_port "$REGISTRY_HOST_PORT" "registry"
    fi
    log_ok "Ports free"
}

# ─── Setup ──────────────────────────────────────────────────────────────────

setup_dirs() {
    mkdir -p "$BIN_DIR" "$LOG_DIR" "$PID_DIR" "$JOB_PID_DIR"
}

create_cluster() {
    kind create cluster --image "$KIND_NODE_IMAGE" --config "$SCRIPT_DIR/kind-e2e.yml" --name "$KIND_CLUSTER_NAME" --kubeconfig "$KUBECONFIG_FILE"
}

setup_kind() {
    log_step "Setting up Kind cluster ${KIND_CLUSTER_NAME}..."
    CLUSTER_REUSED=false
    if cluster_exists; then
        local retained_image
        retained_image=$(docker inspect --format '{{.Config.Image}}' "${KIND_CLUSTER_NAME}-control-plane")
        if [[ $retained_image != "$KIND_NODE_IMAGE" ]]; then
            log_error "Retained cluster uses $retained_image, expected $KIND_NODE_IMAGE; run E2E_RUN_ID=$RUN_ID mise run e2e:teardown before changing Kubernetes versions"
            return 1
        fi
        kind get kubeconfig --name "$KIND_CLUSTER_NAME" >"$KUBECONFIG_FILE"
        if kube cluster-info >/dev/null 2>&1; then
            CLUSTER_REUSED=true
            log_info "Kind cluster ${KIND_CLUSTER_NAME} already exists, reusing"
        else
            log_warn "Kind cluster ${KIND_CLUSTER_NAME} is unreachable, recreating"
            kind delete cluster --name "$KIND_CLUSTER_NAME" --kubeconfig "$KUBECONFIG_FILE"
        fi
    fi
    if [ "$CLUSTER_REUSED" = false ]; then
        if ! create_cluster; then
            log_warn "Creating the Kind cluster failed, deleting it and retrying once"
            kind delete cluster --name "$KIND_CLUSTER_NAME" --kubeconfig "$KUBECONFIG_FILE"
            create_cluster
        fi
        log_ok "Kind cluster created"
    fi

    kind get kubeconfig --name "$KIND_CLUSTER_NAME" >"$KUBECONFIG_FILE"
    export KUBECONFIG="$KUBECONFIG_FILE"
    kube cluster-info >/dev/null
    log_ok "kubectl context set to ${KUBE_CONTEXT}"
}

compose_host_port() {
    local published
    published=$(e2e_compose port "$1" "$2")
    echo "${published##*:}"
}

postgres_accepts_queries() {
    goose -dir "$ROOT_DIR/api/migrations" postgres "$E2E_DATABASE_URL" status
}

setup_postgres() {
    log_step "Setting up Postgres..."
    e2e_compose up -d --wait --pull never postgres
    local port
    port=$(compose_host_port postgres 5432)
    export E2E_DATABASE_URL="postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${port}/${PG_DB}?sslmode=disable"
    wait_for "Postgres to answer queries from the host" "$READY_TIMEOUT" postgres_accepts_queries
    log_ok "Postgres ready on port ${port}"
}

setup_openbao() {
    log_step "Setting up OpenBao..."
    e2e_compose up -d --wait --pull never openbao
    e2e_compose run --pull never --rm openbao-seed >/dev/null
    export E2E_TRANSIT_ADDR="http://127.0.0.1:$(compose_host_port openbao 8200)"
    log_ok "OpenBao ready at ${E2E_TRANSIT_ADDR}"
}

dev_api_env() {
    sed -n "s/^$1=//p" "$ROOT_DIR/dev/api.env"
}

clickhouse_answers() {
    docker exec "$CLICKHOUSE_CONTAINER" clickhouse-client --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASS" -q "SELECT 1"
}

setup_clickhouse() {
    log_step "Setting up ClickHouse..."
    docker rm -fv "$CLICKHOUSE_CONTAINER" >/dev/null 2>&1 || true
    printf '<clickhouse>%s</clickhouse>\n' "$(yq '.clickhouse.clickhouse.keeperConfig' "$CONTROL_PLANE_VALUES")" >"$LOG_DIR/clickhouse-keeper.xml"
    chmod a+r "$LOG_DIR/clickhouse-keeper.xml"
    go -C "$ROOT_DIR" run ./tools/pullimage "$CLICKHOUSE_IMAGE"
    docker run -d --name "$CLICKHOUSE_CONTAINER" --network kind -p "127.0.0.1:${CLICKHOUSE_PORT}:${CLICKHOUSE_NATIVE_PORT}" \
        -e CLICKHOUSE_USER="$CLICKHOUSE_USER" -e CLICKHOUSE_PASSWORD="$CLICKHOUSE_PASS" \
        -v "$LOG_DIR/clickhouse-keeper.xml:/etc/clickhouse-server/config.d/keeper.xml:ro" \
        "$CLICKHOUSE_IMAGE" >/dev/null
    wait_for "ClickHouse to answer queries" "$READY_TIMEOUT" clickhouse_answers
    log_ok "ClickHouse ready on port ${CLICKHOUSE_PORT}"
}

run_migrations() {
    log_step "Resetting the database and running migrations..."
    e2e_psql "DROP SCHEMA public CASCADE; CREATE SCHEMA public;" >/dev/null
    goose -dir "$ROOT_DIR/api/migrations" postgres "$E2E_DATABASE_URL" up >/dev/null
    log_ok "Migrations applied"
}

create_namespace() {
    log_step "Creating the ${LOCO_NAMESPACE} namespace..."
    kube apply -f "$ROOT_DIR/manifests/namespaces/${LOCO_NAMESPACE}.yaml" >/dev/null
    log_ok "Namespace ready"
}

build_chart_dependencies() {
    log_step "Building the Helm chart dependencies..."
    mise run --quiet helm:deps >/dev/null
    log_ok "Chart dependencies built"
}

envoy_proxy_deployment() {
    kube -n "$LOCO_NAMESPACE" get deployments \
        -l "gateway.envoyproxy.io/owning-gateway-name=${GATEWAY_NAME},gateway.envoyproxy.io/owning-gateway-namespace=${LOCO_NAMESPACE}" \
        -o name | grep .
}

envoy_proxy_service() {
    kube -n "$LOCO_NAMESPACE" get services \
        -l "gateway.envoyproxy.io/owning-gateway-name=${GATEWAY_NAME},gateway.envoyproxy.io/owning-gateway-namespace=${LOCO_NAMESPACE}" \
        -o name | grep .
}

agent_chart_env() {
    yq ".spec.template.spec.containers[0].env[] | select(.name == \"$1\") | .value" "$RUN_DIR/agent-deployment.yaml"
}

create_gateway_tls_secret() {
    openssl req -x509 -newkey rsa:2048 -nodes -days "$GATEWAY_TLS_DAYS" \
        -subj "/CN=*.${PLATFORM_DOMAIN}" -addext "subjectAltName=DNS:*.${PLATFORM_DOMAIN}" \
        -keyout "$RUN_DIR/gateway-tls.key" -out "$RUN_DIR/gateway-tls.crt" >/dev/null 2>&1
    kube -n "$LOCO_NAMESPACE" create secret tls "$GATEWAY_TLS_SECRET" \
        --cert "$RUN_DIR/gateway-tls.crt" --key "$RUN_DIR/gateway-tls.key" \
        --dry-run=client -o yaml | kube apply -f - >/dev/null
}

install_crds() {
    log_step "Applying the Loco and Gateway API CRDs..."
    local flags=()
    if [ "$BUILDS_ENABLED" = false ]; then
        flags=(--without-builds)
    fi
    KIND_CLUSTER="$KIND_CLUSTER_NAME" mise run --quiet crds:apply ${flags[@]+"${flags[@]}"} >/dev/null
    log_ok "CRDs applied"
}

port_forward() {
    local name=$1 namespace=$2 target=$3 local_port=$4 remote_port=$5
    kill_pid_file "$PID_DIR/${name}-forward.pid"
    kubectl --kubeconfig "$KUBECONFIG_FILE" --context "$KUBE_CONTEXT" -n "$namespace" \
        port-forward --address 127.0.0.1 "$target" "${local_port}:${remote_port}" \
        >"$LOG_DIR/${name}-forward.log" 2>&1 &
    echo $! >"$PID_DIR/${name}-forward.pid"
    wait_for "the ${name} port-forward on ${local_port}" "$READY_TIMEOUT" port_in_use "$local_port"
}

forward_observability_ports() {
    log_step "Forwarding the gateway to the host..."
    port_forward gateway "$LOCO_NAMESPACE" "$(envoy_proxy_service)" "$GATEWAY_PORT" "$GATEWAY_HTTP_PORT"
    log_ok "Gateway at ${E2E_GATEWAY_URL}"
}

ingest_ready() {
    curl -sf "http://$(docker port "$INGEST_CONTAINER" "$(yq '.obsProxy.ingest.port' "$CONTROL_PLANE_VALUES")/tcp" | head -1)/readyz"
}

start_ingest() {
    log_step "Starting a second observability proxy on the kind network..."
    docker rm -fv "$INGEST_CONTAINER" >/dev/null 2>&1 || true
    docker run -d --name "$INGEST_CONTAINER" --network kind --add-host host.docker.internal:host-gateway \
        -e CONTROL_PLANE_URL="http://host.docker.internal:${API_PORT}" \
        -p "127.0.0.1::$(yq '.obsProxy.ingest.port' "$CONTROL_PLANE_VALUES")" \
        -e PORT="$(yq '.obsProxy.ingest.port' "$CONTROL_PLANE_VALUES")" \
        -e CLICKHOUSE_DB="$OBS_DATABASE" \
        -e OTLP_GRPC_PORT="$(yq '.obsProxy.ingest.grpcPort' "$CONTROL_PLANE_VALUES")" \
        -e OTLP_HTTP_PORT="$INGEST_OTLP_HTTP_PORT" \
        -e CLICKHOUSE_INGEST_URL="clickhouse://${CLICKHOUSE_USER}:${CLICKHOUSE_PASS}@${CLICKHOUSE_CONTAINER}:${CLICKHOUSE_NATIVE_PORT}" \
        -e CLICKHOUSE_MIGRATOR_URL="clickhouse://${CLICKHOUSE_USER}:${CLICKHOUSE_PASS}@${CLICKHOUSE_CONTAINER}:${CLICKHOUSE_NATIVE_PORT}" \
        "${OBS_PROXY_REPOSITORY}:e2e-${RUN_ID}" >/dev/null
    wait_for "the kind network proxy readiness" "$READY_TIMEOUT" ingest_ready
    log_ok "Ingest serving OTLP/HTTP at http://${INGEST_CONTAINER}:${INGEST_OTLP_HTTP_PORT} on the kind network"
}

connect_registry() {
    KIND_CLUSTER="$KIND_CLUSTER_NAME" \
    COMPOSE_PROJECT_NAME="$E2E_COMPOSE_PROJECT" \
    REGISTRY_PORT="$REGISTRY_HOST_PORT" \
    REGISTRY_ALIAS="$E2E_REGISTRY_ALIAS" \
    REGISTRY_AUTH="$REGISTRY_AUTH" \
    S3_ALIAS="$E2E_S3_ALIAS" \
        mise run cluster:registry >/dev/null
}

start_realm_proxy() {
    mkdir -p "$E2E_REGISTRY_TOKEN_DIR"
    printf 'server {\n    listen %s;\n    location / {\n        proxy_pass http://host.docker.internal:%s;\n    }\n}\n' \
        "$API_PORT" "$API_PORT" >"$E2E_REGISTRY_TOKEN_DIR/realm.conf"
    chmod a+r "$E2E_REGISTRY_TOKEN_DIR/realm.conf"
    docker rm -fv "$REALM_CONTAINER" >/dev/null 2>&1 || true
    go -C "$ROOT_DIR" run ./tools/pullimage "$PUBLIC_FIXTURE_IMAGE"
    docker run -d --name "$REALM_CONTAINER" --network kind --add-host host.docker.internal:host-gateway \
        -v "$E2E_REGISTRY_TOKEN_DIR/realm.conf:/etc/nginx/conf.d/default.conf:ro" \
        "$PUBLIC_FIXTURE_IMAGE" >/dev/null
}

realm_proxy_reaches_api() {
    docker exec "$REALM_CONTAINER" wget -q -O /dev/null "http://127.0.0.1:${API_PORT}/health"
}

setup_registry() {
    log_step "Starting the registry and the source bucket..."
    if [ "$CLUSTER_REUSED" = true ]; then
        log_info "Emptying the registry, whose tags are immutable"
        e2e_compose rm --stop --force registry >/dev/null
        docker volume rm --force "${E2E_COMPOSE_PROJECT}_registry-data" >/dev/null
    fi
    BUILD_PRIVATE_EGRESS=$(LOCO_REGISTRY_HOST="$E2E_REGISTRY_HOST" LOCO_SOURCE_BUCKET_ENDPOINT="$E2E_S3_ENDPOINT" mise run --quiet cluster:build-egress)
    if [ "$REGISTRY_AUTH" = token ]; then
        e2e_compose up -d --wait --pull never s3
        start_realm_proxy
        BUILD_PRIVATE_EGRESS=$(PORT="$API_PORT" yq -p json -o json -I 0 \
            '. + ([.[].cidr] | unique | map({"cidr": ., "port": env(PORT)}))' <<<"$BUILD_PRIVATE_EGRESS")
        log_ok "Source bucket at ${E2E_S3_ENDPOINT}, registry token endpoint ${E2E_REGISTRY_REALM}, build egress ${BUILD_PRIVATE_EGRESS}; the registry starts after the API"
        return 0
    fi
    e2e_compose up -d --wait --pull never registry s3
    connect_registry
    log_ok "Registry at ${E2E_REGISTRY_HOST} (host port ${REGISTRY_HOST_PORT}), source bucket at ${E2E_S3_ENDPOINT}, build egress ${BUILD_PRIVATE_EGRESS}"
}

setup_public_image() {
    log_step "Mirroring the public fixture image into the run's anonymous registry..."
    e2e_compose up -d public-image-registry
    local port node
    port=$(compose_host_port public-image-registry 5000)
    wait_for "the public fixture registry" "$READY_TIMEOUT" curl -sf "http://127.0.0.1:${port}/v2/"
    export E2E_PUBLIC_IMAGE="localhost:${port}/public-image:e2e"
    go -C "$ROOT_DIR" run ./tools/e2e-mirror-image "$PUBLIC_FIXTURE_IMAGE" "$E2E_PUBLIC_IMAGE"
    # A version that runs for a few seconds without listening and then exits, for failed rollouts.
    export E2E_CRASHING_IMAGE="localhost:${port}/public-image:e2e-crashing"
    go -C "$ROOT_DIR" run ./tools/e2e-mirror-image "$E2E_PUBLIC_IMAGE" "$E2E_CRASHING_IMAGE" sh -c "sleep 5; exit 1"
    for node in $(kind get nodes --name "$KIND_CLUSTER_NAME"); do
        docker exec "$node" mkdir -p "/etc/containerd/certs.d/localhost:${port}"
        printf '[host."http://%s:5000"]\n  capabilities = ["pull", "resolve"]\n' "$E2E_PUBLIC_REGISTRY_ALIAS" |
            docker exec -i "$node" tee "/etc/containerd/certs.d/localhost:${port}/hosts.toml" >/dev/null
    done
}

start_token_registry() {
    log_step "Starting the registry with the API's token endpoint and key..."
    mkdir -p "$E2E_REGISTRY_TOKEN_DIR"
    wait_for "${REALM_CONTAINER} to reach the API" "$READY_TIMEOUT" realm_proxy_reaches_api
    curl -sSf "${E2E_API_URL}/.well-known/loco-registry-key.pem" >"$E2E_REGISTRY_TOKEN_DIR/key.pem"
    CERT="$E2E_REGISTRY_TOKEN_DIR/key.pem" \
        yq -p json -o json '
            .http.auth = {
                "bearer": {"realm": strenv(E2E_REGISTRY_REALM), "service": strenv(E2E_REGISTRY_TOKEN_SERVICE), "cert": strenv(CERT)},
                "failDelay": .http.auth.failDelay
            } | del(.http.accessControl)' \
        "$ROOT_DIR/dev/registry/config.json" >"$E2E_REGISTRY_TOKEN_DIR/config.json"
    chmod a+r "$E2E_REGISTRY_TOKEN_DIR/key.pem" "$E2E_REGISTRY_TOKEN_DIR/config.json"
    e2e_compose up -d --wait --pull never --force-recreate registry
    connect_registry
    log_ok "Registry at ${E2E_REGISTRY_HOST} (host port ${REGISTRY_HOST_PORT}) trusts tokens from ${E2E_API_URL} for service ${E2E_REGISTRY_TOKEN_SERVICE}"
}

build_image() {
    local repository=$1 dockerfile=$2 binary=$3 context="$RUN_DIR/images/$1" target
    target=$(awk '$1 == "COPY" && $2 == "--from=builder" { print $3 }' "$ROOT_DIR/$dockerfile")
    rm -rf "$context"
    mkdir -p "$context$(dirname "$target")"
    cp "$BIN_DIR/images/$binary" "$context$target"
    docker build -t "${repository}:e2e-${RUN_ID}" --build-arg VERSION="$E2E_VERSION" \
        --build-context builder="$context" -f "$ROOT_DIR/$dockerfile" "$ROOT_DIR"
}

build_all() {
    nice -n "$BUILD_NICENESS" mise run --quiet e2e:build "$BIN_DIR" "$E2E_VERSION"
    build_image "$CONTROLLER_REPOSITORY" controller/Dockerfile manager
    if [ "$OBSERVABILITY" = true ]; then
        build_image "$OBS_PROXY_REPOSITORY" observability-proxy/Dockerfile loco-obs-proxy
    fi
    if [ "$BUILDS_ENABLED" = true ]; then
        build_image "$BUILDER_REPOSITORY" builder/Dockerfile loco-builder
    fi
}

pull_compose_images() {
    local references image images=()
    references=$(e2e_compose config --images postgres openbao registry s3 dex)
    while IFS= read -r image; do
        images+=("$image")
    done <<< "$references"
    go -C "$ROOT_DIR" run ./tools/pullimage "${images[@]}"
}

pull_build_images() {
    go -C "$ROOT_DIR" run ./tools/pullimage "$BUILDKIT_IMAGE" "$E2E_AWS_CLI_IMAGE"
}

image_reference() {
    local run_tag="$1:e2e-${RUN_ID}" id
    if ! id=$(docker image inspect -f '{{.Id}}' "$run_tag" 2>/dev/null); then
        log_error "No ${run_tag} image to reuse; run once without --skip-build" >&2
        return 1
    fi
    id=${id#sha256:}
    echo "${run_tag}-${id:0:12}"
}

load_image() {
    local reference=$1
    docker tag "${reference%-*}" "$reference"
    kind load docker-image "$reference" --name "$KIND_CLUSTER_NAME" >/dev/null
    docker image rm "$reference" >/dev/null
    KIND_CLUSTER="$KIND_CLUSTER_NAME" mise run --quiet cluster:prune-images "$reference" >/dev/null
}

load_builder_images() {
    if [ "$BUILDS_ENABLED" = false ]; then
        log_info "Skipping the builder images (--builds-disabled)"
        return 0
    fi
    await_job build
    await_job build-image-pulls
    log_step "Loading the builder image..."
    BUILDER_IMAGE=$(image_reference "$BUILDER_REPOSITORY")
    load_image "$BUILDER_IMAGE"
    local platform
    platform=$(kube get nodes -o jsonpath='{.items[0].status.nodeInfo.operatingSystem}/{.items[0].status.nodeInfo.architecture}')
    load_kind_image "$BUILDKIT_IMAGE" "$KIND_CLUSTER_NAME" "$platform" >/dev/null
    log_ok "Builder ${BUILDER_IMAGE} and BuildKit images loaded into Kind"
}

load_controller_image() {
    await_job build
    log_step "Loading the controller image..."
    CONTROLLER_IMAGE=$(image_reference "$CONTROLLER_REPOSITORY")
    load_image "$CONTROLLER_IMAGE"
    log_ok "Controller image ${CONTROLLER_IMAGE} loaded into Kind"
}

install_worker() {
    local collectors=$1
    log_step "Installing the loco-worker chart (builds=${BUILDS_ENABLED}, observability=${OBSERVABILITY}, collectors=${collectors}, registry.auth=${REGISTRY_AUTH})..."
    local build_values=(--set cluster.role=apps)
    if [ "$BUILDS_ENABLED" = true ]; then
        build_values=(
            --set builds.builderImage.repository="${BUILDER_IMAGE%%:*}"
            --set builds.builderImage.tag="${BUILDER_IMAGE##*:}"
            --set-json "builds.privateEgress=${BUILD_PRIVATE_EGRESS}"
        )
    fi
    local obs_values=(--set otel-col-daemon.enabled=false --set otel-col-deploy.enabled=false --set otel-col-cluster.enabled=false)
    if [ "$collectors" = true ]; then
        obs_values=(
            --values "$OBS_E2E_VALUES"
            --set "global.exporter.endpoint=http://${INGEST_CONTAINER}:${INGEST_OTLP_HTTP_PORT}"
        )
    fi
    if [ "$OBSERVABILITY" = true ]; then
        obs_values+=(--set global.exporter.ingestToken.enabled=true)
    fi
    local registry_values=()
    if [ "$REGISTRY_AUTH" = token ]; then
        registry_values=(
            --set registry.auth=token
            --set registry.pullSecretName=
            --set builds.pushSecretName=
            --set agent.registryCredentialCheckInterval="$REGISTRY_CREDENTIAL_CHECK_INTERVAL"
        )
    fi
    create_gateway_tls_secret
    helm upgrade --install "$WORKER_RELEASE" "$WORKER_CHART" \
        --kubeconfig "$KUBECONFIG_FILE" \
        --kube-context "$KUBE_CONTEXT" \
        --namespace "$LOCO_NAMESPACE" \
        --values "$ROOT_DIR/dev/kind-worker-values.yaml" \
        --values "$WORKER_E2E_VALUES" \
        --set "global.domain.apps={${PLATFORM_DOMAIN}}" \
        --set "certManager.existingTLSSecret=${GATEWAY_TLS_SECRET}" \
        --set controller.image.repository="${CONTROLLER_IMAGE%%:*}" \
        --set controller.image.tag="${CONTROLLER_IMAGE##*:}" \
        "${build_values[@]}" \
        "${obs_values[@]}" \
        ${registry_values[@]+"${registry_values[@]}"} \
        --wait --timeout "$HELM_TIMEOUT" >/dev/null
    if [ "$BUILDS_ENABLED" = false ] && [ "$CLUSTER_REUSED" = true ]; then
        kube delete namespace "$E2E_BUILD_NAMESPACE" --ignore-not-found --wait --timeout="${READY_TIMEOUT}s" >/dev/null
    fi
    helm get manifest "$WORKER_RELEASE" \
        --kubeconfig "$KUBECONFIG_FILE" \
        --kube-context "$KUBE_CONTEXT" \
        --namespace "$LOCO_NAMESPACE" |
        yq "select(.kind == \"Deployment\" and .metadata.name == \"${WORKER_RELEASE}-agent\")" >"$RUN_DIR/agent-deployment.yaml"
    wait_for "the Envoy proxy Deployment" "$OBS_READY_TIMEOUT" envoy_proxy_deployment
    kube -n "$LOCO_NAMESPACE" rollout status "$(envoy_proxy_deployment)" --timeout "${OBS_READY_TIMEOUT}s" >/dev/null
    log_ok "Controllers running and Envoy Gateway serving the ${GATEWAY_NAME} Gateway"
}

reset_cluster_state() {
    if [ "$CLUSTER_REUSED" = false ]; then
        return 0
    fi
    log_step "Removing the previous run's applications, builds and namespaces..."
    kube delete applications.infra.loco.io --all --all-namespaces --cascade=foreground --wait >/dev/null
    if kube get crd builds.infra.loco.io >/dev/null 2>&1; then
        kube delete builds.infra.loco.io --all --all-namespaces --cascade=foreground --wait >/dev/null
    fi
    local namespace namespaces=()
    while read -r namespace; do
        namespaces+=("$namespace")
    done < <(kube get namespaces -o name | grep -E "^namespace/(${E2E_WORKSPACE_PREFIX}|e2e-)" || true)
    if [ ${#namespaces[@]} -gt 0 ]; then
        kube delete "${namespaces[@]}" --wait >/dev/null
    fi
    kube -n "$LOCO_NAMESPACE" delete secrets -l loco.io/registry-pull --ignore-not-found >/dev/null
    log_ok "Cluster state reset"
}

start_builds() {
    if [ "$SKIP_BUILD" = true ]; then
        log_info "Skipping builds (--skip-build)"
        return 0
    fi
    log_step "Building the images and binaries in the background..."
    start_job build build_all
}

api_bind_address() {
    if [ "$OBSERVABILITY" = true ] || [ "$REGISTRY_AUTH" = token ]; then
        echo 0.0.0.0
        return
    fi
    echo 127.0.0.1
}

start_api() {
    log_step "Starting API server (LOCO_REGISTRY_AUTH=${REGISTRY_AUTH})..."

    local registry_env=(
        LOCO_REGISTRY_USERNAME="${E2E_REGISTRY_API_USER%%:*}"
        LOCO_REGISTRY_PASSWORD="${E2E_REGISTRY_API_USER#*:}"
    )
    if [ "$REGISTRY_AUTH" = token ]; then
        registry_env=(
            LOCO_REGISTRY_AUTH=token
            LOCO_REGISTRY_TRANSIT_KEY="$(dev_api_env LOCO_REGISTRY_TRANSIT_KEY)"
            LOCO_REGISTRY_TRANSIT_KEY_VERSION="$REGISTRY_TOKEN_KEY_VERSION"
            LOCO_REGISTRY_TOKEN_SERVICE="$E2E_REGISTRY_TOKEN_SERVICE"
            LOCO_REGISTRY_ACCESS_TOKEN_LIFETIME="$REGISTRY_ACCESS_TOKEN_LIFETIME"
            LOCO_REGISTRY_PULL_CREDENTIAL_LIFETIME="$REGISTRY_PULL_CREDENTIAL_LIFETIME"
        )
    fi

    AUTH_ISSUERS="[{\"issuer\":\"${E2E_AUTH_ISSUER}\",\"audience\":\"loco-e2e\",\"emailVerification\":\"claim\"}]" \
    AUTH_SIGNUP_MODE=open \
    LOCO_BOOTSTRAP_TOKEN="$BOOTSTRAP_TOKEN" \
    DATABASE_URL="$E2E_DATABASE_URL" \
    CACHE_TYPE=in-memory \
    APP_PORT="$(api_bind_address):$API_PORT" \
    CLICKHOUSE_URL="$(clickhouse_dsn "$CLICKHOUSE_USER" "$CLICKHOUSE_PASS")" \
    CLICKHOUSE_DB="$OBS_DATABASE" \
    CLICKHOUSE_QUERY_TIMEOUT="5s" \
    LOCO_OBSERVABILITY_DEFAULT_LIMIT="100" \
    LOCO_OBSERVABILITY_MAX_LIMIT="1000" \
    LOCO_OBSERVABILITY_MAX_TAIL_DURATION="10m" \
    CORS_ALLOWED_ORIGINS="https://dashboard.e2e.test" \
    APP_ENV="test" \
    LOG_LEVEL=debug \
    LOCO_REGISTRY_HOST="$E2E_REGISTRY_HOST" \
    LOCO_REGISTRY_URL="$E2E_REGISTRY_URL" \
    LOCO_IMAGE_RETENTION="$IMAGE_RETENTION" \
    LOCO_IMAGE_SWEEP_INTERVAL="$IMAGE_SWEEP_INTERVAL" \
    LOCO_SOURCE_BUCKET_ENDPOINT="$E2E_S3_ENDPOINT" \
    LOCO_SOURCE_BUCKET_FORCE_PATH_STYLE=true \
    LOCO_SECRETS_TRANSIT_ADDR="$E2E_TRANSIT_ADDR" \
    LOCO_SECRETS_TRANSIT_MOUNT="$(dev_api_env LOCO_SECRETS_TRANSIT_MOUNT)" \
    LOCO_SECRETS_TRANSIT_KEY="$(dev_api_env LOCO_SECRETS_TRANSIT_KEY)" \
    LOCO_SECRETS_TRANSIT_AUTH="$(dev_api_env LOCO_SECRETS_TRANSIT_AUTH)" \
    LOCO_SECRETS_TRANSIT_TOKEN="$(dev_api_env LOCO_SECRETS_TRANSIT_TOKEN)" \
    LOCO_INGEST_TRANSIT_KEY="$(dev_api_env LOCO_INGEST_TRANSIT_KEY)" \
        env "${registry_env[@]}" "$BIN_DIR/loco-api" \
        >"$LOG_DIR/api.log" 2>&1 &

    echo $! > "$PID_DIR/api.pid"

    wait_for "API health" "$READY_TIMEOUT" curl -sf "${E2E_API_URL}/health"
    log_ok "API server running on port ${API_PORT}"
}

locoadm_as_admin() {
    HOME="$CLUSTER_ADMIN_HOME" LOCO_CREDENTIAL_STORE=file LOCO_HOST="$E2E_API_URL" \
        "$BIN_DIR/locoadm" "$@"
}

cluster_admin() {
    locoadm_as_admin cluster "$@"
}

setup_authentication() {
    log_step "Authenticating fixture users and creating application scope through the API..."
    "$BIN_DIR/e2e-auth"
    export E2E_USER_ID E2E_ORG_ID E2E_WORKSPACE_ID E2E_ENVIRONMENT_ID E2E_USER_TOKEN E2E_CLUSTER_ADMIN_TOKEN
    E2E_USER_ID=$(yq -p json -o yaml '.userId' "$E2E_AUTH_STATE")
    E2E_ORG_ID=$(yq -p json -o yaml '.orgId' "$E2E_AUTH_STATE")
    E2E_WORKSPACE_ID=$(yq -p json -o yaml '.workspaceId' "$E2E_AUTH_STATE")
    E2E_ENVIRONMENT_ID=$(yq -p json -o yaml '.environmentId' "$E2E_AUTH_STATE")
    E2E_USER_TOKEN=$(yq -p json -o yaml '.userCredentials.token' "$E2E_AUTH_STATE")
    E2E_CLUSTER_ADMIN_TOKEN=$(yq -p json -o yaml '.adminCredentials.token' "$E2E_AUTH_STATE")
    mkdir -p "$CLUSTER_ADMIN_HOME/.loco"
    yq -p json -o json '.adminCredentials' "$E2E_AUTH_STATE" >"$CLUSTER_ADMIN_HOME/.loco/credentials.json"
    chmod 600 "$CLUSTER_ADMIN_HOME/.loco/credentials.json"
    cli_user_id=$E2E_USER_ID
    cli_org_id=$E2E_ORG_ID
    cli_workspace_id=$E2E_WORKSPACE_ID
    log_ok "Real identity-provider accounts and API-issued sessions ready"
}

setup_identity_provider() {
    log_step "Starting fixture identity provider..."
    e2e_compose up -d --wait --pull never dex
    wait_for "Identity provider discovery" "$READY_TIMEOUT" curl -sf "${E2E_AUTH_ISSUER}/.well-known/openid-configuration"
}

delete_agent_key() {
    kube -n "$LOCO_NAMESPACE" delete secret "$AGENT_KEY_SECRET" --ignore-not-found >/dev/null
}

create_e2e_cluster() {
    log_step "Creating the cluster record and its join token..."
    local created
    created=$(cluster_admin create --name "$E2E_CLUSTER_NAME" --region "$E2E_CLUSTER_REGION" --tier "$E2E_CLUSTER_TIER")
    E2E_JOIN_TOKEN=$(yq -p json '.joinToken.token' <<<"$created")
    export E2E_JOIN_TOKEN
    delete_agent_key
    log_ok "Cluster $(yq -p json '.clusterId' <<<"$created") is pending enrollment"
}

agent_registered() {
    test "$(e2e_psql "SELECT EXISTS (SELECT 1 FROM clusters WHERE cluster_key = '${E2E_CLUSTER_KEY}' AND agent_version = '${E2E_VERSION}' AND sync_generation > ${E2E_PREVIOUS_SYNC_GENERATION})")" = t
}

agent_heartbeat_recorded() {
    test "$(e2e_psql "SELECT last_heartbeat IS NOT NULL FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")" = t
}

start_agent() {
    log_step "Starting Agent..."

    E2E_CLUSTER_KEY=$(kube get namespace kube-system -o jsonpath='{.metadata.uid}')
    export E2E_CLUSTER_KEY
    E2E_PREVIOUS_SYNC_GENERATION=$(e2e_psql "SELECT COALESCE((SELECT sync_generation FROM clusters WHERE cluster_key = '${E2E_CLUSTER_KEY}'), 0)")
    CONTROL_PLANE_URL="$E2E_API_URL" \
    LOCO_JOIN_TOKEN="${E2E_JOIN_TOKEN:-}" \
    LOCO_AGENT_KEY_SECRET="$AGENT_KEY_SECRET" \
    LOCO_ASSERTION_LIFETIME="$(yq '.agent.assertionLifetime' "$WORKER_VALUES")" \
    LOCO_INGEST_TOKEN_SECRET="$(agent_chart_env LOCO_INGEST_TOKEN_SECRET)" \
    LOCO_INGEST_TOKEN_SECRET_KEY="$(agent_chart_env LOCO_INGEST_TOKEN_SECRET_KEY)" \
    LOCO_INGEST_TOKEN_NAMESPACES="$(agent_chart_env LOCO_INGEST_TOKEN_NAMESPACES)" \
    LOG_LEVEL="$(yq '.env.LOG_LEVEL' "$WORKER_VALUES")" \
    LOCO_NAMESPACE="$LOCO_NAMESPACE" \
    LOCO_BUILD_NAMESPACE="$E2E_BUILD_NAMESPACE" \
    LOCO_CONTROLLER_DEPLOYMENT="$(agent_chart_env LOCO_CONTROLLER_DEPLOYMENT)" \
    LOCO_CLUSTER_ROLE="$(agent_chart_env LOCO_CLUSTER_ROLE)" \
    LOCO_INVENTORY_INTERVAL="$(yq '.agent.inventoryInterval' "$WORKER_VALUES")" \
    LOCO_BUILD_RETENTION="$(yq '.agent.buildRetention' "$WORKER_VALUES")" \
    LOCO_BUILD_COLLECT_INTERVAL="$(yq '.agent.buildCollectInterval' "$WORKER_VALUES")" \
    LOCO_CLUSTER_QUERY_TIMEOUT="$(yq '.agent.clusterQueryTimeout' "$WORKER_VALUES")" \
    LOCO_RECONCILE_WORKERS="$(yq '.agent.reconcileWorkers' "$WORKER_VALUES")" \
    LOCO_RECONCILE_RETRY_BASE_DELAY="$(yq '.agent.reconcileRetryBaseDelay' "$WORKER_VALUES")" \
    LOCO_RECONCILE_RETRY_MAX_DELAY="$(yq '.agent.reconcileRetryMaxDelay' "$WORKER_VALUES")" \
    LOCO_SYNC_OUTBOUND_BUFFER="$(yq '.agent.syncOutboundBuffer' "$WORKER_VALUES")" \
    LOCO_BUILD_QUEUE_SIZE="$(yq '.agent.buildQueueSize' "$WORKER_VALUES")" \
    LOCO_BUILD_CREATE_RETRY_DELAY="$(yq '.agent.buildCreateRetryDelay' "$WORKER_VALUES")" \
    LOCO_BUILD_CREATE_RETRY_ATTEMPTS="$(yq '.agent.buildCreateRetryAttempts' "$WORKER_VALUES")" \
    LOCO_RECONNECT_BASE_DELAY="$(yq '.agent.reconnectBaseDelay' "$WORKER_VALUES")" \
    LOCO_RECONNECT_MAX_DELAY="$(yq '.agent.reconnectMaxDelay' "$WORKER_VALUES")" \
    LOCO_HEALTHY_STREAM_DURATION="$(yq '.agent.healthyStreamDuration' "$WORKER_VALUES")" \
    LOCO_GATEWAY="$(agent_chart_env LOCO_GATEWAY)" \
    LOCO_CERTIFICATE="$(agent_chart_env LOCO_CERTIFICATE)" \
    LOCO_APP_DOMAINS="$(agent_chart_env LOCO_APP_DOMAINS)" \
    LOCO_REGISTRY_AUTH="$(agent_chart_env LOCO_REGISTRY_AUTH)" \
    LOCO_REGISTRY_CREDENTIAL_CHECK_INTERVAL="$(agent_chart_env LOCO_REGISTRY_CREDENTIAL_CHECK_INTERVAL)" \
    KUBECONFIG="$KUBECONFIG_FILE" \
        "$BIN_DIR/loco-agent" \
        >>"$LOG_DIR/agent.log" 2>&1 &

    echo $! > "$PID_DIR/agent.pid"

    wait_for "Agent registration" "$READY_TIMEOUT" agent_registered
    E2E_CLUSTER_ID=$(e2e_psql "SELECT id FROM clusters WHERE cluster_key = '${E2E_CLUSTER_KEY}'")
    export E2E_CLUSTER_ID
    log_ok "Agent registered"
}

clickhouse_dsn() {
    local user=$1 password=$2
    echo "clickhouse://${user}:${password}@127.0.0.1:${CLICKHOUSE_PORT}"
}

start_obs_proxy() {
    log_step "Starting Observability Proxy..."

    local dsn
    dsn=$(clickhouse_dsn "$CLICKHOUSE_USER" "$CLICKHOUSE_PASS")

    PORT="$OBS_PROXY_PORT" \
    CONTROL_PLANE_URL="$E2E_API_URL" \
    CLICKHOUSE_DB="$OBS_DATABASE" \
    CLICKHOUSE_MIGRATOR_URL="$dsn" \
    CLICKHOUSE_INGEST_URL="$dsn" \
    OTLP_GRPC_PORT="$OTLP_GRPC_PORT" \
    OTLP_HTTP_PORT="$OTLP_HTTP_PORT" \
        "$BIN_DIR/loco-obs-proxy" \
        >"$LOG_DIR/obs-proxy.log" 2>&1 &

    echo $! > "$PID_DIR/obs-proxy.pid"

    wait_for "Obs proxy readiness" "$READY_TIMEOUT" curl -sf "http://127.0.0.1:${OBS_PROXY_PORT}/readyz"
    log_ok "Observability proxy running on port ${OBS_PROXY_PORT}"
}

# ─── Test Runner ────────────────────────────────────────────────────────────

select_suites() {
    local file name filter matched
    local files=() names=()
    local filters=(${TEST_FILTERS[@]+"${TEST_FILTERS[@]}"})
    for file in "$SCRIPT_DIR"/suites/*.sh; do
        [ -f "$file" ] || continue
        files+=("$file")
    done
    if [ ${#files[@]} -eq 0 ]; then
        log_error "No e2e suites found in $SCRIPT_DIR/suites"
        return 1
    fi

    for filter in ${filters[@]+"${filters[@]}"}; do
        matched=false
        for file in "${files[@]}"; do
            name=${file##*/}
            if [ -n "$filter" ] && suite_selected "${name%.sh}" "$filter"; then
                matched=true
                break
            fi
        done
        if [ "$matched" = false ]; then
            log_error "No e2e suite matches suite filter '$filter'"
            return 1
        fi
    done

    # Cluster attachment exercises an enrolled cluster with a running workload.
    if [ "${#filters[@]}" -gt 0 ] && suite_selected 09_cluster_attachment "${filters[@]}"; then
        filters+=(01_agent_lifecycle)
    fi

    SELECTED_SUITES=()
    for file in "${files[@]}"; do
        name=${file##*/}
        name=${name%.sh}
        if suite_selected "$name" ${filters[@]+"${filters[@]}"}; then
            SELECTED_SUITES+=("$file")
            names+=("$name")
        fi
    done
    log_info "Selected e2e suites: ${names[*]}"
}

suite_cases() (
    # With extdebug, declare -F reports the file that defined each function.
    shopt -s extdebug
    local name line file
    while read -r name; do
        read -r name line file <<<"$(declare -F "$name")"
        if [ "$file" = "$1" ]; then
            printf '%s\n' "$name"
        fi
    done < <(compgen -A function test_)
)

record_suite_result() {
    local status=$1
    if [ "$status" -ne 0 ]; then
        log_error "${test_case} failed (exit ${status})"
        if [ "$E2E_FAIL" -eq "$failures_before" ]; then
            E2E_FAIL=$((E2E_FAIL + 1))
        fi
    fi
    if declare -F suite_diagnostics >/dev/null; then
        suite_diagnostics "$status"
    fi
    printf '%s %s %s\n' "$E2E_PASS" "$E2E_FAIL" "$E2E_SKIP" >"$suite_counts_file"
}

run_suite() (
    # Cases share state within the suite. Call each directly so errexit applies.
    set -e
    local file=$1 name=$2 cases
    # EXIT runs after Bash unwinds the function's locals.
    test_case="$name setup"
    failures_before=$E2E_FAIL
    suite_counts_file=$3
    trap 'record_suite_result "$?"' EXIT
    source "$file"

    if [ -n "${E2E_SKIP_REASON:-}" ]; then
        log_warn "Skipping ${name}: ${E2E_SKIP_REASON}"
        E2E_SKIP=$((E2E_SKIP + 1))
        return 0
    fi

    cases=$(suite_cases "$file")
    if [ -z "$cases" ]; then
        log_error "No test cases defined in ${name}"
        return 1
    fi
    for test_case in $cases; do
        failures_before=$E2E_FAIL
        log_info "  ${test_case}..."
        "$test_case"
    done
)

run_tests() {
    log_step "Running e2e tests..."
    echo ""

    local test_file test_name
    local counts_file="$LOG_DIR/counts"
    for test_file in "${SELECTED_SUITES[@]}"; do
        test_name=${test_file##*/}
        test_name=${test_name%.sh}
        echo "────────────────────────────────────────"
        log_step "Running: ${test_name}"
        echo "────────────────────────────────────────"

        # A conditional call would disable errexit inside the suite as well.
        rm -f "$counts_file"
        set +e
        run_suite "$test_file" "$test_name" "$counts_file"
        set -e
        read -r E2E_PASS E2E_FAIL E2E_SKIP < "$counts_file"

        echo ""
    done
}

check_agent_log() {
    log_step "Checking the agent log of the whole run..."
    local log_file="$LOG_DIR/agent.log" errors
    assert "Agent log exists at ${log_file}" test -f "$log_file" || return 0
    touch "$AGENT_EXPECTED_ERRORS"
    errors=$(grep '"level":"ERROR"' "$log_file" | grep -vxF -f "$AGENT_EXPECTED_ERRORS" || true)
    if ! assert "Agent logged no ERROR entries during the run" test -z "$errors"; then
        echo "$errors"
    fi
}

# ─── Main ───────────────────────────────────────────────────────────────────

main() {
    echo ""
    echo "╔══════════════════════════════════════╗"
    echo "║       Loco E2E Test Runner           ║"
    echo "╚══════════════════════════════════════╝"
    echo ""
    log_info "Run ${RUN_ID}: cluster ${KIND_CLUSTER_NAME}, compose project ${E2E_COMPOSE_PROJECT}, state in ${RUN_DIR}"

    select_suites
    trap on_exit EXIT

    export E2E_BUILDS_ENABLED="$BUILDS_ENABLED"
    export E2E_OBSERVABILITY="$OBSERVABILITY"
    check_prerequisites
    stop_processes
    check_ports
    setup_dirs
    if [ "$BUILDS_ENABLED" = true ]; then
        start_job build-image-pulls pull_build_images
    fi
    start_builds
    setup_kind
    create_namespace
    build_chart_dependencies
    install_crds
    pull_compose_images
    setup_postgres
    setup_identity_provider
    setup_openbao
    setup_clickhouse
    run_migrations
    setup_registry
    setup_public_image
    load_builder_images
    load_controller_image
    install_worker false
    reset_cluster_state
    await_job build
    start_api
    if [ "$REGISTRY_AUTH" = token ]; then
        start_token_registry
    fi
    if [ "$(e2e_psql 'SELECT count(*) FROM clusters')" != 0 ]; then
        log_error "Cluster registry must be empty before enrollment"
        return 1
    fi
    setup_authentication
    create_e2e_cluster
    rm -f "$LOG_DIR/agent.log" "$AGENT_EXPECTED_ERRORS"
    start_agent
    if [ "$OBSERVABILITY" = true ]; then
        install_worker true
        forward_observability_ports
    fi
    start_obs_proxy
    if [ "$OBSERVABILITY" = true ]; then
        await_job build
        start_ingest
    fi

    echo ""
    echo "════════════════════════════════════════"
    log_ok "Infrastructure ready"
    echo "════════════════════════════════════════"
    echo ""

    run_tests
    check_agent_log
    print_summary
}

build_only() {
    trap stop_jobs EXIT
    setup_dirs
    start_builds
    await_job build
    log_ok "Images tagged e2e-${RUN_ID} and binaries in ${BIN_DIR}"
}

if [ "$BUILD_ONLY" = true ]; then
    build_only
else
    main
fi
