#!/usr/bin/env bash
# E2E tests for the agent lifecycle:
#   - Agent registration
#   - Heartbeat
#   - Command dispatch and execution
#
# These functions are sourced by run.sh and called automatically.
# lib.sh helpers (assert, assert_contains, e2e_psql, etc.) are available.

test_api_health() {
    assert "API /health returns 200" \
        curl -sf "${E2E_API_URL}/health"
}

test_agent_registered() {
    local version
    version=$(e2e_psql "SELECT agent_version FROM clusters WHERE id = 1")
    assert "Agent registered with correct version" \
        test "$version" = "e2e-test"
}

test_agent_heartbeat() {
    # Wait a bit for at least one heartbeat cycle
    sleep 5

    local heartbeat
    heartbeat=$(e2e_psql "SELECT last_heartbeat IS NOT NULL FROM clusters WHERE id = 1")
    assert "Agent sent at least one heartbeat" \
        test "$heartbeat" = "t"

    local health
    health=$(e2e_psql "SELECT health_status FROM clusters WHERE id = 1")
    assert "Cluster health status is 'healthy'" \
        test "$health" = "healthy"
}

test_agent_capacity_reported() {
    local cpu
    cpu=$(e2e_psql "SELECT capacity_cpu_millicores FROM clusters WHERE id = 1")
    assert "Agent reported CPU capacity" \
        test "$cpu" -gt 0

    local mem
    mem=$(e2e_psql "SELECT capacity_memory_bytes FROM clusters WHERE id = 1")
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

test_report_status_rpc() {
    # Test the ReportStatus RPC directly (agent service has no auth interceptor)
    # First, create a dummy deployment in the DB to update
    local deploy_id
    deploy_id=$(e2e_psql "
        INSERT INTO resources (id, workspace_id, name, type, description, status, spec, spec_version)
        VALUES (
            '00000000-0000-7000-8000-000000000010',
            '00000000-0000-7000-8000-000000000003',
            'e2e-test-resource',
            'service',
            'E2E test resource',
            'deploying',
            '{\"image\": \"nginx:latest\", \"port\": 80}',
            1
        ) ON CONFLICT (workspace_id, name) DO UPDATE SET status = 'deploying'
        RETURNING id;
    ")

    # Create a resource region
    e2e_psql "
        INSERT INTO resource_regions (id, resource_id, region, is_primary, status)
        VALUES (
            '00000000-0000-7000-8000-000000000011',
            '00000000-0000-7000-8000-000000000010',
            'us-east-1',
            true,
            'active'
        ) ON CONFLICT DO NOTHING;
    " >/dev/null

    # Create a deployment
    e2e_psql "
        INSERT INTO deployments (id, resource_id, resource_region_id, cluster_id, region, replicas, status, is_active, message, spec, spec_version)
        VALUES (
            '00000000-0000-7000-8000-000000000012',
            '00000000-0000-7000-8000-000000000010',
            '00000000-0000-7000-8000-000000000011',
            1,
            'us-east-1',
            1,
            'deploying',
            true,
            'E2E test deployment',
            '{\"image\": \"nginx:latest\"}',
            1
        ) ON CONFLICT DO NOTHING;
    " >/dev/null

    # Call ReportStatus via Connect RPC (JSON POST)
    local response
    response=$(curl -sf \
        -X POST \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${E2E_AGENT_TOKEN}" \
        "${E2E_API_URL}/loco.agent.v1.AgentService/ReportStatus" \
        -d '{
            "cluster_id": 1,
            "deployment_id": "00000000-0000-7000-8000-000000000012",
            "resource_id": "00000000-0000-7000-8000-000000000010",
            "phase": "DEPLOYMENT_PHASE_RUNNING",
            "message": "e2e test: deployment running"
        }' 2>&1) || true

    # Verify the deployment status was updated in DB
    local status
    status=$(e2e_psql "SELECT status FROM deployments WHERE id = '00000000-0000-7000-8000-000000000012'")
    assert "ReportStatus updated deployment to 'running'" \
        test "$status" = "running"
}

test_agent_logs_no_errors() {
    local log_file="${SCRIPT_DIR:-$(cd "$(dirname "$0")/.." && pwd)}/logs/agent.log"
    if [ -f "$log_file" ]; then
        local error_count
        error_count=$(grep -c '"level":"ERROR"' "$log_file" 2>/dev/null || echo "0")
        assert "Agent has no ERROR-level log entries" \
            test "$error_count" -eq 0
    else
        log_warn "Agent log file not found, skipping log check"
        E2E_SKIP=$((E2E_SKIP + 1))
    fi
}
