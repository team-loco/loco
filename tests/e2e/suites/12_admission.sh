#!/usr/bin/env bash

if [ "${E2E_BUILDS_ENABLED:-true}" = false ]; then
    E2E_SKIP_REASON="builds are disabled"
fi

test_a00_create_resource() {
    admission_resource=$(e2e_api loco.resource.v1.ResourceService/CreateResource \
        "{\"workspaceId\":\"$E2E_WORKSPACE_ID\",\"name\":\"admission-probe\",\"type\":\"RESOURCE_TYPE_SERVICE\",\"spec\":{\"service\":{\"routing\":{\"port\":8080}}}}" | yq -p json -r .resourceId)
}

admission_fill() {
    e2e_psql "INSERT INTO api_admission (key, used, expires_at) VALUES ('$1', 2147483647, NOW() + interval '1 hour') ON CONFLICT (key) DO UPDATE SET used = EXCLUDED.used, expires_at = EXCLUDED.expires_at" >/dev/null
}

admission_rejected() {
    local method=$1 body=$2 response
    response=$(curl -sS -X POST "$E2E_API_URL/$method" \
        -H "Authorization: Bearer $E2E_USER_TOKEN" -H 'Content-Type: application/json' -d "$body")
    test "$(printf '%s' "$response" | yq -p json -r .code)" = resource_exhausted
    [[ "$response" == *"retry after"* ]]
}

test_a01_build_budget_precedes_uploads() {
    admission_fill builds:global
    assert "build admission returns retry guidance" admission_rejected loco.build.v1.BuildService/CreateBuild \
        "{\"resourceId\":\"$admission_resource\",\"sourceSize\":10,\"dockerfilePath\":\"Dockerfile\"}"
    assert "rejected builds create no row" test "$(e2e_psql "SELECT count(*) FROM builds WHERE resource_id = '$admission_resource'")" -eq 0
    e2e_psql "DELETE FROM api_admission WHERE key = 'builds:global'" >/dev/null
}

test_a02_deploy_budget_precedes_deployments() {
    admission_fill deploys:global
    assert "deploy admission returns retry guidance" admission_rejected loco.deployment.v1.DeploymentService/CreateDeployment \
        "{\"resourceId\":\"$admission_resource\",\"environmentId\":\"$E2E_ENVIRONMENT_ID\",\"region\":\"us-east-1\",\"spec\":{\"service\":{\"build\":{\"type\":\"image\",\"image\":\"$E2E_PUBLIC_IMAGE\"}}}}"
    assert "rejected deployments create no row" test "$(e2e_psql "SELECT count(*) FROM deployments WHERE resource_id = '$admission_resource'")" -eq 0
    e2e_psql "DELETE FROM api_admission WHERE key = 'deploys:global'" >/dev/null
}

test_a03_control_plane_survives_request_pressure() {
    admission_pressure_start=$(e2e_psql "SELECT clock_timestamp()")
    admission_fill requests:global
    assert "ordinary requests are throttled" admission_rejected loco.resource.v1.ResourceService/ListWorkspaceResources \
        "{\"workspaceId\":\"$E2E_WORKSPACE_ID\"}"
    assert "health remains available" curl -fsS "$E2E_API_URL/health"
    assert "ingest keys remain available" curl -fsS "$E2E_API_URL/.well-known/loco-ingest-jwks.json"
    assert "agent sync continues" wait_for "agent heartbeat under admission pressure" 60 admission_heartbeat
    e2e_psql "DELETE FROM api_admission WHERE key = 'requests:global'" >/dev/null
}

admission_heartbeat() {
    test "$(e2e_psql "SELECT last_heartbeat > '$admission_pressure_start'::timestamptz FROM clusters WHERE id = '$E2E_CLUSTER_ID'")" = t
}

suite_diagnostics() {
    e2e_psql "DELETE FROM api_admission WHERE key IN ('requests:global', 'builds:global', 'deploys:global')" >/dev/null
}
