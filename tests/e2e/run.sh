#!/usr/bin/env bash
# E2E test orchestrator for Loco
# Usage: ./tests/e2e/run.sh [--no-teardown] [--skip-build] [--build-only] [--teardown-only] [--builds-disabled] [--observability] [test-filter...]

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
OTLP_GRPC_PORT=$((PORT_BASE + 6))
OTLP_HTTP_PORT=$((PORT_BASE + 7))
OBS_PROXY_PORT=$((PORT_BASE + 1))
S3_PORT=$((PORT_BASE + 2))
CLICKHOUSE_PORT=$((PORT_BASE + 3))
GATEWAY_PORT=$((PORT_BASE + 4))
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
INGEST_OTLP_HTTP_PORT=$(yq '.obsProxy.ingest.httpPort' "$CONTROL_PLANE_VALUES")
OBS_DATABASE=$(yq '.clickhouse.clickhouse.database' "$CONTROL_PLANE_VALUES")
CLICKHOUSE_USER="loco_e2e"
CLICKHOUSE_PASS="loco_e2e_pass"
BUILDKIT_IMAGE=$(yq '.builds.buildkitImage.repository + ":" + .builds.buildkitImage.tag' "$WORKER_VALUES")
CLUSTER_ADMIN_TOKEN="loco_s_e2e-admin-session-do-not-use-in-production"
CLUSTER_ADMIN_HOME="$RUN_DIR/cluster-admin-home"
AGENT_KEY_SECRET="e2e-agent-key"
E2E_CLUSTER_NAME="e2e-test-cluster"
E2E_CLUSTER_REGION="us-east-1"
E2E_CLUSTER_TIER="production"
BOOTSTRAP_TOKEN=$(openssl rand -hex 32)
USER_TOKEN="loco_s_e2e-test-session-do-not-use-in-production"
LOCO_NAMESPACE="loco-system"
IMAGE_RETENTION=1
IMAGE_SWEEP_INTERVAL=5s
E2E_CLUSTER_ID=""

export E2E_ROOT_DIR="$ROOT_DIR"
export E2E_VERSION
export E2E_RUN_ID="$RUN_ID"
export E2E_BIN_DIR="$BIN_DIR"
export E2E_LOG_DIR="$LOG_DIR"
export E2E_COMPOSE_PROJECT="$RUN_NAME"
export POSTGRES_USER="$PG_USER" POSTGRES_PASSWORD="$PG_PASS" POSTGRES_DB="$PG_DB"
export POSTGRES_PORT="127.0.0.1:" OPENBAO_PORT="127.0.0.1:" REGISTRY_PORT="127.0.0.1:" S3_PORT
export E2E_API_URL="http://127.0.0.1:${API_PORT}"
export E2E_CLUSTER_ADMIN_TOKEN="$CLUSTER_ADMIN_TOKEN"
export E2E_AGENT_KEY_SECRET="$AGENT_KEY_SECRET"
export E2E_BOOTSTRAP_TOKEN="$BOOTSTRAP_TOKEN"
export E2E_USER_TOKEN="$USER_TOKEN"
export E2E_KIND_CLUSTER="$KIND_CLUSTER_NAME"
export E2E_LOCO_NAMESPACE="$LOCO_NAMESPACE"
export E2E_OBS_PROXY_PORT="$OBS_PROXY_PORT"
export E2E_OTLP_HTTP_PORT="$OTLP_HTTP_PORT"
export E2E_GATEWAY_URL="http://127.0.0.1:${GATEWAY_PORT}"
export E2E_OBS_NAMESPACE="$OBS_NAMESPACE"
export E2E_CLICKHOUSE_CONTAINER="$CLICKHOUSE_CONTAINER"
export E2E_OBS_INGEST_CONTAINER="$INGEST_CONTAINER"
export E2E_CLICKHOUSE_USER="$CLICKHOUSE_USER"
export E2E_CLICKHOUSE_PASSWORD="$CLICKHOUSE_PASS"
export E2E_PLATFORM_DOMAIN="$PLATFORM_DOMAIN"
export LOCO_SOURCE_BUCKET="loco-e2e-sources"
export LOCO_SOURCE_BUCKET_ACCESS_KEY_ID="loco-e2e"
export LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY="loco-e2e-secret"
export LOCO_SOURCE_BUCKET_REGION="us-east-1"
export E2E_REGISTRY_ALIAS="${RUN_NAME}-registry"
export E2E_REGISTRY_HOST="${E2E_REGISTRY_ALIAS}:5000"
export E2E_REGISTRY_API_USER="api:loco-dev-api"
export E2E_REGISTRY_NODES_USER="nodes:loco-dev-nodes"
export E2E_S3_ALIAS="${RUN_NAME}-s3.localhost"
export E2E_S3_ENDPOINT="http://${E2E_S3_ALIAS}:${S3_PORT}"
E2E_BUILD_NAMESPACE=$(yq '.builds.namespace' "$WORKER_VALUES")
export E2E_BUILD_NAMESPACE
export E2E_BUILD_WORK_DIR="$LOG_DIR/builds"
E2E_AWS_CLI_IMAGE=$(awk '$1 == "FROM" { print $2 }' "$SCRIPT_DIR/fixtures/aws-cli/Dockerfile")
E2E_PUBLIC_IMAGE=$(awk '$1 == "FROM" { split($2, ref, "@"); print ref[1] }' "$SCRIPT_DIR/fixtures/public-image/Dockerfile")
export E2E_AWS_CLI_IMAGE E2E_PUBLIC_IMAGE

source "$SCRIPT_DIR/lib.sh"
source "$SCRIPT_DIR/cli.sh"

# Parse flags
NO_TEARDOWN=false
SKIP_BUILD=false
BUILD_ONLY=false
TEARDOWN_ONLY=false
BUILDS_ENABLED=true
OBSERVABILITY=false
TEST_FILTERS=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-teardown)  NO_TEARDOWN=true; shift ;;
        --skip-build)   SKIP_BUILD=true; shift ;;
        --build-only)   BUILD_ONLY=true; shift ;;
        --teardown-only) TEARDOWN_ONLY=true; shift ;;
        --builds-disabled) BUILDS_ENABLED=false; shift ;;
        --observability) OBSERVABILITY=true; shift ;;
        *)              TEST_FILTERS+=("$1"); shift ;;
    esac
done

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

    docker rm -fv "$INGEST_CONTAINER" "$CLICKHOUSE_CONTAINER" >/dev/null 2>&1 || true

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
    log_ok "Ports free"
}

# ─── Setup ──────────────────────────────────────────────────────────────────

setup_dirs() {
    mkdir -p "$BIN_DIR" "$LOG_DIR" "$PID_DIR" "$JOB_PID_DIR"
}

create_cluster() {
    kind create cluster --config "$SCRIPT_DIR/kind-e2e.yml" --name "$KIND_CLUSTER_NAME" --kubeconfig "$KUBECONFIG_FILE"
}

setup_kind() {
    log_step "Setting up Kind cluster ${KIND_CLUSTER_NAME}..."
    CLUSTER_REUSED=false
    if cluster_exists; then
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
    e2e_compose up -d --wait postgres
    local port
    port=$(compose_host_port postgres 5432)
    export E2E_DATABASE_URL="postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${port}/${PG_DB}?sslmode=disable"
    wait_for "Postgres to answer queries from the host" "$READY_TIMEOUT" postgres_accepts_queries
    log_ok "Postgres ready on port ${port}"
}

setup_openbao() {
    log_step "Setting up OpenBao..."
    e2e_compose up -d --wait openbao
    e2e_compose run --rm openbao-seed >/dev/null
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
    docker run -d --name "$CLICKHOUSE_CONTAINER" --network kind -p "127.0.0.1:${CLICKHOUSE_PORT}:${CLICKHOUSE_NATIVE_PORT}" \
        -e CLICKHOUSE_USER="$CLICKHOUSE_USER" -e CLICKHOUSE_PASSWORD="$CLICKHOUSE_PASS" \
        -v "$LOG_DIR/clickhouse-keeper.xml:/etc/clickhouse-server/config.d/keeper.xml:ro" \
        "$CLICKHOUSE_IMAGE" >/dev/null
    wait_for "ClickHouse to answer queries" "$READY_TIMEOUT" clickhouse_answers
    log_ok "ClickHouse ready on port ${CLICKHOUSE_PORT}"
}

run_migrations() {
    log_step "Resetting the database, running migrations and seeding test data..."
    e2e_psql "DROP SCHEMA public CASCADE; CREATE SCHEMA public;" >/dev/null
    goose -dir "$ROOT_DIR/api/migrations" postgres "$E2E_DATABASE_URL" up >/dev/null
    SEED_FILE=api/seed/e2e.sql e2e_compose run --rm seed >/dev/null
    log_ok "Migrations applied and test data seeded"
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
    log_step "Starting the observability proxy ingest role on the kind network..."
    docker rm -fv "$INGEST_CONTAINER" >/dev/null 2>&1 || true
    docker run -d --name "$INGEST_CONTAINER" --network kind \
        -p "127.0.0.1::$(yq '.obsProxy.ingest.port' "$CONTROL_PLANE_VALUES")" \
        -e QUERY_ENABLED=false -e INGEST_ENABLED=true \
        -e PORT="$(yq '.obsProxy.ingest.port' "$CONTROL_PLANE_VALUES")" \
        -e CLICKHOUSE_DB="$OBS_DATABASE" \
        -e OTLP_GRPC_PORT="$(yq '.obsProxy.ingest.grpcPort' "$CONTROL_PLANE_VALUES")" \
        -e OTLP_HTTP_PORT="$INGEST_OTLP_HTTP_PORT" \
        -e CLICKHOUSE_INGEST_URL="clickhouse://${CLICKHOUSE_USER}:${CLICKHOUSE_PASS}@${CLICKHOUSE_CONTAINER}:${CLICKHOUSE_NATIVE_PORT}" \
        "${OBS_PROXY_REPOSITORY}:e2e-${RUN_ID}" >/dev/null
    wait_for "the ingest role readiness" "$READY_TIMEOUT" ingest_ready
    log_ok "Ingest serving OTLP/HTTP at http://${INGEST_CONTAINER}:${INGEST_OTLP_HTTP_PORT} on the kind network"
}

setup_registry() {
    log_step "Starting the registry and the source bucket..."
    if [ "$CLUSTER_REUSED" = true ]; then
        log_info "Emptying the registry, whose tags are immutable"
        e2e_compose rm --stop --force registry >/dev/null
        docker volume rm --force "${E2E_COMPOSE_PROJECT}_registry-data" >/dev/null
    fi
    e2e_compose up -d --wait registry s3
    local registry_port
    registry_port=$(compose_host_port registry 5000)
    export E2E_REGISTRY_URL="http://127.0.0.1:${registry_port}"
    KIND_CLUSTER="$KIND_CLUSTER_NAME" \
    COMPOSE_PROJECT_NAME="$E2E_COMPOSE_PROJECT" \
    REGISTRY_PORT="$registry_port" \
    REGISTRY_ALIAS="$E2E_REGISTRY_ALIAS" \
    S3_ALIAS="$E2E_S3_ALIAS" \
        mise run cluster:registry >/dev/null
    BUILD_PRIVATE_EGRESS=$(LOCO_REGISTRY_HOST="$E2E_REGISTRY_HOST" LOCO_SOURCE_BUCKET_ENDPOINT="$E2E_S3_ENDPOINT" mise run --quiet cluster:build-egress)
    log_ok "Registry at ${E2E_REGISTRY_HOST} (host port ${registry_port}), source bucket at ${E2E_S3_ENDPOINT}, build egress ${BUILD_PRIVATE_EGRESS}"
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

pull_build_images() {
    local image
    for image in "$BUILDKIT_IMAGE" "$E2E_AWS_CLI_IMAGE"; do
        if ! docker image inspect "$image" >/dev/null 2>&1; then
            docker pull -q "$image"
        fi
    done
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
    log_step "Installing the loco-worker chart (builds.enabled=${BUILDS_ENABLED}, observability=${OBSERVABILITY})..."
    local build_values=(--set builds.enabled=false)
    if [ "$BUILDS_ENABLED" = true ]; then
        build_values=(
            --set builds.builderImage.repository="${BUILDER_IMAGE%%:*}"
            --set builds.builderImage.tag="${BUILDER_IMAGE##*:}"
            --set-json "builds.privateEgress=${BUILD_PRIVATE_EGRESS}"
        )
    fi
    local obs_values=(--set otel-col-daemon.enabled=false --set otel-col-deploy.enabled=false --set otel-col-cluster.enabled=false)
    if [ "$OBSERVABILITY" = true ]; then
        obs_values=(
            --values "$OBS_E2E_VALUES"
            --set "global.exporter.endpoint=http://${INGEST_CONTAINER}:${INGEST_OTLP_HTTP_PORT}"
        )
    fi
    create_gateway_tls_secret
    helm upgrade --install "$WORKER_RELEASE" "$WORKER_CHART" \
        --kubeconfig "$KUBECONFIG_FILE" \
        --kube-context "$KUBE_CONTEXT" \
        --namespace "$LOCO_NAMESPACE" \
        --values "$WORKER_E2E_VALUES" \
        --set "global.domain.apps={${PLATFORM_DOMAIN}}" \
        --set "certManager.existingTLSSecret=${GATEWAY_TLS_SECRET}" \
        --set controller.image.repository="${CONTROLLER_IMAGE%%:*}" \
        --set controller.image.tag="${CONTROLLER_IMAGE##*:}" \
        "${build_values[@]}" \
        "${obs_values[@]}" \
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

start_api() {
    log_step "Starting API server..."

    LOCO_BOOTSTRAP_TOKEN="$BOOTSTRAP_TOKEN" \
    DATABASE_URL="$E2E_DATABASE_URL" \
    CACHE_TYPE=in-memory \
    APP_PORT="127.0.0.1:$API_PORT" \
    OBSERVABILITY_PROXY_URL="http://127.0.0.1:$OBS_PROXY_PORT" \
    CORS_ALLOWED_ORIGINS="https://dashboard.e2e.test" \
    APP_ENV="test" \
    LOG_LEVEL=debug \
    LOCO_REGISTRY_HOST="$E2E_REGISTRY_HOST" \
    LOCO_REGISTRY_URL="$E2E_REGISTRY_URL" \
    LOCO_REGISTRY_USERNAME="${E2E_REGISTRY_API_USER%%:*}" \
    LOCO_REGISTRY_PASSWORD="${E2E_REGISTRY_API_USER#*:}" \
    LOCO_IMAGE_RETENTION="$IMAGE_RETENTION" \
    LOCO_IMAGE_SWEEP_INTERVAL="$IMAGE_SWEEP_INTERVAL" \
    LOCO_SOURCE_BUCKET_ENDPOINT="$E2E_S3_ENDPOINT" \
    LOCO_SOURCE_BUCKET_FORCE_PATH_STYLE=true \
    LOCO_SECRETS_TRANSIT_ADDR="$E2E_TRANSIT_ADDR" \
    LOCO_SECRETS_TRANSIT_MOUNT="$(dev_api_env LOCO_SECRETS_TRANSIT_MOUNT)" \
    LOCO_SECRETS_TRANSIT_KEY="$(dev_api_env LOCO_SECRETS_TRANSIT_KEY)" \
    LOCO_SECRETS_TRANSIT_TOKEN="$(dev_api_env LOCO_SECRETS_TRANSIT_TOKEN)" \
        "$BIN_DIR/loco-api" \
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

seed_cluster_admin() {
    e2e_psql "
        INSERT INTO users (id, email) VALUES ('00000000-0000-7000-8000-000000000008', 'loco-admin@test.local');
        INSERT INTO session_tokens (id, access_token_hash, refresh_token_hash, user_id, access_expires_at, refresh_expires_at)
        VALUES ('00000000-0000-7000-8000-000000000009',
                encode(sha256(convert_to('${CLUSTER_ADMIN_TOKEN}', 'UTF8')), 'hex'),
                encode(sha256(convert_to('refresh-${CLUSTER_ADMIN_TOKEN}', 'UTF8')), 'hex'),
                '00000000-0000-7000-8000-000000000008', NOW() + INTERVAL '1 day', NOW() + INTERVAL '1 day');
    " >/dev/null
    mkdir -p "$CLUSTER_ADMIN_HOME/.loco"
    printf '{"Host":"%s","ExpiresAt":"2099-01-01T00:00:00Z","Token":"%s","RefreshToken":""}\n' \
        "$E2E_API_URL" "$CLUSTER_ADMIN_TOKEN" >"$CLUSTER_ADMIN_HOME/.loco/credentials.json"
    locoadm_as_admin bootstrap --token "$BOOTSTRAP_TOKEN" >/dev/null
    log_ok "Cluster admin bootstrapped"
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

start_agent() {
    log_step "Starting Agent..."

    E2E_CLUSTER_KEY=$(kube get namespace kube-system -o jsonpath='{.metadata.uid}')
    export E2E_CLUSTER_KEY
    E2E_PREVIOUS_SYNC_GENERATION=$(e2e_psql "SELECT COALESCE((SELECT sync_generation FROM clusters WHERE cluster_key = '${E2E_CLUSTER_KEY}'), 0)")
    CONTROL_PLANE_URL="$E2E_API_URL" \
    LOCO_JOIN_TOKEN="${E2E_JOIN_TOKEN:-}" \
    LOCO_AGENT_KEY_SECRET="$AGENT_KEY_SECRET" \
    LOCO_ASSERTION_LIFETIME="$(yq '.agent.assertionLifetime' "$WORKER_VALUES")" \
    LOG_LEVEL="$(yq '.env.LOG_LEVEL' "$WORKER_VALUES")" \
    LOCO_NAMESPACE="$LOCO_NAMESPACE" \
    LOCO_BUILD_NAMESPACE="$E2E_BUILD_NAMESPACE" \
    LOCO_CONTROLLER_DEPLOYMENT="$(yq '.controller.name' "$WORKER_VALUES")" \
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
    CORS_ALLOWED_ORIGINS="https://dashboard.e2e.test" \
    CLICKHOUSE_DB="$OBS_DATABASE" \
    CLICKHOUSE_URL="$dsn" \
    CLICKHOUSE_MIGRATOR_URL="$dsn" \
    INGEST_ENABLED=true \
    CLICKHOUSE_INGEST_URL="$dsn" \
    OTLP_GRPC_PORT="$OTLP_GRPC_PORT" \
    OTLP_HTTP_PORT="$OTLP_HTTP_PORT" \
    DEFAULT_LIMIT="100" \
    MAX_LIMIT="1000" \
    QUERY_TIMEOUT_SECONDS="5" \
    MAX_TIME_RANGE_HOURS="24" \
    MAX_CONCURRENT_QUERIES="5" \
    MAX_TAIL_DURATION_MINUTES="10" \
    MAX_CONCURRENT_TAILS="3" \
    TOKEN_CACHE_TTL_SECONDS="5" \
        "$BIN_DIR/loco-obs-proxy" \
        >"$LOG_DIR/obs-proxy.log" 2>&1 &

    echo $! > "$PID_DIR/obs-proxy.pid"

    wait_for "Obs proxy readiness" "$READY_TIMEOUT" curl -sf "http://127.0.0.1:${OBS_PROXY_PORT}/readyz"
    log_ok "Observability proxy running on port ${OBS_PROXY_PORT}"
}

# ─── Test Runner ────────────────────────────────────────────────────────────

run_tests() {
    log_step "Running e2e tests..."
    echo ""

    local test_files=("$SCRIPT_DIR"/suites/*.sh)
    local counts_file="$LOG_DIR/counts"
    if [ ${#test_files[@]} -eq 0 ]; then
        log_warn "No test files found in tests/e2e/suites/"
        return 0
    fi

    for test_file in "${test_files[@]}"; do
        [ -f "$test_file" ] || continue
        local test_name
        test_name="$(basename "$test_file" .sh)"

        if ! suite_selected "$test_name" ${TEST_FILTERS[@]+"${TEST_FILTERS[@]}"}; then
            log_info "Skipping ${test_name} (filtered)"
            continue
        fi

        echo "────────────────────────────────────────"
        log_step "Running: ${test_name}"
        echo "────────────────────────────────────────"

        # Source the test file and run all test_ functions
        (
            source "$test_file"

            if [ -n "${E2E_SKIP_REASON:-}" ]; then
                log_warn "Skipping ${test_name}: ${E2E_SKIP_REASON}"
                E2E_SKIP=$((E2E_SKIP + 1))
            else
                # Find and run all test_ functions
                local funcs
                funcs=$(declare -F | awk '{print $3}' | grep '^test_' || true)
                for func in $funcs; do
                    log_info "  ${func}..."
                    if ! "$func"; then
                        log_error "  ${func} had failures"
                    fi
                done
            fi

            echo "$E2E_PASS $E2E_FAIL $E2E_SKIP" > "$counts_file"
        )
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
    setup_postgres
    setup_openbao
    setup_clickhouse
    run_migrations
    setup_registry
    load_builder_images
    load_controller_image
    install_worker
    reset_cluster_state
    if [ "$OBSERVABILITY" = true ]; then
        forward_observability_ports
    fi
    await_job build
    start_api
    if [ "$(e2e_psql 'SELECT count(*) FROM clusters')" != 0 ]; then
        log_error "Cluster registry must be empty before enrollment"
        return 1
    fi
    seed_cluster_admin
    create_e2e_cluster
    rm -f "$LOG_DIR/agent.log" "$AGENT_EXPECTED_ERRORS"
    start_agent
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
