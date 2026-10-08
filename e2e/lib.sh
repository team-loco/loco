#!/usr/bin/env bash
# Shared helpers for e2e tests

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Counters
E2E_PASS=0
E2E_FAIL=0
E2E_SKIP=0

PROCESS_STOP_SECONDS=15

log_info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
log_ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }
log_step()  { echo -e "${BLUE}[STEP]${NC}  $*"; }

log_group() {
    if [ "${GITHUB_ACTIONS:-}" = true ]; then
        echo "::group::$*"
    else
        echo -e "${BLUE}──── $*${NC}"
    fi
}

log_group_end() {
    if [ "${GITHUB_ACTIONS:-}" = true ]; then
        echo "::endgroup::"
    fi
}

# Wait for a condition to become true.
# Usage: wait_for "description" <max_seconds> <command...>
wait_for() {
    local desc="$1"
    local max_wait="$2"
    shift 2

    log_info "Waiting for ${desc} (up to ${max_wait}s)..."
    local elapsed=0
    while [ $elapsed -lt "$max_wait" ]; do
        if "$@" >/dev/null 2>&1; then
            log_ok "${desc} ready (${elapsed}s)"
            return 0
        fi
        sleep 2
        elapsed=$((elapsed + 2))
    done
    log_error "${desc} not ready after ${max_wait}s"
    return 1
}

# Assert a command succeeds.
# Usage: assert "description" <command...>
assert() {
    local desc="$1"
    shift
    if "$@" >/dev/null 2>&1; then
        log_ok "PASS: ${desc}"
        E2E_PASS=$((E2E_PASS + 1))
        return 0
    else
        log_error "FAIL: ${desc}"
        E2E_FAIL=$((E2E_FAIL + 1))
        return 1
    fi
}

# Assert command output contains a string.
# Usage: assert_contains "description" "expected_substring" <command...>
assert_contains() {
    local desc="$1"
    local expected="$2"
    shift 2
    local output
    if output=$("$@" 2>&1) && grep -q -- "$expected" <<<"$output"; then
        log_ok "PASS: ${desc}"
        E2E_PASS=$((E2E_PASS + 1))
        return 0
    else
        log_error "FAIL: ${desc}"
        log_error "  Expected to contain: ${expected}"
        log_error "  Got: ${output}"
        E2E_FAIL=$((E2E_FAIL + 1))
        return 1
    fi
}

# Assert a command fails (non-zero exit).
# Usage: assert_fails "description" <command...>
assert_fails() {
    local desc="$1"
    shift
    if "$@" >/dev/null 2>&1; then
        log_error "FAIL: ${desc} (expected failure but succeeded)"
        E2E_FAIL=$((E2E_FAIL + 1))
        return 1
    else
        log_ok "PASS: ${desc}"
        E2E_PASS=$((E2E_PASS + 1))
        return 0
    fi
}

# Run docker compose against the e2e project of the repository's compose.yaml.
e2e_compose() {
    docker compose -f "$E2E_ROOT_DIR/compose.yaml" -f "$E2E_ROOT_DIR/e2e/compose.kind.yaml" \
        -p "$E2E_COMPOSE_PROJECT" "$@"
}

# Query the e2e Postgres database, with psql inside the Postgres container.
# Usage: e2e_psql "SELECT ..."
e2e_psql() {
    e2e_compose exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -t -A -c "$1"
}

registry_manifest_status() {
    local user=$1 repository=$2 digest=$3
    local auth=()
    if [ -n "$user" ]; then
        auth=(-u "$user")
    fi
    curl -s -o /dev/null -w '%{http_code}' ${auth[@]+"${auth[@]}"} \
        -H 'Accept: application/vnd.oci.image.manifest.v1+json' \
        -H 'Accept: application/vnd.oci.image.index.v1+json' \
        -H 'Accept: application/vnd.docker.distribution.manifest.v2+json' \
        "${E2E_REGISTRY_URL}/v2/${repository}/manifests/${digest}"
}

# Print test summary.
print_summary() {
    echo ""
    echo "=============================="
    echo "  E2E Test Results"
    echo "=============================="
    echo -e "  ${GREEN}Passed:${NC}  ${E2E_PASS}"
    echo -e "  ${RED}Failed:${NC}  ${E2E_FAIL}"
    echo -e "  ${YELLOW}Skipped:${NC} ${E2E_SKIP}"
    echo "=============================="
    echo ""
    if [ "$E2E_FAIL" -gt 0 ]; then
        log_error "Some tests failed!"
        return 1
    else
        log_ok "All tests passed!"
        return 0
    fi
}

# Kill a process by PID file, if it exists.
kill_pid_file() {
    local pidfile="$1"
    if [ -f "$pidfile" ]; then
        local pid
        pid=$(cat "$pidfile")
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            local waited=0
            while kill -0 "$pid" 2>/dev/null && [ "$waited" -lt "$PROCESS_STOP_SECONDS" ]; do
                sleep 1
                waited=$((waited + 1))
            done
            if kill -0 "$pid" 2>/dev/null; then
                kill -9 "$pid" 2>/dev/null || true
            fi
            wait "$pid" 2>/dev/null || true
        fi
        rm -f "$pidfile"
    fi
}
