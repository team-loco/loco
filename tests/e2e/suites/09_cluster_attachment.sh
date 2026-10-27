#!/usr/bin/env bash

source "$SCRIPT_DIR/suites/01_agent_lifecycle.sh"

cluster_admin_code() {
    curl -s -o /dev/null -w '%{http_code}' \
        -H 'Content-Type: application/json' -H "Authorization: Bearer $3" \
        --data "$2" "${E2E_API_URL}/loco.cluster.v1.ClusterAdminService/$1"
}

test_z05_cluster_admin_authorization() {
    cli_seed_session
    assert_fails_with "Wrong bootstrap token is rejected" "Invalid bootstrap token" \
        locoadm_as_admin bootstrap --token "wrong-${E2E_BOOTSTRAP_TOKEN}"
    assert_fails_with "Second bootstrap is refused" "already has a system admin" \
        locoadm_as_admin bootstrap --token "$E2E_BOOTSTRAP_TOKEN"
    assert "Workspace user cannot list clusters" \
        test "$(cluster_admin_code ListClusters '{}' "$E2E_USER_TOKEN")" = 403
    assert "Join token cannot manage clusters" \
        test "$(cluster_admin_code PauseCluster "{\"clusterId\":\"${E2E_CLUSTER_ID}\"}" "$E2E_JOIN_TOKEN")" = 401
    assert_contains "System admin can list the enrolled cluster" "$E2E_CLUSTER_ID" cluster_admin list
}

start_api_replica() {
    local replica_port="$API_REPLICA_PORT"
    mkdir -p "$PID_DIR/api-replica" "$LOG_DIR/api-replica"
    API_PORT="$replica_port" \
    E2E_API_URL="http://127.0.0.1:${replica_port}" \
    PID_DIR="$PID_DIR/api-replica" \
    LOG_DIR="$LOG_DIR/api-replica" \
        start_api
}

enrollment_code() {
    local token="$1" public_key
    public_key=$(head -c 32 /dev/urandom | base64)
    curl -s -o /dev/null -w '%{http_code}' \
        -H 'Content-Type: application/json' \
        --data "{\"clusterKey\":\"${E2E_CLUSTER_KEY}\",\"agentVersion\":\"${E2E_VERSION}\",\"enrollment\":{\"joinToken\":\"${token}\",\"publicKey\":\"${public_key}\"}}" \
        "${E2E_API_URL}/loco.agent.v1.AgentService/Register"
}

agent_key_fingerprint() {
    cluster_admin list | yq -p json ".clusters[] | select(.id == \"${E2E_CLUSTER_ID}\") | .agentKeyFingerprint"
}

test_z08_join_token_is_single_use() {
    assert "A second enrollment with the same join token is rejected" \
        test "$(enrollment_code "$E2E_JOIN_TOKEN")" = 401
    assert "Rejected enrollment keeps a single cluster record" test "$(e2e_psql 'SELECT count(*) FROM clusters')" = 1
}

test_z09_restarted_api_keeps_bootstrap_closed() {
    kill_pid_file "$PID_DIR/api.pid"
    start_api
    assert_fails_with "A restarted API keeps bootstrap closed" "already has a system admin" \
        locoadm_as_admin bootstrap --token "$E2E_BOOTSTRAP_TOKEN"
}

test_z10_reconnects_across_api_processes() {
    local replica_url="http://127.0.0.1:${API_REPLICA_PORT}"
    start_api_replica
    kill_pid_file "$PID_DIR/agent.pid"
    E2E_API_URL="$replica_url" E2E_JOIN_TOKEN="" start_agent
    local previous_generation
    previous_generation=$(e2e_psql "SELECT sync_generation FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")
    kill_pid_file "$PID_DIR/api-replica/api.pid"
    start_api_replica
    E2E_PREVIOUS_SYNC_GENERATION="$previous_generation" \
        wait_for "Agent reconnect after an API restart" "$READY_TIMEOUT" agent_registered
    kill_pid_file "$PID_DIR/agent.pid"
    kill_pid_file "$PID_DIR/api-replica/api.pid"
    E2E_JOIN_TOKEN="" start_agent
    assert "Reconnects preserve a single cluster" test "$(e2e_psql 'SELECT count(*) FROM clusters')" = 1
}

test_z15_reenrollment_rotates_the_agent_key() {
    local previous_fingerprint
    previous_fingerprint=$(agent_key_fingerprint)
    E2E_JOIN_TOKEN=$(cluster_admin join-token "$E2E_CLUSTER_ID" | yq -p json '.joinToken.token')
    kill_pid_file "$PID_DIR/agent.pid"
    delete_agent_key
    start_agent
    assert "Re-enrollment keeps the cluster ID" \
        test "$(e2e_psql "SELECT id FROM clusters WHERE cluster_key = '${E2E_CLUSTER_KEY}'")" = "$E2E_CLUSTER_ID"
    assert "Re-enrollment rotates the agent key" test "$(agent_key_fingerprint)" != "$previous_fingerprint"
}

process_stopped() {
    ! kill -0 "$1" 2>/dev/null
}

agent_errors_after() {
    tail -n +"$(($1 + 1))" "$LOG_DIR/agent.log" | grep '"level":"ERROR"' || true
}

only_removal_error() {
    test "$(grep -c . <<<"$1")" = 1 &&
        grep -q '"msg":"agent error".*permission_denied: cluster is removed' <<<"$1"
}

test_z20_pause_remove_and_restore() {
    local unused_token agent_pid log_lines status=0 removal_errors
    unused_token=$(cluster_admin join-token "$E2E_CLUSTER_ID" | yq -p json '.joinToken.token')
    agent_pid=$(cat "$PID_DIR/agent.pid")
    log_lines=$(wc -l <"$LOG_DIR/agent.log")
    cluster_admin pause "$E2E_CLUSTER_ID"
    assert "Pause removes cluster from assignment" \
        test "$(e2e_psql "SELECT is_active FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")" = f
    assert_fails "Normal removal refuses existing workloads" \
        cluster_admin remove "$E2E_CLUSTER_ID"
    assert_contains "Force removal reports workloads" 'placement' \
        cluster_admin remove "$E2E_CLUSTER_ID" --force
    if wait_for "Agent exit on the removed cluster" "$READY_TIMEOUT" process_stopped "$agent_pid"; then
        wait "$agent_pid" || status=$?
    fi
    assert "Agent exits nonzero on the removed cluster" test "$status" = 1
    removal_errors=$(agent_errors_after "$log_lines")
    if assert "Agent logs the removal as its one ERROR" only_removal_error "$removal_errors"; then
        printf '%s\n' "$removal_errors" >>"$AGENT_EXPECTED_ERRORS"
    fi
    assert "Removed cluster rejects enrollment" test "$(enrollment_code "$unused_token")" = 401
    assert_fails "Removed cluster gets no join token" cluster_admin join-token "$E2E_CLUSTER_ID"
    assert_fails "Removed cluster cannot resume without restore" \
        cluster_admin resume "$E2E_CLUSTER_ID"
    cluster_admin restore "$E2E_CLUSTER_ID"
    assert "Restore keeps cluster paused" \
        test "$(e2e_psql "SELECT is_active FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")" = f
    kill_pid_file "$PID_DIR/agent.pid"
    E2E_JOIN_TOKEN="" start_agent
    cluster_admin resume "$E2E_CLUSTER_ID"
    wait_for "Restored cluster heartbeat" "$READY_TIMEOUT" agent_heartbeat_recorded
    assert "Restore reuses cluster identity" test "$(e2e_psql 'SELECT count(*) FROM clusters')" = 1
}
