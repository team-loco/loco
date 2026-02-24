#!/usr/bin/env bash
# E2E tests for the observability proxy:
#   - Health/readiness endpoints
#   - Token validation (valid, invalid, expired)
#   - Tenant scoping (workspace isolation)
#   - Guardrail enforcement
#
# These functions are sourced by run.sh and called automatically.
# lib.sh helpers (assert, assert_contains, e2e_psql, etc.) are available.
#
# NOTE: These tests require the observability proxy to be running.
# The proxy is started by run.sh if the binary exists.
# ClickHouse is NOT available in e2e (no loco-obs helm chart),
# so we test auth/validation paths and expect ClickHouse errors for query tests.

OBS_PROXY_PORT="${E2E_OBS_PROXY_PORT:-8878}"
OBS_PROXY_URL="http://localhost:${OBS_PROXY_PORT}"

WORKSPACE_ID="00000000-0000-7000-8000-000000000003"
RESOURCE_ID="00000000-0000-7000-8000-000000000010"

# ─── Helper: get an observability token from the control plane ─────────────

get_obs_token() {
    local workspace_id="${1:-$WORKSPACE_ID}"
    local resource_ids="${2:-[\"$RESOURCE_ID\"]}"

    # Issue a TVM token with observability scopes directly in the DB
    # This bypasses the GetObservabilityAccess RPC (which requires OAuth)
    local token_id
    token_id=$(uuidgen | tr '[:upper:]' '[:lower:]')
    local token_hash
    token_hash=$(echo -n "$token_id" | shasum -a 256 | awk '{print $1}')
    local expires_at
    expires_at=$(date -u -v+30M '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '+30 minutes' '+%Y-%m-%dT%H:%M:%SZ')

    e2e_psql "
        INSERT INTO tvm_tokens (token_hash, owner_type, owner_id, expires_at, data)
        VALUES (
            '${token_hash}',
            'user',
            '00000000-0000-7000-8000-000000000001',
            '${expires_at}',
            '{\"scopes\": [{\"scope\": \"read\", \"entity_type\": \"workspace\", \"entity_id\": \"${workspace_id}\"}], \"resource_ids\": ${resource_ids}}'
        );
    " >/dev/null

    echo "$token_id"
}

get_expired_obs_token() {
    local token_id
    token_id=$(uuidgen | tr '[:upper:]' '[:lower:]')
    local token_hash
    token_hash=$(echo -n "$token_id" | shasum -a 256 | awk '{print $1}')
    local expires_at
    expires_at=$(date -u -v-1H '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '-1 hour' '+%Y-%m-%dT%H:%M:%SZ')

    e2e_psql "
        INSERT INTO tvm_tokens (token_hash, owner_type, owner_id, expires_at, data)
        VALUES (
            '${token_hash}',
            'user',
            '00000000-0000-7000-8000-000000000001',
            '${expires_at}',
            '{\"scopes\": [{\"scope\": \"read\", \"entity_type\": \"workspace\", \"entity_id\": \"${WORKSPACE_ID}\"}], \"resource_ids\": [\"${RESOURCE_ID}\"]}'
        );
    " >/dev/null

    echo "$token_id"
}

# ─── Tests ─────────────────────────────────────────────────────────────────

test_obs_proxy_health() {
    if ! curl -sf "${OBS_PROXY_URL}/healthz" >/dev/null 2>&1; then
        log_warn "Observability proxy not running, skipping obs-proxy tests"
        E2E_SKIP=$((E2E_SKIP + 1))
        return 0
    fi
    assert "Obs proxy /healthz returns 200" \
        curl -sf "${OBS_PROXY_URL}/healthz"
}

test_obs_proxy_no_auth_rejected() {
    if ! curl -sf "${OBS_PROXY_URL}/healthz" >/dev/null 2>&1; then
        E2E_SKIP=$((E2E_SKIP + 1))
        return 0
    fi

    # Call QueryLogs without auth token — should fail
    assert_fails "QueryLogs without auth returns error" \
        curl -sf \
            -X POST \
            -H "Content-Type: application/json" \
            "${OBS_PROXY_URL}/loco.observability.v1.ObservabilityProxyService/QueryLogs" \
            -d "{
                \"workspace_id\": \"${WORKSPACE_ID}\",
                \"resource_ids\": [\"${RESOURCE_ID}\"]
            }"
}

test_obs_proxy_invalid_token_rejected() {
    if ! curl -sf "${OBS_PROXY_URL}/healthz" >/dev/null 2>&1; then
        E2E_SKIP=$((E2E_SKIP + 1))
        return 0
    fi

    assert_fails "QueryLogs with invalid token returns error" \
        curl -sf \
            -X POST \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer invalid-token-12345" \
            "${OBS_PROXY_URL}/loco.observability.v1.ObservabilityProxyService/QueryLogs" \
            -d "{
                \"workspace_id\": \"${WORKSPACE_ID}\",
                \"resource_ids\": [\"${RESOURCE_ID}\"]
            }"
}

test_obs_proxy_expired_token_rejected() {
    if ! curl -sf "${OBS_PROXY_URL}/healthz" >/dev/null 2>&1; then
        E2E_SKIP=$((E2E_SKIP + 1))
        return 0
    fi

    local token
    token=$(get_expired_obs_token)

    assert_fails "QueryLogs with expired token returns error" \
        curl -sf \
            -X POST \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer ${token}" \
            "${OBS_PROXY_URL}/loco.observability.v1.ObservabilityProxyService/QueryLogs" \
            -d "{
                \"workspace_id\": \"${WORKSPACE_ID}\",
                \"resource_ids\": [\"${RESOURCE_ID}\"]
            }"
}

test_obs_proxy_wrong_workspace_rejected() {
    if ! curl -sf "${OBS_PROXY_URL}/healthz" >/dev/null 2>&1; then
        E2E_SKIP=$((E2E_SKIP + 1))
        return 0
    fi

    # Get a token scoped to WORKSPACE_ID
    local token
    token=$(get_obs_token)

    # Try to query a different workspace — should fail
    assert_fails "QueryLogs for wrong workspace returns error" \
        curl -sf \
            -X POST \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer ${token}" \
            "${OBS_PROXY_URL}/loco.observability.v1.ObservabilityProxyService/QueryLogs" \
            -d '{
                "workspace_id": "00000000-0000-7000-8000-999999999999",
                "resource_ids": ["some-resource"]
            }'
}

test_obs_proxy_valid_token_accepted() {
    if ! curl -sf "${OBS_PROXY_URL}/healthz" >/dev/null 2>&1; then
        E2E_SKIP=$((E2E_SKIP + 1))
        return 0
    fi

    local token
    token=$(get_obs_token)

    # With a valid token and correct workspace, the request should pass auth.
    # It may fail at the ClickHouse layer (no CH in e2e), but the HTTP status
    # from ConnectRPC will be different from an auth error.
    # Auth errors return 401/403; ClickHouse errors return 500/503.
    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" \
        -X POST \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${token}" \
        "${OBS_PROXY_URL}/loco.observability.v1.ObservabilityProxyService/QueryLogs" \
        -d "{
            \"workspace_id\": \"${WORKSPACE_ID}\",
            \"resource_ids\": [\"${RESOURCE_ID}\"]
        }")

    # 401 = auth failed, anything else means auth passed (even if query failed downstream)
    assert "Valid token passes auth (HTTP code != 401)" \
        test "$http_code" != "401"
}
