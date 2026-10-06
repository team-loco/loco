#!/usr/bin/env bash
# E2E tests for the agent lifecycle:
#   - Agent registration
#   - Heartbeat
#   - Placement sync
#   - Controller running in Kind and reconciling the Application
#
# These functions are sourced by run.sh and called automatically.
# lib.sh helpers (assert, assert_contains, e2e_psql, etc.) are available.

test_api_health() {
    assert "API /health returns 200" \
        curl -sf "${E2E_API_URL}/health"
}

test_agent_registered() {
    local version
    version=$(e2e_psql "SELECT agent_version FROM clusters WHERE id = '00000000-0000-7000-8000-000000000005'")
    assert "Agent registered with correct version" \
        test "$version" = "e2e-test"
}

test_agent_heartbeat() {
    # Wait a bit for at least one heartbeat cycle
    sleep 5

    local heartbeat
    heartbeat=$(e2e_psql "SELECT last_heartbeat IS NOT NULL FROM clusters WHERE id = '00000000-0000-7000-8000-000000000005'")
    assert "Agent sent at least one heartbeat" \
        test "$heartbeat" = "t"

    local health
    health=$(e2e_psql "SELECT health_status FROM clusters WHERE id = '00000000-0000-7000-8000-000000000005'")
    assert "Cluster health status is 'healthy'" \
        test "$health" = "healthy"
}

test_agent_capacity_reported() {
    local cpu
    cpu=$(e2e_psql "SELECT capacity_cpu_millicores FROM clusters WHERE id = '00000000-0000-7000-8000-000000000005'")
    assert "Agent reported CPU capacity" \
        test "$cpu" -gt 0

    local mem
    mem=$(e2e_psql "SELECT capacity_memory_bytes FROM clusters WHERE id = '00000000-0000-7000-8000-000000000005'")
    assert "Agent reported memory capacity" \
        test "$mem" -gt 0
}

test_crds_installed() {
    assert "Application CRD is installed in Kind" \
        kubectl get crd applications.infra.loco.io --context "kind-${E2E_KIND_CLUSTER}"
}

test_loco_namespace_exists() {
    assert "loco-system namespace exists" \
        kubectl get namespace "$E2E_LOCO_NAMESPACE" --context "kind-${E2E_KIND_CLUSTER}"
}

e2e_placement_id='00000000-0000-7000-8000-000000000013'
e2e_resource_id='00000000-0000-7000-8000-000000000010'

placement_applied() {
    test "$(e2e_psql "SELECT applied_revision FROM placements WHERE id = '${e2e_placement_id}'")" = "1"
}

application_annotated() {
    kubectl get application "resource-${e2e_resource_id}" \
        --namespace "$E2E_LOCO_NAMESPACE" \
        --context "kind-${E2E_KIND_CLUSTER}" \
        -o jsonpath='{.metadata.annotations.loco\.io/placement-revision}' | grep -qx 1
}

test_agent_applies_placement() {
    e2e_psql "
        INSERT INTO resources (id, workspace_id, name, type, description, status, spec, spec_version)
        VALUES (
            '${e2e_resource_id}',
            '00000000-0000-7000-8000-000000000003',
            'e2e-test-resource',
            'service',
            'E2E test resource',
            'deploying',
            '{\"image\": \"nginx:latest\", \"port\": 80}',
            1
        ) ON CONFLICT (workspace_id, name) DO NOTHING;
    " >/dev/null

    e2e_psql "
        INSERT INTO placements (id, resource_id, cluster_id, region, desired_spec)
        VALUES (
            '${e2e_placement_id}',
            '${e2e_resource_id}',
            '00000000-0000-7000-8000-000000000005',
            'us-east-1',
            '{\"resource_id\": \"${e2e_resource_id}\", \"app_spec\": {\"type\": \"SERVICE\"}}'
        ) ON CONFLICT DO NOTHING;
        SELECT pg_notify('placements', '00000000-0000-7000-8000-000000000005');
    " >/dev/null

    wait_for "agent to apply the placement" 30 application_annotated
    assert "Agent created the Application at placement revision 1" application_annotated

    wait_for "control plane to record the applied revision" 30 placement_applied
    assert "Placement applied revision recorded as 1" placement_applied
}

application_has_status() {
    kubectl get application "resource-${e2e_resource_id}" \
        --namespace "$E2E_LOCO_NAMESPACE" \
        --context "kind-${E2E_KIND_CLUSTER}" \
        -o jsonpath='{.status.phase}' | grep -q .
}

test_controller_running() {
    assert "Controller Deployment is available in Kind" \
        kubectl rollout status deployment/controller-loco-manager \
            --namespace "$E2E_LOCO_NAMESPACE" \
            --context "kind-${E2E_KIND_CLUSTER}" \
            --timeout 60s
}

test_controller_reconciles_application() {
    wait_for "controller to write the Application status" 60 application_has_status
    assert "Controller wrote a status on the agent's Application" application_has_status
}

test_agent_logs_no_errors() {
    local log_file="${SCRIPT_DIR:-$(cd "$(dirname "$0")/.." && pwd)}/logs/agent.log"
    if [ -f "$log_file" ]; then
        local error_count
        error_count=$(grep -c '"level":"ERROR"' "$log_file" 2>/dev/null || true)
        assert "Agent has no ERROR-level log entries" \
            test "$error_count" -eq 0
    else
        log_warn "Agent log file not found, skipping log check"
        E2E_SKIP=$((E2E_SKIP + 1))
    fi
}
