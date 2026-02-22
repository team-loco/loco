#!/usr/bin/env bash
# E2E test orchestrator for Loco
# Usage: ./e2e/run.sh [--no-teardown] [--skip-build] [--teardown-only] [test-filter]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_DIR="$SCRIPT_DIR/bin"
LOG_DIR="$SCRIPT_DIR/logs"
PID_DIR="$SCRIPT_DIR/pids"

# Config
KIND_CLUSTER_NAME="loco-e2e"
PG_CONTAINER_NAME="loco-e2e-postgres"
PG_PORT=5433
PG_USER="loco_e2e"
PG_PASS="loco_e2e_pass"
PG_DB="loco_e2e"
API_PORT=8877  # avoid conflict with dev API on 8000
AGENT_TOKEN="e2e-test-token-do-not-use-in-production"
LOCO_NAMESPACE="loco-system"

export E2E_DATABASE_URL="postgres://${PG_USER}:${PG_PASS}@localhost:${PG_PORT}/${PG_DB}?sslmode=disable"
export E2E_API_URL="http://localhost:${API_PORT}"
export E2E_AGENT_TOKEN="$AGENT_TOKEN"
export E2E_KIND_CLUSTER="$KIND_CLUSTER_NAME"
export E2E_LOCO_NAMESPACE="$LOCO_NAMESPACE"

source "$SCRIPT_DIR/lib.sh"

# Parse flags
NO_TEARDOWN=false
SKIP_BUILD=false
TEARDOWN_ONLY=false
TEST_FILTER=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-teardown)  NO_TEARDOWN=true; shift ;;
        --skip-build)   SKIP_BUILD=true; shift ;;
        --teardown-only) TEARDOWN_ONLY=true; shift ;;
        *)              TEST_FILTER="$1"; shift ;;
    esac
done

# ─── Teardown ───────────────────────────────────────────────────────────────

teardown() {
    log_step "Tearing down e2e infrastructure..."

    # Kill processes
    kill_pid_file "$PID_DIR/api.pid"
    kill_pid_file "$PID_DIR/agent.pid"
    kill_pid_file "$PID_DIR/controller.pid"

    # Remove Kind cluster
    if kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
        log_info "Deleting Kind cluster ${KIND_CLUSTER_NAME}..."
        kind delete cluster --name "$KIND_CLUSTER_NAME"
    fi

    # Remove Postgres container
    if docker ps -a --format '{{.Names}}' | grep -q "^${PG_CONTAINER_NAME}$"; then
        log_info "Removing Postgres container..."
        docker rm -f "$PG_CONTAINER_NAME" >/dev/null 2>&1 || true
    fi

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

    for cmd in kind docker kubectl psql go; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            missing+=("$cmd")
        fi
    done

    if [ ${#missing[@]} -gt 0 ]; then
        log_error "Missing required tools: ${missing[*]}"
        exit 1
    fi

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
        kind create cluster --config "$SCRIPT_DIR/kind-e2e.yml" --name "$KIND_CLUSTER_NAME"
        log_ok "Kind cluster created"
    fi

    # Point kubectl at the e2e cluster
    export KUBECONFIG="$(kind get kubeconfig-path --name "$KIND_CLUSTER_NAME" 2>/dev/null || echo "$HOME/.kube/config")"
    kubectl config use-context "kind-${KIND_CLUSTER_NAME}" >/dev/null 2>&1
    kubectl cluster-info --context "kind-${KIND_CLUSTER_NAME}" >/dev/null 2>&1
    log_ok "kubectl context set to kind-${KIND_CLUSTER_NAME}"
}

setup_postgres() {
    log_step "Setting up Postgres..."
    if docker ps --format '{{.Names}}' | grep -q "^${PG_CONTAINER_NAME}$"; then
        log_info "Postgres container already running, reusing"
        return 0
    fi

    # Remove stale container if exists
    docker rm -f "$PG_CONTAINER_NAME" >/dev/null 2>&1 || true

    docker run -d \
        --name "$PG_CONTAINER_NAME" \
        -e POSTGRES_USER="$PG_USER" \
        -e POSTGRES_PASSWORD="$PG_PASS" \
        -e POSTGRES_DB="$PG_DB" \
        -p "${PG_PORT}:5432" \
        postgres:18-alpine \
        >/dev/null

    wait_for "Postgres" 30 pg_isready -h localhost -p "$PG_PORT" -U "$PG_USER"
}

run_migrations() {
    log_step "Running migrations..."
    for migration in "$ROOT_DIR"/api/migrations/*.sql; do
        log_info "Applying $(basename "$migration")..."
        psql "$E2E_DATABASE_URL" -f "$migration" >/dev/null 2>&1
    done
    log_ok "Migrations applied"
}

seed_data() {
    log_step "Seeding test data..."
    psql "$E2E_DATABASE_URL" -f "$SCRIPT_DIR/seed.sql" >/dev/null 2>&1
    log_ok "Test data seeded"
}

install_crds() {
    log_step "Installing CRDs into Kind cluster..."
    local crd_dir="$ROOT_DIR/controller/config/crd/bases"
    if [ -d "$crd_dir" ]; then
        kubectl apply -f "$crd_dir/" --context "kind-${KIND_CLUSTER_NAME}"
        log_ok "CRDs installed"
    else
        log_warn "CRD directory not found at ${crd_dir}, skipping"
    fi

    # Create the loco namespace
    kubectl create namespace "$LOCO_NAMESPACE" --context "kind-${KIND_CLUSTER_NAME}" 2>/dev/null || true
}

build_binaries() {
    if [ "$SKIP_BUILD" = true ]; then
        log_info "Skipping builds (--skip-build)"
        return 0
    fi

    log_step "Building binaries..."

    log_info "Building API..."
    (cd "$ROOT_DIR" && go build -o "$BIN_DIR/loco-api" ./api)

    log_info "Building Agent..."
    (cd "$ROOT_DIR" && go build -o "$BIN_DIR/loco-agent" ./agent)

    log_info "Building Controller..."
    (cd "$ROOT_DIR/controller" && go build -o "$BIN_DIR/loco-controller" ./cmd)

    log_ok "All binaries built"
}

start_api() {
    log_step "Starting API server..."

    DATABASE_URL="$E2E_DATABASE_URL" \
    PORT="$API_PORT" \
    LOCO_NAMESPACE="$LOCO_NAMESPACE" \
    LOCO_DOMAIN_BASE="e2e.test.local" \
    APP_ENV="test" \
    LOG_LEVEL="-4" \
        "$BIN_DIR/loco-api" \
        >"$LOG_DIR/api.log" 2>&1 &

    echo $! > "$PID_DIR/api.pid"

    wait_for "API health" 15 curl -sf "${E2E_API_URL}/health"
    log_ok "API server running on port ${API_PORT}"
}

start_agent() {
    log_step "Starting Agent..."

    CONTROL_PLANE_URL="$E2E_API_URL" \
    AGENT_TOKEN="$AGENT_TOKEN" \
    REGION="us-east-1" \
    AGENT_VERSION="e2e-test" \
    KUBECONFIG="$(kind get kubeconfig-path --name "$KIND_CLUSTER_NAME" 2>/dev/null || echo "$HOME/.kube/config")" \
        "$BIN_DIR/loco-agent" \
        >"$LOG_DIR/agent.log" 2>&1 &

    echo $! > "$PID_DIR/agent.pid"

    # Give agent time to register and open streams
    sleep 3

    # Verify agent registered by checking DB
    local heartbeat
    heartbeat=$(psql "$E2E_DATABASE_URL" -t -A -c "SELECT agent_version FROM clusters WHERE id = 1" 2>/dev/null || echo "")
    if [ "$heartbeat" = "e2e-test" ]; then
        log_ok "Agent registered successfully"
    else
        log_warn "Agent may not have registered yet (agent_version: '${heartbeat}')"
    fi
}

start_controller() {
    log_step "Starting Controller..."

    KUBECONFIG="$(kind get kubeconfig-path --name "$KIND_CLUSTER_NAME" 2>/dev/null || echo "$HOME/.kube/config")" \
        "$BIN_DIR/loco-controller" \
        >"$LOG_DIR/controller.log" 2>&1 &

    echo $! > "$PID_DIR/controller.pid"

    sleep 2
    log_ok "Controller started"
}

# ─── Test Runner ────────────────────────────────────────────────────────────

run_tests() {
    log_step "Running e2e tests..."
    echo ""

    local test_files=("$SCRIPT_DIR"/tests/*.sh)
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

            # Find and run all test_ functions
            local funcs
            funcs=$(declare -F | awk '{print $3}' | grep '^test_' || true)
            for func in $funcs; do
                log_info "  ${func}..."
                if ! "$func"; then
                    log_error "  ${func} had failures"
                fi
            done
        )

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
        trap 'log_info "Leaving infrastructure running. Clean up with: make e2e-teardown"' EXIT
    fi

    check_prerequisites
    setup_dirs
    setup_kind
    setup_postgres
    run_migrations
    seed_data
    install_crds
    build_binaries
    start_api
    start_agent
    start_controller

    echo ""
    echo "════════════════════════════════════════"
    log_ok "Infrastructure ready"
    echo "════════════════════════════════════════"
    echo ""

    run_tests
    print_summary
}

main
