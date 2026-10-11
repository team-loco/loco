#!/usr/bin/env bash

source "$E2E_ROOT_DIR/e2e/cli.sh"

proxy_url="http://127.0.0.1:${E2E_OBS_PROXY_PORT}"
proxy_query_logs="${proxy_url}/loco.observability.v1.ObservabilityProxyService/QueryLogs"
proxy_other_workspace_id='00000000-0000-7000-8000-999999999999'
proxy_query_window_seconds=3600

proxy_logs_body() {
    local workspace_id=$1 now start end
    now=$(date -u +%s)
    start=$(utc_timestamp "$((now - proxy_query_window_seconds))")
    end=$(utc_timestamp "$now")
    printf '{"workspaceId":"%s","startTime":"%s","endTime":"%s"}' "$workspace_id" "$start" "$end"
}

proxy_status() {
    local workspace_id=$1
    shift
    local body
    body=$(proxy_logs_body "$workspace_id")
    curl -s -o /dev/null -w '%{http_code}' -X POST "$proxy_query_logs" \
        -H 'Content-Type: application/json' "$@" -d "$body"
}

proxy_token_name() {
    echo "e2e-proxy-$1-$(openssl rand -hex 4)"
}

test_p01_health() {
    assert "The proxy answers /healthz" curl -sf "${proxy_url}/healthz"
    assert "The proxy answers /readyz once its schema is applied" curl -sf "${proxy_url}/readyz"
}

test_p02_requests_without_a_valid_token_are_unauthenticated() {
    cli_seed_session
    local status
    status=$(proxy_status "$cli_workspace_id")
    assert "QueryLogs without a token is unauthenticated (HTTP ${status})" test "$status" = 401
    status=$(proxy_status "$cli_workspace_id" -H 'Authorization: Bearer loco_k_not-a-token')
    assert "QueryLogs with an unknown token is denied (HTTP ${status})" test "$status" = 403
}

test_p03_revoked_token_is_denied() {
    local name token status
    name=$(proxy_token_name revoked)
    token=$(mint_api_token "$name" ENTITY_TYPE_WORKSPACE "$cli_workspace_id" SCOPE_READ)
    assert "TokenService/RevokeToken revokes the token" \
        revoke_api_token "$name" ENTITY_TYPE_WORKSPACE "$cli_workspace_id"
    status=$(proxy_status "$cli_workspace_id" -H "Authorization: Bearer ${token}")
    assert "QueryLogs with a revoked token is denied (HTTP ${status})" test "$status" = 403
}

test_p04_token_is_scoped_to_its_workspace() {
    local token status
    token=$(mint_api_token "$(proxy_token_name scoped)" ENTITY_TYPE_WORKSPACE "$cli_workspace_id" SCOPE_READ)
    status=$(proxy_status "$proxy_other_workspace_id" -H "Authorization: Bearer ${token}")
    assert "QueryLogs for another workspace is denied (HTTP ${status})" test "$status" = 403
    status=$(proxy_status "$cli_workspace_id" -H "Authorization: Bearer ${token}")
    assert "QueryLogs for the token's workspace succeeds (HTTP ${status})" test "$status" = 200
}
