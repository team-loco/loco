#!/usr/bin/env bash

source "$E2E_ROOT_DIR/e2e/cli.sh"

proxy_url="http://127.0.0.1:${E2E_OBS_PROXY_PORT}"
proxy_query_logs="${E2E_API_URL}/loco.observability.v1.ObservabilityProxyService/QueryLogs"
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


test_p05_browser_preflight_and_error_responses() {
    local origin=https://dashboard.e2e.test headers status
    headers=$(curl -s -D - -o /dev/null -X OPTIONS "$proxy_query_logs" \
        -H "Origin: $origin" -H 'Access-Control-Request-Method: POST' \
        -H 'Access-Control-Request-Headers: authorization,connect-protocol-version,connect-timeout-ms,content-type')
    assert "Browser preflight allows the configured dashboard" bash -c '[[ "$1" == *"Access-Control-Allow-Origin: $2"* ]]' _ "$headers" "$origin"
    status=$(proxy_status "$cli_workspace_id" -H "Origin: $origin")
    assert "CORS preserves authentication requirements" test "$status" = 401
    headers=$(curl -s -D - -o /dev/null -X POST "$proxy_query_logs" \
        -H "Origin: $origin" -H 'Content-Type: application/json' -d "$(proxy_logs_body "$cli_workspace_id")")
    assert "Browser can read authentication errors" bash -c '[[ "$1" == *"Access-Control-Allow-Origin: $2"* ]]' _ "$headers" "$origin"
    headers=$(curl -s -D - -o /dev/null -X OPTIONS "$proxy_query_logs" \
        -H 'Origin: https://other.e2e.test' -H 'Access-Control-Request-Method: POST')
    assert "Other origins are excluded" bash -c '[[ "$1" != *"Access-Control-Allow-Origin:"* ]]' _ "$headers"
}

otlp_logs_url="http://127.0.0.1:${E2E_OTLP_HTTP_PORT}/v1/logs"
otlp_dir="$E2E_BUILD_WORK_DIR/observability-proxy"
cli_home="$otlp_dir/home"
otlp_app="e2e-otlp-ingest"

otlp_logs_body() {
    local workspace_id=$1 body=$2
    printf '{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"e2e-ingest"}},{"key":"loco.workspace.id","value":{"stringValue":"%s"}}]},"scopeLogs":[{"logRecords":[{"timeUnixNano":"%s000000000","severityText":"INFO","body":{"stringValue":"%s"}}]}]}]}' \
        "$workspace_id" "$(date -u +%s)" "$body"
}

ingest_token() {
    local namespace=${1:-$E2E_OBS_NAMESPACE}
    kubectl --context "kind-${E2E_KIND_CLUSTER}" -n "$namespace" get secret "$E2E_INGEST_TOKEN_SECRET" \
        -o jsonpath="{.data.${E2E_INGEST_TOKEN_SECRET_KEY}}" | base64 -d
}

ingest_token_written() {
    test -n "$(ingest_token)"
}

otlp_post() {
    local workspace_id=$1 marker=$2
    shift 2
    curl -s -w '\n%{http_code}' -X POST "$otlp_logs_url" -H 'Content-Type: application/json' "$@" \
        -d "$(otlp_logs_body "$workspace_id" "$marker")"
}

otlp_status() {
    otlp_post "$@" | tail -n 1
}

forged_token() {
    local token=$1 signature
    signature=$(openssl rand 64 | base64 | tr '+/' '-_' | tr -d '=\n')
    printf '%s.%s' "${token%.*}" "$signature"
}

place_cli_workspace() {
    cli_seed_session
    rm -rf "$otlp_dir"
    mkdir -p "$otlp_dir/$otlp_app"
    cli_write_credentials
    cli_write_image_config "$otlp_dir/$otlp_app" "$otlp_app" "$E2E_PUBLIC_IMAGE" 8080 "[]"
    (cd "$otlp_dir/$otlp_app" && loco_cli deploy --yes) >"$otlp_dir/deploy.out" 2>"$otlp_dir/deploy.err"
}

test_p06_otlp_ingest_requires_an_enrolled_cluster_token() {
    local marker token status
    marker="e2e-otlp-ingest-$(openssl rand -hex 4)"
    wait_for "the agent to write the ingest token" 60 ingest_token_written
    token=$(ingest_token)
    assert "The agent wrote an ingest token to the collector Secret" test -n "$token"
    assert "The node collector namespace holds the same token" test "$(ingest_token "$E2E_OBS_NODE_NAMESPACE")" = "$token"
    status=$(otlp_status "$cli_workspace_id" "$marker")
    assert "OTLP without a token is unauthenticated (HTTP ${status})" test "$status" = 401
    status=$(otlp_status "$cli_workspace_id" "$marker" -H "Authorization: Bearer $(forged_token "$token")")
    assert "OTLP with a forged token is unauthenticated (HTTP ${status})" test "$status" = 401
    status=$(otlp_status "$cli_workspace_id" "$marker" -H "Authorization: Bearer ${E2E_USER_TOKEN}")
    assert "OTLP with a user session token is unauthenticated (HTTP ${status})" test "$status" = 401
}

test_p07_otlp_ingest_accepts_placed_workspaces_only() {
    local token marker rejected_marker response status body query
    if ! assert "loco deploy places the workspace on the cluster" place_cli_workspace; then
        sed 's/^/    /' "$otlp_dir/deploy.out" "$otlp_dir/deploy.err"
        return 1
    fi
    token=$(ingest_token)
    marker="e2e-otlp-ingest-$(openssl rand -hex 4)"
    rejected_marker="e2e-otlp-rejected-$(openssl rand -hex 4)"
    response=$(otlp_post "$cli_workspace_id" "$marker" -H "Authorization: Bearer ${token}")
    status=$(tail -n 1 <<<"$response")
    body=$(sed '$d' <<<"$response")
    assert "OTLP/HTTP JSON logs of a placed workspace are accepted (HTTP ${status})" test "$status" = 200
    assert "Nothing is rejected for a placed workspace" bash -c '[[ "$1" != *rejectedLogRecords* ]]' _ "$body"
    response=$(otlp_post "$proxy_other_workspace_id" "$rejected_marker" -H "Authorization: Bearer ${token}")
    status=$(tail -n 1 <<<"$response")
    body=$(sed '$d' <<<"$response")
    assert "Logs of a workspace not placed on the cluster still return 200 (HTTP ${status})" test "$status" = 200
    assert "They are reported as rejected through partial success" \
        bash -c '[[ "$1" == *"\"rejectedLogRecords\":\"1\""* ]]' _ "$body"
    status=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$otlp_logs_url" \
        -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" -d '{"resourceLogs":')
    assert "An undecodable OTLP request is rejected as permanent (HTTP ${status})" test "$status" = 400
    query=$(mint_api_token "$(proxy_token_name ingest)" ENTITY_TYPE_WORKSPACE "$cli_workspace_id" SCOPE_READ)
    response=$(curl -s -X POST "$proxy_query_logs" -H 'Content-Type: application/json' \
        -H "Authorization: Bearer ${query}" -d "$(proxy_logs_body "$cli_workspace_id")")
    assert "QueryLogs returns the record written through OTLP ingest" bash -c '[[ "$1" == *"$2"* ]]' _ "$response" "$marker"
}
