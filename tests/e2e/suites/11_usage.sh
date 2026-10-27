#!/usr/bin/env bash

source "$E2E_ROOT_DIR/tests/e2e/cli.sh"

usage_ctx="kind-${E2E_KIND_CLUSTER}"
usage_dir="$E2E_BUILD_WORK_DIR/usage"
cli_home="$usage_dir/home"
usage_app="e2e-usage"
usage_app_dir="$usage_dir/$usage_app"
usage_namespace="${E2E_WORKSPACE_PREFIX}${cli_workspace_id}"
usage_millicores=100
usage_memory_bytes=134217728
usage_page_size=100
usage_host="${usage_app}.${E2E_PLATFORM_DOMAIN}"
usage_requests=20
usage_curl_timeout_seconds=5

uk() {
    kubectl --context "$usage_ctx" "$@"
}

usage_resource_id() {
    e2e_psql "SELECT id FROM resources WHERE name = '${usage_app}'"
}

usage_call() {
    local token=$1 method=$2 body=$3
    curl -sS --fail-with-body -X POST "${E2E_API_URL}/loco.usage.v1.UsageService/${method}" \
        -H "Authorization: Bearer ${token}" \
        -H 'Content-Type: application/json' \
        -d "$body"
}

# Print every row of the feed as one JSON object per line, following next_cursor until it stops moving.
usage_feed() {
    local method=$1 field=$2 cursor="" page next
    while :; do
        page=$(usage_call "$E2E_CLUSTER_ADMIN_TOKEN" "$method" \
            "{\"cursor\":\"${cursor}\",\"pageSize\":${usage_page_size}}") || return 1
        yq -p json -o json -I0 ".${field} // [] | .[]" <<<"$page"
        next=$(yq -p json -r '.nextCursor // ""' <<<"$page")
        [ "$next" = "$cursor" ] && return 0
        cursor=$next
    done
}

usage_pod_uid() {
    uk -n "$usage_namespace" get pods -l "app=resource-$(usage_resource_id)" \
        --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.uid}'
}

usage_run() {
    local uid=$1
    usage_feed ListWorkloadRuns runs | yq -p json -o json -I0 "select(.id == \"${uid}\")" | tail -n 1
}

usage_run_has_allocation() {
    local run
    run=$(usage_run "$1")
    test -n "$run" &&
        test "$(yq -p json -r '.cpuMillicores' <<<"$run")" = "$usage_millicores" &&
        test "$(yq -p json -r '.memoryBytes' <<<"$run")" = "$usage_memory_bytes" &&
        test "$(yq -p json -r '.resourceId' <<<"$run")" = "$(usage_resource_id)" &&
        test "$(yq -p json -r '.clusterId' <<<"$run")" = "$E2E_CLUSTER_ID" &&
        test "$(yq -p json -r '.endedAt // ""' <<<"$run")" = ""
}

usage_run_ended() {
    local run
    run=$(usage_run "$1")
    test -n "$(yq -p json -r '.endedAt // ""' <<<"$run")"
}

usage_metered_through() {
    usage_call "$E2E_CLUSTER_ADMIN_TOKEN" ListWorkloadRuns '{}' | yq -p json -r '.meteredThrough // ""'
}

usage_metered_through_after() {
    local through
    through=$(usage_metered_through)
    test -n "$through" && [[ $through > $1 ]]
}

test_u01_a_running_pod_is_metered_with_its_allocation() {
    cli_seed_session
    rm -rf "$usage_dir"
    mkdir -p "$usage_app_dir"
    cli_write_credentials
    cli_write_image_config "$usage_app_dir" "$usage_app" "$E2E_PUBLIC_IMAGE" 8080 "[${usage_host}]"

    local rc=0
    (cd "$usage_app_dir" && loco_cli deploy --yes) >"$usage_dir/deploy.out" 2>"$usage_dir/deploy.err" || rc=$?
    if ! assert "loco deploy ${usage_app} (exit ${rc})" test "$rc" -eq 0; then
        sed 's/^/    /' "$usage_dir/deploy.out" "$usage_dir/deploy.err"
        return 1
    fi
    wait_for "${usage_app} to be Ready" 180 cli_application_is_ready "$usage_app"

    USAGE_FIRST_POD=$(usage_pod_uid)
    assert "The app has a running pod" test -n "$USAGE_FIRST_POD" || return 1
    wait_for "a workload run for pod ${USAGE_FIRST_POD}" 150 usage_run_has_allocation "$USAGE_FIRST_POD"
    assert "The pod's run carries its ${usage_millicores}m CPU and ${usage_memory_bytes} B memory requests" \
        usage_run_has_allocation "$USAGE_FIRST_POD"
    local started
    started=$(date -u +%Y-%m-%dT%H:%M:%S)
    wait_for "metered_through to pass ${started}" 150 usage_metered_through_after "$started"
    assert "metered_through advances with the cluster's reports" usage_metered_through_after "$started"
}

test_u02_a_replaced_pod_ends_its_run() {
    test -n "${USAGE_FIRST_POD:-}" || return 1
    uk -n "$usage_namespace" delete pod -l "app=resource-$(usage_resource_id)" --wait=true >/dev/null
    wait_for "the deleted pod's run to end" 60 usage_run_ended "$USAGE_FIRST_POD"
    assert "Deleting the pod ends its run" usage_run_ended "$USAGE_FIRST_POD"

    wait_for "${usage_app} to be Ready again" 120 cli_application_is_ready "$usage_app"
    local replacement
    replacement=$(usage_pod_uid)
    assert "The Deployment replaced the pod" test -n "$replacement" -a "$replacement" != "$USAGE_FIRST_POD"
    wait_for "a workload run for the replacement pod" 150 usage_run_has_allocation "$replacement"
    assert "The replacement pod gets its own run" usage_run_has_allocation "$replacement"
}

usage_gateway_service() {
    uk -n "$E2E_LOCO_NAMESPACE" get services -o name \
        -l "gateway.envoyproxy.io/owning-gateway-name=eg,gateway.envoyproxy.io/owning-gateway-namespace=${E2E_LOCO_NAMESPACE}"
}

usage_forward_gateway() {
    if curl -s -o /dev/null --max-time "$usage_curl_timeout_seconds" "$E2E_GATEWAY_URL"; then
        return 0
    fi
    local service
    service=$(usage_gateway_service)
    uk -n "$E2E_LOCO_NAMESPACE" port-forward --address 127.0.0.1 "$service" "${E2E_GATEWAY_URL##*:}:80" \
        >"$usage_dir/gateway-forward.log" 2>&1 &
    USAGE_GATEWAY_FORWARD=$!
}

usage_gateway_status() {
    curl -s -o /dev/null -w '%{http_code}' --max-time "$usage_curl_timeout_seconds" \
        -H "Host: ${usage_host}" "${E2E_GATEWAY_URL}/"
}

usage_route_serves() {
    test "$(usage_gateway_status)" = 200
}

usage_send_requests() {
    local i
    for i in $(seq 1 "$usage_requests"); do
        usage_gateway_status >/dev/null
    done
}

usage_egress_bytes() {
    local resource
    resource=$(usage_resource_id)
    usage_feed ListEgressUsage usage |
        yq -p json -o json -I0 "select(.resourceId == \"${resource}\" and .clusterId == \"${E2E_CLUSTER_ID}\")" |
        yq -p json -r '.bytes // 0' |
        awk '{ total += $1 } END { print total + 0 }'
}

usage_egress_above() {
    usage_send_requests
    test "$(usage_egress_bytes)" -gt "$1"
}

usage_egress_metered() {
    test -n "$(usage_call "$E2E_CLUSTER_ADMIN_TOKEN" ListEgressUsage '{}' | yq -p json -r '.meteredThrough // ""')"
}

test_u03_gateway_responses_are_metered_as_egress() {
    usage_forward_gateway
    wait_for "the gateway to route ${usage_host}" 120 usage_route_serves
    assert "The gateway serves ${usage_host}" usage_route_serves || return 1
    wait_for "egress for ${usage_app}" 180 usage_egress_above 0
    local first
    first=$(usage_egress_bytes)
    assert "Responses through the gateway are metered as egress (${first} B)" test "$first" -gt 0
    wait_for "egress for ${usage_app} to grow past ${first} B" 180 usage_egress_above "$first"
    assert "More responses add to the service's egress" usage_egress_above "$first"
    assert "The egress feed reports metered_through" usage_egress_metered
    if [ -n "${USAGE_GATEWAY_FORWARD:-}" ]; then
        kill "$USAGE_GATEWAY_FORWARD" 2>/dev/null || true
    fi
}

usage_denied_to_workspace_member() {
    local method=$1 out
    out=$(usage_call "$E2E_USER_TOKEN" "$method" '{}' 2>&1) && return 1
    grep -q permission_denied <<<"$out"
}

test_u04_the_feed_requires_a_system_admin() {
    assert "A workspace member cannot read workload runs" usage_denied_to_workspace_member ListWorkloadRuns
    assert "A workspace member cannot read egress" usage_denied_to_workspace_member ListEgressUsage
}
