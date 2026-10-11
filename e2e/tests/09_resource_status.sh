#!/usr/bin/env bash

source "$E2E_ROOT_DIR/e2e/cli.sh"

status_ctx="kind-${E2E_KIND_CLUSTER}"
status_dir="$E2E_BUILD_WORK_DIR/resource-status"
cli_home="$status_dir/home"
status_app="e2e-status"
status_app_dir="$status_dir/$status_app"
status_namespace="ws-${cli_workspace_id}"
status_node="${E2E_KIND_CLUSTER}-control-plane"

sk() {
    kubectl --context "$status_ctx" "$@"
}

status_json() {
    loco_cli resource status "$status_app" --output json
}

status_field() {
    status_json | yq -p json -r "$1"
}

environment_status() {
    status_field '.resource.environmentStatuses[0].status'
}

environment_status_is() {
    test "$(environment_status)" = "$1"
}

region_field() {
    status_field ".resource.environmentStatuses[0].regions[0].$1 // \"\""
}

status_app_selector() {
    local id
    id=$(e2e_psql "SELECT id FROM resources WHERE name = '${status_app}'")
    echo "app=resource-${id}"
}

test_s01_deploy_reports_healthy() {
    cli_seed_session
    rm -rf "$status_dir"
    mkdir -p "$status_app_dir"
    cli_write_credentials
    cli_write_image_config "$status_app_dir" "$status_app" "$E2E_PUBLIC_IMAGE" 8080 "[]"

    local rc=0
    (cd "$status_app_dir" && loco_cli deploy --yes) >"$status_dir/deploy.out" 2>"$status_dir/deploy.err" || rc=$?
    if ! assert "loco deploy ${status_app} (exit ${rc})" test "$rc" -eq 0; then
        sed 's/^/    /' "$status_dir/deploy.out" "$status_dir/deploy.err"
        return 1
    fi

    wait_for "the API to report ${status_app} healthy" 180 environment_status_is RESOURCE_STATUS_HEALTHY
    assert "The API reports ${status_app} healthy" environment_status_is RESOURCE_STATUS_HEALTHY
    assert "The region reports one of one replicas ready" \
        test "$(region_field readyReplicas)/$(region_field desiredReplicas)" = 1/1
    assert_contains "The table view shows the environment as healthy" "Healthy" \
        loco_cli resource status "$status_app"
}

test_s02_crashed_pods_report_unavailable() {
    sk cordon "$status_node" >/dev/null
    sk -n "$status_namespace" delete pods -l "$(status_app_selector)" --wait=false >/dev/null

    wait_for "the API to report ${status_app} unavailable" 120 environment_status_is RESOURCE_STATUS_UNAVAILABLE
    local status deployment ready
    status=$(environment_status)
    deployment=$(region_field deploymentStatus)
    ready=$(region_field readyReplicas)
    sk uncordon "$status_node" >/dev/null

    assert "The API reports ${status_app} unavailable while its pod cannot run (got ${status})" \
        test "$status" = RESOURCE_STATUS_UNAVAILABLE
    assert "The deployment itself still reads running (got ${deployment})" \
        test "$deployment" = DEPLOYMENT_PHASE_RUNNING
    assert "The region reports no ready replicas (got '${ready}')" test -z "$ready"

    wait_for "the API to report ${status_app} healthy again" 180 environment_status_is RESOURCE_STATUS_HEALTHY
    assert "The API reports ${status_app} healthy once the pod runs again" \
        environment_status_is RESOURCE_STATUS_HEALTHY
}

test_s03_silent_cluster_reports_unknown() {
    kill_pid_file "$PID_DIR/agent.pid"
    mv "$LOG_DIR/agent.log" "$LOG_DIR/agent.before-restart.log"

    wait_for "the API to stop trusting the silent cluster" 180 environment_status_is RESOURCE_STATUS_UNKNOWN
    local status
    status=$(environment_status)
    start_agent

    assert "The API reports ${status_app} unknown while its cluster is silent (got ${status})" \
        test "$status" = RESOURCE_STATUS_UNKNOWN
    wait_for "the API to report ${status_app} healthy after the agent returns" 120 \
        environment_status_is RESOURCE_STATUS_HEALTHY
    assert "The API reports ${status_app} healthy once the agent reports again" \
        environment_status_is RESOURCE_STATUS_HEALTHY
}
