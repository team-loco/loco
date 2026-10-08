#!/usr/bin/env bash
# E2E test orchestrator for Loco
# Usage: ./e2e/run.sh [--no-teardown] [--skip-build] [--teardown-only] [--builds-disabled] [test-filter]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_DIR="$SCRIPT_DIR/bin"
LOG_DIR="$SCRIPT_DIR/logs"
PID_DIR="$SCRIPT_DIR/pids"
KUBECONFIG_FILE="$SCRIPT_DIR/kubeconfig"

# Config
KIND_CLUSTER_NAME="loco-e2e"
PG_PORT=5433
PG_USER="loco_e2e"
PG_PASS="loco_e2e_pass"
PG_DB="loco_e2e"
API_PORT=8877  # avoid conflict with dev API on 8000
OBS_PROXY_PORT=8878
CONTROLLER_IMAGE="loco-controller:e2e"
BUILDER_IMAGE="loco-builder:e2e"
BUILDKIT_IMAGE=$(yq '.builds.buildkitImage.repository + ":" + .builds.buildkitImage.tag' "$ROOT_DIR/charts/loco-operator/values.yaml")
GATEWAY_API_VERSION=$(awk '$1 == "sigs.k8s.io/gateway-api" { print $2 }' "$ROOT_DIR/controller/go.mod")
AGENT_TOKEN="e2e-test-token-do-not-use-in-production"
USER_TOKEN="loco_s_e2e-test-session-do-not-use-in-production"
LOCO_NAMESPACE="loco-system"
IMAGE_RETENTION=1
IMAGE_SWEEP_INTERVAL=5s

export E2E_ROOT_DIR="$ROOT_DIR"
export E2E_COMPOSE_PROJECT="loco-e2e"
export POSTGRES_USER="$PG_USER" POSTGRES_PASSWORD="$PG_PASS" POSTGRES_DB="$PG_DB" POSTGRES_PORT="$PG_PORT"
export E2E_DATABASE_URL="postgres://${PG_USER}:${PG_PASS}@localhost:${PG_PORT}/${PG_DB}?sslmode=disable"
export E2E_API_URL="http://localhost:${API_PORT}"
export E2E_AGENT_TOKEN="$AGENT_TOKEN"
export E2E_USER_TOKEN="$USER_TOKEN"
export E2E_KIND_CLUSTER="$KIND_CLUSTER_NAME"
export E2E_LOCO_NAMESPACE="$LOCO_NAMESPACE"
export E2E_OBS_PROXY_PORT="$OBS_PROXY_PORT"
export REGISTRY_PORT=5011 S3_PORT=9011
export LOCO_SOURCE_BUCKET="loco-e2e-sources"
export LOCO_SOURCE_BUCKET_ACCESS_KEY_ID="loco-e2e"
export LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY="loco-e2e-secret"
export LOCO_SOURCE_BUCKET_REGION="us-east-1"
export E2E_REGISTRY_PORT="$REGISTRY_PORT"
export E2E_REGISTRY_ALIAS="loco-e2e-registry"
export E2E_REGISTRY_HOST="${E2E_REGISTRY_ALIAS}:5000"
export E2E_REGISTRY_URL="http://localhost:${REGISTRY_PORT}"
export E2E_REGISTRY_API_USER="api:loco-dev-api"
export E2E_REGISTRY_NODES_USER="nodes:loco-dev-nodes"
export E2E_S3_ALIAS="loco-e2e-s3.localhost"
export E2E_S3_ENDPOINT="http://${E2E_S3_ALIAS}:${S3_PORT}"
export E2E_BUILD_NAMESPACE="loco-builds"
export E2E_BUILD_WORK_DIR="$LOG_DIR/builds"
export E2E_AWS_CLI_IMAGE=$(awk '$1 == "FROM" { print $2 }' "$SCRIPT_DIR/fixtures/aws-cli/Dockerfile")
export E2E_PUBLIC_IMAGE=$(awk '$1 == "FROM" { print $2 }' "$SCRIPT_DIR/fixtures/public-image/Dockerfile")

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

# ─── Teardown ───────────────────────────────────────────────────────────────

teardown() {
    log_step "Tearing down e2e infrastructure..."

    # Kill processes
    kill_pid_file "$PID_DIR/obs-proxy.pid"
    kill_pid_file "$PID_DIR/api.pid"
    kill_pid_file "$PID_DIR/agent.pid"

    # Remove Kind cluster
    if kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
        log_info "Deleting Kind cluster ${KIND_CLUSTER_NAME}..."
        kind delete cluster --name "$KIND_CLUSTER_NAME" --kubeconfig "$KUBECONFIG_FILE"
    fi
    rm -f "$KUBECONFIG_FILE"

    # Remove Postgres and its volume
    log_info "Removing Postgres..."
    e2e_compose down -v >/dev/null 2>&1 || true

    # Clean up dirs
    rm -rf "$BIN_DIR" "$LOG_DIR" "$PID_DIR"

    log_ok "Teardown complete"
}

if [ "$TEARDOWN_ONLY" = true ]; then
    teardown
    exit 0
fi

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

# ─── Setup ──────────────────────────────────────────────────────────────────

setup_dirs() {
    mkdir -p "$BIN_DIR" "$LOG_DIR" "$PID_DIR"
}

setup_kind() {
    log_step "Setting up Kind cluster..."
    if kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
        log_info "Kind cluster ${KIND_CLUSTER_NAME} already exists, reusing"
    else
        kind create cluster --config "$SCRIPT_DIR/kind-e2e.yml" --name "$KIND_CLUSTER_NAME" --kubeconfig "$KUBECONFIG_FILE"
        log_ok "Kind cluster created"
    fi

    # Point kubectl at the e2e cluster
    kind get kubeconfig --name "$KIND_CLUSTER_NAME" >"$KUBECONFIG_FILE"
    export KUBECONFIG="$KUBECONFIG_FILE"
    kubectl cluster-info --context "kind-${KIND_CLUSTER_NAME}" >/dev/null 2>&1
    log_ok "kubectl context set to kind-${KIND_CLUSTER_NAME}"
}

setup_postgres() {
    log_step "Setting up Postgres..."
    e2e_compose up -d --wait postgres
    log_ok "Postgres ready"
}

run_migrations() {
    log_step "Running migrations and seeding test data..."
    goose -dir "$E2E_ROOT_DIR/api/migrations" postgres "$E2E_DATABASE_URL" up >/dev/null
    SEED_FILE=api/seed/e2e.sql AGENT_TOKEN="$AGENT_TOKEN" e2e_compose run --rm seed >/dev/null
    log_ok "Migrations applied and test data seeded"
}

create_namespace() {
    log_step "Creating the ${LOCO_NAMESPACE} namespace..."
    kubectl create namespace "$LOCO_NAMESPACE" --context "kind-${KIND_CLUSTER_NAME}" 2>/dev/null || true
    log_ok "Namespace ready"
}

install_gateway_api() {
    log_step "Installing the Gateway API CRDs..."
    kubectl apply --server-side --context "kind-${KIND_CLUSTER_NAME}" \
        -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml" \
        >/dev/null
    log_ok "Gateway API ${GATEWAY_API_VERSION} CRDs installed"
}

setup_registry() {
    log_step "Starting the registry and the source bucket..."
    e2e_compose up -d --wait registry s3
    KIND_CLUSTER="$KIND_CLUSTER_NAME" \
    COMPOSE_PROJECT="$E2E_COMPOSE_PROJECT" \
    REGISTRY_ALIAS="$E2E_REGISTRY_ALIAS" \
    S3_ALIAS="$E2E_S3_ALIAS" \
        mise run cluster:registry >/dev/null
    BUILD_EGRESS_CIDRS=$(COMPOSE_PROJECT="$E2E_COMPOSE_PROJECT" mise run --quiet cluster:build-egress)
    log_ok "Registry at ${E2E_REGISTRY_HOST}, source bucket at ${E2E_S3_ENDPOINT}, build egress to ${BUILD_EGRESS_CIDRS}"
}

build_builder_images() {
    if [ "$BUILDS_ENABLED" = false ]; then
        log_info "Skipping the builder images (--builds-disabled)"
        return 0
    fi
    if [ "$SKIP_BUILD" = true ]; then
        log_info "Skipping builder image build (--skip-build)"
    else
        log_step "Building the builder image..."
        docker build -q -t "$BUILDER_IMAGE" -f "$ROOT_DIR/builder/Dockerfile" "$ROOT_DIR" >/dev/null
        log_ok "Builder image built"
    fi
    local image
    for image in "$BUILDKIT_IMAGE" "$E2E_AWS_CLI_IMAGE"; do
        if ! docker image inspect "$image" >/dev/null 2>&1; then
            docker pull -q "$image" >/dev/null
        fi
    done
    kind load docker-image "$BUILDER_IMAGE" "$BUILDKIT_IMAGE" --name "$KIND_CLUSTER_NAME" >/dev/null
    log_ok "Builder and BuildKit images loaded into Kind"
}

build_controller_image() {
    if [ "$SKIP_BUILD" = true ]; then
        log_info "Skipping controller image build (--skip-build)"
    else
        log_step "Building the controller image..."
        docker build -q -t "$CONTROLLER_IMAGE" -f "$ROOT_DIR/controller/Dockerfile" "$ROOT_DIR" >/dev/null
        log_ok "Controller image built"
    fi
    kind load docker-image "$CONTROLLER_IMAGE" --name "$KIND_CLUSTER_NAME" >/dev/null
    log_ok "Controller image loaded into Kind"
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
        --kube-context "kind-${KIND_CLUSTER_NAME}" \
        --namespace "$LOCO_NAMESPACE" \
        --values "$SCRIPT_DIR/operator-values.yaml" \
        --set controller.image.repository="${CONTROLLER_IMAGE%%:*}" \
        --set controller.image.tag="${CONTROLLER_IMAGE##*:}" \
        "${build_values[@]}" \
        --wait --timeout 3m >/dev/null
    log_ok "Controllers running in Kind"
}

build_binaries() {
    if [ "$SKIP_BUILD" = true ]; then
        log_info "Skipping builds (--skip-build)"
        return 0
    fi

    log_step "Building binaries..."

    log_info "Building API..."
    (cd "$ROOT_DIR/api" && go build -o "$BIN_DIR/loco-api" .)

    log_info "Building Agent..."
    (cd "$ROOT_DIR/agent" && go build -ldflags "-X main.version=e2e-test" -o "$BIN_DIR/loco-agent" .)

    log_info "Building Observability Proxy..."
    (cd "$ROOT_DIR/observability-proxy" && go build -o "$BIN_DIR/loco-obs-proxy" .)

    log_info "Building the CLI..."
    (cd "$ROOT_DIR" && go build -o "$BIN_DIR/loco" .)

    log_ok "All binaries built"
}

start_api() {
    log_step "Starting API server..."

    DATABASE_URL="$E2E_DATABASE_URL" \
    APP_PORT=":$API_PORT" \
    DEFAULT_PLATFORM_DOMAIN="e2e.test.local" \
    APP_ENV="test" \
    LOG_LEVEL="-4" \
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

    wait_for "API health" 15 curl -sf "${E2E_API_URL}/health"
    log_ok "API server running on port ${API_PORT}"
}

start_agent() {
    log_step "Starting Agent..."

    local core_values="$ROOT_DIR/charts/loco-core/values.yaml"
    CONTROL_PLANE_URL="$E2E_API_URL" \
    AGENT_TOKEN="$AGENT_TOKEN" \
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

    # Give agent time to register and open streams
    sleep 3

    # Verify agent registered by checking DB
    local heartbeat
    heartbeat=$(e2e_psql "SELECT agent_version FROM clusters WHERE id = '00000000-0000-7000-8000-000000000005'" 2>/dev/null || echo "")
    if [ "$heartbeat" = "e2e-test" ]; then
        log_ok "Agent registered successfully"
    else
        log_warn "Agent may not have registered yet (agent_version: '${heartbeat}')"
    fi
}

start_obs_proxy() {
    log_step "Starting Observability Proxy..."

    PORT="$OBS_PROXY_PORT" \
    CONTROL_PLANE_URL="$E2E_API_URL" \
    PROXY_AUTH_TOKEN="$AGENT_TOKEN" \
    CLICKHOUSE_URL="clickhouse://localhost:9000" \
    CLICKHOUSE_DB="default" \
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

    wait_for "Obs proxy health" 10 curl -sf "http://localhost:${OBS_PROXY_PORT}/healthz"
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

    # Ensure clean state on exit (unless --no-teardown)
    if [ "$NO_TEARDOWN" = false ]; then
        trap teardown EXIT
    else
        log_warn "--no-teardown: infrastructure will persist after tests"
        trap 'log_info "Leaving infrastructure running. Clean up with: mise run e2e:teardown"' EXIT
    fi

    export E2E_BUILDS_ENABLED="$BUILDS_ENABLED"
    check_prerequisites
    setup_dirs
    setup_kind
    setup_postgres
    run_migrations
    create_namespace
    install_gateway_api
    setup_registry
    build_builder_images
    build_controller_image
    install_operator
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
