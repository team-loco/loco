#!/usr/bin/env bash

source "$E2E_ROOT_DIR/e2e/cli.sh"

status_ctx="kind-${E2E_KIND_CLUSTER}"
status_dir="$E2E_BUILD_WORK_DIR/resource-status"
cli_home="$status_dir/home"
status_app="e2e-status"
status_app_dir="$status_dir/$status_app"
status_namespace="${E2E_WORKSPACE_PREFIX}${cli_workspace_id}"
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

    mv "$cli_home/.loco/config.yaml" "$status_dir/config.yaml"
    assert_contains "A workspace-only member resolves the workspace by name without a cached scope" "Healthy" \
        loco_cli resource status "$status_app" --org e2e-test-org --workspace e2e-test-workspace
    mv "$status_dir/config.yaml" "$cli_home/.loco/config.yaml"
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

status_rejects_rollout() {
    local name=$1
    shift
    if "$@" >"$status_dir/$name.out" 2>&1; then
        log_error "${name} unexpectedly accepted a mutation during rollout"
        return 1
    fi
    grep -q 'a deployment is rolling out; wait for it to finish before changing this service' \
        "$status_dir/$name.out"
}

status_hold_rollout_and_reject_mutations() (
    local agent_pid resource_id environment_id body
    agent_pid=$(cat "$PID_DIR/agent.pid")
    trap 'kill -CONT "$agent_pid" 2>/dev/null || true' EXIT
    kill -STOP "$agent_pid"
    loco_cli resource scale "$status_app" --cpu 125m >"$status_dir/start-rollout.out" 2>&1 || exit 1
    resource_id=$(e2e_psql "SELECT id FROM resources WHERE name = '${status_app}'")
    environment_id=$(e2e_psql "SELECT environment_id FROM deployments WHERE resource_id = '${resource_id}' AND is_active")
    status_rejects_rollout scale-during-rollout loco_cli resource scale "$status_app" --cpu 150m || exit 1
    status_rejects_rollout secret-during-rollout loco_cli env set --service "$status_app" \
        --env production ROLLOUT_TEST=changed || exit 1
    body="{\"resourceId\":\"${resource_id}\",\"environmentId\":\"${environment_id}\",\"region\":\"us-east-1\",\"spec\":{\"service\":{\"build\":{\"type\":\"image\",\"image\":\"${E2E_PUBLIC_IMAGE}\"},\"env\":{\"ROLLOUT_TEST\":\"changed\"}}}}"
    status_rejects_rollout deployment-during-rollout curl -sS --fail-with-body \
        "${E2E_API_URL}/loco.deployment.v1.DeploymentService/CreateDeployment" \
        -H "Authorization: Bearer ${E2E_USER_TOKEN}" -H 'Content-Type: application/json' -d "$body"
)

status_rollout_is_finished() {
    test "$(region_field deploymentStatus)" = DEPLOYMENT_PHASE_RUNNING &&
        environment_status_is RESOURCE_STATUS_HEALTHY
}

test_s04_rollout_rejects_runtime_mutations() {
    local rc=0
    status_hold_rollout_and_reject_mutations >"$status_dir/rollout-guard.out" 2>&1 || rc=$?
    if ! assert "Scaling, secret edits and replacement deployments reject an active rollout" test "$rc" -eq 0; then
        cat "$status_dir/rollout-guard.out" "$status_dir/"*during-rollout.out
        return 1
    fi
    wait_for "the held rollout to finish after the agent resumes" 180 status_rollout_is_finished
    assert "Scaling becomes available after rollout" \
        loco_cli resource scale "$status_app" --cpu 150m
    wait_for "the follow-up scaling rollout to finish" 180 status_rollout_is_finished
}
