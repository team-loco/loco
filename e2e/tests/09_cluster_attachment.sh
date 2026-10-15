#!/usr/bin/env bash

source "$SCRIPT_DIR/tests/01_agent_lifecycle.sh"

cluster_admin_home="$E2E_BUILD_WORK_DIR/cluster-admin-home"
cluster_admin_token="loco_s_e2e-admin-session-do-not-use-in-production"

cluster_admin() {
    HOME="$cluster_admin_home" LOCO_CREDENTIAL_STORE=file LOCO_HOST="$E2E_API_URL" \
        "$BIN_DIR/loco" admin cluster "$@"
}

cluster_admin_code() {
    curl -s -o /dev/null -w '%{http_code}' \
        -H 'Content-Type: application/json' -H "Authorization: Bearer $3" \
        --data "$2" "${E2E_API_URL}/loco.cluster.v1.ClusterAdminService/$1"
}

test_z05_cluster_admin_authorization() {
    cli_seed_session
    e2e_psql "
        INSERT INTO users (id, email) VALUES ('00000000-0000-7000-8000-000000000008', 'loco-admin@test.local');
        INSERT INTO user_scopes (user_id, scope, entity_type, entity_id)
        VALUES ('00000000-0000-7000-8000-000000000008', 'admin', 'system', '00000000-0000-0000-0000-000000000000');
        INSERT INTO session_tokens (id, access_token_hash, refresh_token_hash, user_id, access_expires_at, refresh_expires_at)
        VALUES ('00000000-0000-7000-8000-000000000009',
                encode(sha256(convert_to('${cluster_admin_token}', 'UTF8')), 'hex'),
                encode(sha256(convert_to('refresh-${cluster_admin_token}', 'UTF8')), 'hex'),
                '00000000-0000-7000-8000-000000000008', NOW() + INTERVAL '1 day', NOW() + INTERVAL '1 day');
    " >/dev/null
    cli_home="$cluster_admin_home" E2E_USER_TOKEN="$cluster_admin_token" cli_write_credentials
    assert "Workspace user cannot list clusters" \
        test "$(cluster_admin_code ListClusters '{}' "$E2E_USER_TOKEN")" = 403
    assert "Agent secret cannot manage clusters" \
        test "$(cluster_admin_code PauseCluster "{\"clusterId\":\"${E2E_CLUSTER_ID}\"}" "$AGENT_TOKEN")" = 401
    assert_contains "System admin can list the attached cluster" "$E2E_CLUSTER_ID" cluster_admin list
}

start_api_replica() {
    local replica_port="$API_REPLICA_PORT"
    mkdir -p "$PID_DIR/api-replica" "$LOG_DIR/api-replica"
    API_PORT="$replica_port" \
    E2E_API_URL="http://127.0.0.1:${replica_port}" \
    PID_DIR="$PID_DIR/api-replica" \
    LOG_DIR="$LOG_DIR/api-replica" \
        start_api "$1"
}

registration_code() {
    local endpoint="$1" token="$2"
    curl -s -o /dev/null -w '%{http_code}' \
        -H 'Content-Type: application/json' \
        -H "Authorization: Bearer ${token}" \
        --data "{\"clusterKey\":\"${E2E_CLUSTER_KEY}\",\"displayName\":\"e2e-test-cluster\",\"clusterRegion\":\"us-east-1\",\"tier\":\"production\",\"agentVersion\":\"${E2E_VERSION}\"}" \
        "${endpoint}/loco.agent.v1.AgentService/Register"
}

test_z10_rotation_across_api_processes() {
    local previous_token="$AGENT_TOKEN"
    local replacement_token="e2e-replacement-token-do-not-use-in-production"
    local replica_url="http://127.0.0.1:${API_REPLICA_PORT}"
    start_api_replica "$previous_token"
    assert "New secret is rejected before replica rollout" \
        test "$(registration_code "$replica_url" "$replacement_token")" = 401

    kill_pid_file "$PID_DIR/api.pid"
    start_api "$previous_token,$replacement_token"
    kill_pid_file "$PID_DIR/agent.pid"
    E2E_API_URL="$replica_url" start_agent
    local previous_generation
    previous_generation=$(e2e_psql "SELECT sync_generation FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")
    kill_pid_file "$PID_DIR/api-replica/api.pid"
    start_api_replica "$previous_token,$replacement_token"
    E2E_PREVIOUS_SYNC_GENERATION="$previous_generation" wait_for "Agent reconnect after replica rollout" "$READY_TIMEOUT" agent_registered

    AGENT_TOKEN="$replacement_token"
    kill_pid_file "$PID_DIR/agent.pid"
    start_agent
    previous_generation=$(e2e_psql "SELECT sync_generation FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")
    kill_pid_file "$PID_DIR/api.pid"
    start_api "$replacement_token"
    E2E_PREVIOUS_SYNC_GENERATION="$previous_generation" wait_for "Agent reconnect after retiring the old secret" "$READY_TIMEOUT" agent_registered
    kill_pid_file "$PID_DIR/api-replica/api.pid"
    start_api_replica "$replacement_token"

    assert "Primary rejects retired secret" test "$(registration_code "$E2E_API_URL" "$previous_token")" = 401
    assert "Replica rejects retired secret" test "$(registration_code "$replica_url" "$previous_token")" = 401
    assert "Replica accepts replacement secret" test "$(registration_code "$replica_url" "$replacement_token")" = 200
    assert "Rotation preserves a single cluster" test "$(e2e_psql 'SELECT count(*) FROM clusters')" = 1
}

test_z20_pause_remove_and_restore() {
    cluster_admin pause "$E2E_CLUSTER_ID"
    assert "Pause removes cluster from assignment" \
        test "$(e2e_psql "SELECT is_active FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")" = f
    assert_fails "Normal removal refuses existing workloads" \
        cluster_admin remove "$E2E_CLUSTER_ID"
    assert_contains "Force removal reports workloads" 'placement' \
        cluster_admin remove "$E2E_CLUSTER_ID" --force
    assert "Removed cluster rejects registration" \
        test "$(registration_code "$E2E_API_URL" "$AGENT_TOKEN")" = 400
    assert_fails "Removed cluster cannot resume without restore" \
        cluster_admin resume "$E2E_CLUSTER_ID"
    cluster_admin restore "$E2E_CLUSTER_ID"
    assert "Restore keeps cluster paused" \
        test "$(e2e_psql "SELECT is_active FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")" = f
    kill_pid_file "$PID_DIR/agent.pid"
    start_agent
    cluster_admin resume "$E2E_CLUSTER_ID"
    wait_for "Restored cluster heartbeat" "$READY_TIMEOUT" agent_heartbeat_recorded
    assert "Restore reuses cluster identity" test "$(e2e_psql 'SELECT count(*) FROM clusters')" = 1
}
