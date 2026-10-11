#!/usr/bin/env bash
# E2E test orchestrator for Loco
# Usage: ./e2e/run.sh [--no-teardown] [--skip-build] [--teardown-only] [--builds-disabled] [test-filter]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

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
KUBECONFIG_FILE="$RUN_DIR/kubeconfig"

PORT_RANGE_START=20000
PORT_BLOCKS=1000
PORT_BLOCK_SIZE=10
RUN_HASH=$(cksum <<<"$RUN_ID" | awk '{print $1}')
PORT_BASE=$((PORT_RANGE_START + RUN_HASH % PORT_BLOCKS * PORT_BLOCK_SIZE))
API_PORT=$PORT_BASE
OBS_PROXY_PORT=$((PORT_BASE + 1))
S3_PORT=$((PORT_BASE + 2))
CLICKHOUSE_PORT=$((PORT_BASE + 3))
READY_TIMEOUT=120
LOG_TAIL_LINES=200

KIND_CLUSTER_NAME="$RUN_NAME"
KUBE_CONTEXT="kind-${KIND_CLUSTER_NAME}"
PG_USER="loco_e2e"
PG_PASS="loco_e2e_pass"
PG_DB="loco_e2e"
E2E_VERSION="e2e"
CONTROLLER_REPOSITORY="loco-controller"
BUILDER_REPOSITORY="loco-builder"
OBS_VALUES="$ROOT_DIR/charts/loco-obs/values.yaml"
CLICKHOUSE_IMAGE=$(yq '.clickhouse.clickhouse.image.repository + ":" + .clickhouse.clickhouse.image.tag' "$OBS_VALUES")
CLICKHOUSE_CONTAINER="${RUN_NAME}-clickhouse"
CLICKHOUSE_USER="loco_e2e"
CLICKHOUSE_PASS="loco_e2e_pass"
BUILDKIT_IMAGE=$(yq '.builds.buildkitImage.repository + ":" + .builds.buildkitImage.tag' "$ROOT_DIR/charts/loco-operator/values.yaml")
GATEWAY_API_VERSION=$(awk '$1 == "sigs.k8s.io/gateway-api" { print $2 }' "$ROOT_DIR/controller/go.mod")
AGENT_TOKEN="e2e-test-token-do-not-use-in-production"
USER_TOKEN="loco_s_e2e-test-session-do-not-use-in-production"
LOCO_NAMESPACE="loco-system"
IMAGE_RETENTION=1
IMAGE_SWEEP_INTERVAL=5s
SEEDED_CLUSTER_ID="00000000-0000-7000-8000-000000000005"

export E2E_ROOT_DIR="$ROOT_DIR"
export E2E_VERSION
export E2E_RUN_ID="$RUN_ID"
export E2E_BIN_DIR="$BIN_DIR"
export E2E_LOG_DIR="$LOG_DIR"
export E2E_COMPOSE_PROJECT="$RUN_NAME"
export POSTGRES_USER="$PG_USER" POSTGRES_PASSWORD="$PG_PASS" POSTGRES_DB="$PG_DB"
export POSTGRES_PORT="127.0.0.1:" REGISTRY_PORT="127.0.0.1:" S3_PORT
export E2E_API_URL="http://127.0.0.1:${API_PORT}"
export E2E_AGENT_TOKEN="$AGENT_TOKEN"
export E2E_USER_TOKEN="$USER_TOKEN"
export E2E_KIND_CLUSTER="$KIND_CLUSTER_NAME"
export E2E_LOCO_NAMESPACE="$LOCO_NAMESPACE"
export E2E_OBS_PROXY_PORT="$OBS_PROXY_PORT"
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
export E2E_BUILD_NAMESPACE="loco-builds"
export E2E_BUILD_WORK_DIR="$LOG_DIR/builds"
E2E_AWS_CLI_IMAGE=$(awk '$1 == "FROM" { print $2 }' "$SCRIPT_DIR/fixtures/aws-cli/Dockerfile")
E2E_PUBLIC_IMAGE=$(awk '$1 == "FROM" { split($2, ref, "@"); print ref[1] }' "$SCRIPT_DIR/fixtures/public-image/Dockerfile")
export E2E_AWS_CLI_IMAGE E2E_PUBLIC_IMAGE

source "$SCRIPT_DIR/lib.sh"

# Parse flags
NO_TEARDOWN=false
SKIP_BUILD=false
TEARDOWN_ONLY=false
BUILDS_ENABLED=true
TEST_FILTER=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-teardown)  NO_TEARDOWN=true; shift ;;
        --skip-build)   SKIP_BUILD=true; shift ;;
        --teardown-only) TEARDOWN_ONLY=true; shift ;;
        --builds-disabled) BUILDS_ENABLED=false; shift ;;
        *)              TEST_FILTER="$1"; shift ;;
    esac
done

kube() {
    kubectl --kubeconfig "$KUBECONFIG_FILE" --context "$KUBE_CONTEXT" "$@"
}

cluster_exists() {
    kind get clusters 2>/dev/null | grep -qx "$KIND_CLUSTER_NAME"
}

stop_processes() {
    kill_pid_file "$PID_DIR/obs-proxy.pid"
    kill_pid_file "$PID_DIR/agent.pid"
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

    docker rm -f "$CLICKHOUSE_CONTAINER" >/dev/null 2>&1 || true

    log_info "Removing compose project ${E2E_COMPOSE_PROJECT}..."
    e2e_compose --profile tools down -v --remove-orphans >/dev/null 2>&1 || true

    local repository
    for repository in "$CONTROLLER_REPOSITORY" "$BUILDER_REPOSITORY"; do
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
}

on_exit() {
    local status=$?
    if [ "$status" -ne 0 ]; then
        log_error "Run ${RUN_ID} failed (exit ${status}); component logs follow"
        dump_logs
    fi
    if [ "$NO_TEARDOWN" = true ]; then
        log_info "Leaving run ${RUN_ID} running (cluster ${KIND_CLUSTER_NAME}, compose project ${E2E_COMPOSE_PROJECT})."
        log_info "Rerun against it: ./e2e/run.sh --no-teardown [--skip-build] <test-filter>"
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
    require_free_port "$OBS_PROXY_PORT" "observability proxy"
    if ! docker port "$CLICKHOUSE_CONTAINER" 9000/tcp >/dev/null 2>&1; then
        require_free_port "$CLICKHOUSE_PORT" "ClickHouse"
    fi
    if ! e2e_compose port s3 "$S3_PORT" >/dev/null 2>&1; then
        require_free_port "$S3_PORT" "source bucket"
    fi
    log_ok "Ports free"
}

# ─── Setup ──────────────────────────────────────────────────────────────────

setup_dirs() {
    mkdir -p "$BIN_DIR" "$LOG_DIR" "$PID_DIR"
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

clickhouse_answers() {
    docker exec "$CLICKHOUSE_CONTAINER" clickhouse-client --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASS" -q "SELECT 1"
}

setup_clickhouse() {
    log_step "Setting up ClickHouse..."
    docker rm -f "$CLICKHOUSE_CONTAINER" >/dev/null 2>&1 || true
    docker run -d --name "$CLICKHOUSE_CONTAINER" -p "127.0.0.1:${CLICKHOUSE_PORT}:9000" \
        -e CLICKHOUSE_USER="$CLICKHOUSE_USER" -e CLICKHOUSE_PASSWORD="$CLICKHOUSE_PASS" \
        "$CLICKHOUSE_IMAGE" >/dev/null
    wait_for "ClickHouse to answer queries" "$READY_TIMEOUT" clickhouse_answers
    log_ok "ClickHouse ready on port ${CLICKHOUSE_PORT}"
}

run_migrations() {
    log_step "Resetting the database, running migrations and seeding test data..."
    e2e_psql "DROP SCHEMA public CASCADE; CREATE SCHEMA public;" >/dev/null
    goose -dir "$ROOT_DIR/api/migrations" postgres "$E2E_DATABASE_URL" up >/dev/null
    SEED_FILE=api/seed/e2e.sql AGENT_TOKEN="$AGENT_TOKEN" e2e_compose run --rm seed >/dev/null
    log_ok "Migrations applied and test data seeded"
}

create_namespace() {
    log_step "Creating the ${LOCO_NAMESPACE} namespace..."
    kube create namespace "$LOCO_NAMESPACE" --dry-run=client -o yaml | kube apply -f - >/dev/null
    log_ok "Namespace ready"
}

install_gateway_api() {
    log_step "Installing the Gateway API CRDs..."
    kube apply --server-side \
        -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml" \
        >/dev/null
    log_ok "Gateway API ${GATEWAY_API_VERSION} CRDs installed"
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
    COMPOSE_PROJECT="$E2E_COMPOSE_PROJECT" \
    REGISTRY_PORT="$registry_port" \
    REGISTRY_ALIAS="$E2E_REGISTRY_ALIAS" \
    S3_ALIAS="$E2E_S3_ALIAS" \
        mise run cluster:registry >/dev/null
    BUILD_EGRESS_CIDRS=$(COMPOSE_PROJECT="$E2E_COMPOSE_PROJECT" mise run --quiet cluster:build-egress)
    log_ok "Registry at ${E2E_REGISTRY_HOST} (host port ${registry_port}), source bucket at ${E2E_S3_ENDPOINT}, build egress to ${BUILD_EGRESS_CIDRS}"
}

image_reference() {
    local repository=$1 dockerfile=$2 run_tag="$1:e2e-${RUN_ID}" id
    if [ "$SKIP_BUILD" = true ]; then
        if ! id=$(docker image inspect -f '{{.Id}}' "$run_tag" 2>/dev/null); then
            log_error "No ${run_tag} image to reuse; run once without --skip-build" >&2
            return 1
        fi
    else
        id=$(docker build -q -t "$run_tag" --build-arg VERSION="$E2E_VERSION" \
            -f "$ROOT_DIR/$dockerfile" "$ROOT_DIR")
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

build_builder_images() {
    if [ "$BUILDS_ENABLED" = false ]; then
        log_info "Skipping the builder images (--builds-disabled)"
        return 0
    fi
    log_step "Building the builder image..."
    BUILDER_IMAGE=$(image_reference "$BUILDER_REPOSITORY" builder/Dockerfile)
    load_image "$BUILDER_IMAGE"
    local image
    for image in "$BUILDKIT_IMAGE" "$E2E_AWS_CLI_IMAGE"; do
        if ! docker image inspect "$image" >/dev/null 2>&1; then
            docker pull -q "$image" >/dev/null
        fi
    done
    kind load docker-image "$BUILDKIT_IMAGE" --name "$KIND_CLUSTER_NAME" >/dev/null
    log_ok "Builder ${BUILDER_IMAGE} and BuildKit images loaded into Kind"
}

build_controller_image() {
    log_step "Building the controller image..."
    CONTROLLER_IMAGE=$(image_reference "$CONTROLLER_REPOSITORY" controller/Dockerfile)
    load_image "$CONTROLLER_IMAGE"
    log_ok "Controller image ${CONTROLLER_IMAGE} loaded into Kind"
}

install_operator() {
    log_step "Installing the loco-operator chart (builds.enabled=${BUILDS_ENABLED})..."
    local build_values=(--set builds.enabled=false)
    if [ "$BUILDS_ENABLED" = true ]; then
        build_values=(
            --set builds.builderImage.repository="${BUILDER_IMAGE%%:*}"
            --set builds.builderImage.tag="${BUILDER_IMAGE##*:}"
            --set "builds.privateEgressCIDRs={${BUILD_EGRESS_CIDRS// /,}}"
        )
    fi
    helm upgrade --install loco-operator "$ROOT_DIR/charts/loco-operator" \
        --kubeconfig "$KUBECONFIG_FILE" \
        --kube-context "$KUBE_CONTEXT" \
        --namespace "$LOCO_NAMESPACE" \
        --values "$SCRIPT_DIR/operator-values.yaml" \
        --set controller.image.repository="${CONTROLLER_IMAGE%%:*}" \
        --set controller.image.tag="${CONTROLLER_IMAGE##*:}" \
        "${build_values[@]}" \
        --wait --timeout 3m >/dev/null
    log_ok "Controllers running in Kind"
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
    done < <(kube get namespaces -o name | grep -E '^namespace/(ws-|e2e-)' || true)
    if [ ${#namespaces[@]} -gt 0 ]; then
        kube delete "${namespaces[@]}" --wait >/dev/null
    fi
    log_ok "Cluster state reset"
}

build_binaries() {
    if [ "$SKIP_BUILD" = true ]; then
        log_info "Skipping builds (--skip-build)"
        return 0
    fi

    log_step "Building binaries..."
    local ldflags="-X main.version=${E2E_VERSION}"

    log_info "Building API..."
    (cd "$ROOT_DIR/api" && go build -ldflags "$ldflags" -o "$BIN_DIR/loco-api" .)

    log_info "Building Agent..."
    (cd "$ROOT_DIR/agent" && go build -ldflags "$ldflags" -o "$BIN_DIR/loco-agent" .)

    log_info "Building Observability Proxy..."
    (cd "$ROOT_DIR/observability-proxy" && go build -ldflags "$ldflags" -o "$BIN_DIR/loco-obs-proxy" .)

    log_info "Building the CLI..."
    (cd "$ROOT_DIR" && go build -ldflags "$ldflags" -o "$BIN_DIR/loco" .)

    log_ok "All binaries built"
}

start_api() {
    log_step "Starting API server..."

    DATABASE_URL="$E2E_DATABASE_URL" \
    APP_PORT="127.0.0.1:$API_PORT" \
    DEFAULT_PLATFORM_DOMAIN="e2e.test.local" \
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
        "$BIN_DIR/loco-api" \
        >"$LOG_DIR/api.log" 2>&1 &

    echo $! > "$PID_DIR/api.pid"

    wait_for "API health" "$READY_TIMEOUT" curl -sf "${E2E_API_URL}/health"
    log_ok "API server running on port ${API_PORT}"
}

agent_registered() {
    local version
    version=$(e2e_psql "SELECT agent_version FROM clusters WHERE id = '${SEEDED_CLUSTER_ID}'")
    test "$version" = "$E2E_VERSION"
}

start_agent() {
    log_step "Starting Agent..."

    local core_values="$ROOT_DIR/charts/loco-core/values.yaml"
    CONTROL_PLANE_URL="$E2E_API_URL" \
    AGENT_TOKEN="$AGENT_TOKEN" \
    LOG_LEVEL="$(yq '.env.LOG_LEVEL' "$core_values")" \
    LOCO_NAMESPACE="$LOCO_NAMESPACE" \
    LOCO_BUILD_NAMESPACE="$(yq '.agent.buildNamespace' "$core_values")" \
    LOCO_CONTROLLER_DEPLOYMENT="$(yq '.agent.controllerDeployment' "$core_values")" \
    LOCO_INVENTORY_INTERVAL="$(yq '.agent.inventoryInterval' "$core_values")" \
    LOCO_BUILD_RETENTION="$(yq '.agent.buildRetention' "$core_values")" \
    LOCO_BUILD_COLLECT_INTERVAL="$(yq '.agent.buildCollectInterval' "$core_values")" \
    LOCO_HEARTBEAT_INTERVAL="$(yq '.agent.heartbeatInterval' "$core_values")" \
    LOCO_CLUSTER_QUERY_TIMEOUT="$(yq '.agent.clusterQueryTimeout' "$core_values")" \
    LOCO_RECONCILE_WORKERS="$(yq '.agent.reconcileWorkers' "$core_values")" \
    LOCO_RECONCILE_RETRY_BASE_DELAY="$(yq '.agent.reconcileRetryBaseDelay' "$core_values")" \
    LOCO_RECONCILE_RETRY_MAX_DELAY="$(yq '.agent.reconcileRetryMaxDelay' "$core_values")" \
    LOCO_SYNC_OUTBOUND_BUFFER="$(yq '.agent.syncOutboundBuffer' "$core_values")" \
    LOCO_BUILD_QUEUE_SIZE="$(yq '.agent.buildQueueSize' "$core_values")" \
    LOCO_BUILD_CREATE_RETRY_DELAY="$(yq '.agent.buildCreateRetryDelay' "$core_values")" \
    LOCO_BUILD_CREATE_RETRY_ATTEMPTS="$(yq '.agent.buildCreateRetryAttempts' "$core_values")" \
    LOCO_RECONNECT_BASE_DELAY="$(yq '.agent.reconnectBaseDelay' "$core_values")" \
    LOCO_RECONNECT_MAX_DELAY="$(yq '.agent.reconnectMaxDelay' "$core_values")" \
    LOCO_HEALTHY_STREAM_DURATION="$(yq '.agent.healthyStreamDuration' "$core_values")" \
    KUBECONFIG="$KUBECONFIG_FILE" \
        "$BIN_DIR/loco-agent" \
        >"$LOG_DIR/agent.log" 2>&1 &

    echo $! > "$PID_DIR/agent.pid"

    wait_for "Agent registration" "$READY_TIMEOUT" agent_registered
    log_ok "Agent registered"
}

start_obs_proxy() {
    log_step "Starting Observability Proxy..."

    PORT="$OBS_PROXY_PORT" \
    CONTROL_PLANE_URL="$E2E_API_URL" \
    PROXY_AUTH_TOKEN="$AGENT_TOKEN" \
    CLICKHOUSE_URL="clickhouse://${CLICKHOUSE_USER}:${CLICKHOUSE_PASS}@127.0.0.1:${CLICKHOUSE_PORT}" \
    CLICKHOUSE_MIGRATOR_URL="clickhouse://${CLICKHOUSE_USER}:${CLICKHOUSE_PASS}@127.0.0.1:${CLICKHOUSE_PORT}" \
    CLICKHOUSE_DB="$(yq '.obsProxy.clickhouse.database' "$OBS_VALUES")" \
    CLICKHOUSE_LOGS_TTL="$(yq '.obsProxy.clickhouse.retention.logs' "$OBS_VALUES")" \
    CLICKHOUSE_TRACES_TTL="$(yq '.obsProxy.clickhouse.retention.traces' "$OBS_VALUES")" \
    CLICKHOUSE_METRICS_TTL="$(yq '.obsProxy.clickhouse.retention.metrics' "$OBS_VALUES")" \
    MIGRATION_RETRY_BUDGET="$(yq '.obsProxy.clickhouse.migrations.retryBudgetSeconds' "$OBS_VALUES")s" \
    MIGRATION_RETRY_INTERVAL="$(yq '.obsProxy.clickhouse.migrations.retryIntervalSeconds' "$OBS_VALUES")s" \
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

    local test_files=("$SCRIPT_DIR"/tests/*.sh)
    local counts_file="$LOG_DIR/counts"
    if [ ${#test_files[@]} -eq 0 ]; then
        log_warn "No test files found in e2e/tests/"
        return 0
    fi

    for test_file in "${test_files[@]}"; do
        [ -f "$test_file" ] || continue
        local test_name
        test_name="$(basename "$test_file" .sh)"

        # Apply filter if provided
        if [ -n "$TEST_FILTER" ] && [[ "$test_name" != *"$TEST_FILTER"* ]]; then
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
    check_prerequisites
    stop_processes
    check_ports
    setup_dirs
    setup_kind
    setup_postgres
    setup_clickhouse
    run_migrations
    create_namespace
    install_gateway_api
    setup_registry
    build_builder_images
    build_controller_image
    install_operator
    reset_cluster_state
    build_binaries
    start_api
    start_agent
    start_obs_proxy

    echo ""
    echo "════════════════════════════════════════"
    log_ok "Infrastructure ready"
    echo "════════════════════════════════════════"
    echo ""

    run_tests
    print_summary
}

main
