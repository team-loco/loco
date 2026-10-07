#!/usr/bin/env bash
# E2E tests for the agent lifecycle:
#   - Agent registration
#   - Heartbeat
#   - Placement sync
#   - Controller running in Kind, rolling the app out to Ready
#   - Network isolation of the workspace namespace
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
e2e_workspace_id='00000000-0000-7000-8000-000000000003'
e2e_app_image='nginxinc/nginx-unprivileged:1.31.6-alpine'
e2e_desired_spec=$(cat <<JSON
{
  "resource_id": "${e2e_resource_id}",
  "workspace_id": "${e2e_workspace_id}",
  "resource_name": "e2e-test-resource",
  "resource_type": "service",
  "region": "us-east-1",
  "app_spec": {
    "type": "SERVICE",
    "resourceId": "${e2e_resource_id}",
    "workspaceId": "${e2e_workspace_id}",
    "region": "us-east-1",
    "environmentName": "production",
    "serviceSpec": {
      "deployment": {
        "image": "${e2e_app_image}",
        "port": 8080,
        "healthCheck": {"path": "/", "interval": 5, "timeout": 2, "failThreshold": 3}
      },
      "resources": {"cpu": "100m", "memory": "64Mi", "replicas": {"min": 1, "max": 1}}
    }
  }
}
JSON
)

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
        INSERT INTO resources (id, workspace_id, environment_id, stack_id, service_key, name, type, description, status, spec, spec_version)
        VALUES (
            '${e2e_resource_id}',
            '00000000-0000-7000-8000-000000000003',
            '00000000-0000-7000-8000-000000000004',
            '00000000-0000-7000-8000-000000000007',
            'e2e-test-resource',
            'e2e-test-resource',
            'service',
            'E2E test resource',
            'deploying',
            '{\"image\": \"nginx:latest\", \"port\": 80}',
            1
        ) ON CONFLICT (environment_id, name) DO NOTHING;
    " >/dev/null

    e2e_psql "
        INSERT INTO placements (id, resource_id, cluster_id, region, desired_spec)
        VALUES (
            '${e2e_placement_id}',
            '${e2e_resource_id}',
            '00000000-0000-7000-8000-000000000005',
            'us-east-1',
            \$spec\$${e2e_desired_spec}\$spec\$
        ) ON CONFLICT DO NOTHING;
        SELECT pg_notify('placements', '00000000-0000-7000-8000-000000000005');
    " >/dev/null

    wait_for "agent to apply the placement" 30 application_annotated
    assert "Agent created the Application at placement revision 1" application_annotated

    wait_for "control plane to record the applied revision" 30 placement_applied
    assert "Placement applied revision recorded as 1" placement_applied
}

application_ready() {
    kubectl get application "resource-${e2e_resource_id}" \
        --namespace "$E2E_LOCO_NAMESPACE" \
        --context "kind-${E2E_KIND_CLUSTER}" \
        -o jsonpath='{.status.phase}' | grep -qx Ready
}

placement_reported_ready() {
    test "$(e2e_psql "SELECT ready AND observed_revision = 1 FROM placements WHERE id = '${e2e_placement_id}'")" = "t"
}

test_application_becomes_ready() {
    wait_for "the app to roll out" 180 application_ready
    assert "Controller rolled the app out and marked the Application Ready" application_ready

    wait_for "the agent to report the app ready" 30 placement_reported_ready
    assert "Placement records the app ready at revision 1" placement_reported_ready
}

probe_reaches_app() {
    local namespace="$1"
    local probe="$2"
    local target="http://resource-${e2e_resource_id}.ws-${e2e_workspace_id}.svc.cluster.local/"
    kubectl apply --context "kind-${E2E_KIND_CLUSTER}" -f - >/dev/null <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: ${probe}
  namespace: ${namespace}
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: probe
      image: ${e2e_app_image}
      command: ["wget", "-q", "-T", "5", "-O", "/dev/null", "${target}"]
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: ["ALL"]
YAML
    kubectl wait "pod/${probe}" \
        --namespace "$namespace" \
        --context "kind-${E2E_KIND_CLUSTER}" \
        --for=jsonpath='{.status.phase}'=Succeeded \
        --timeout 30s >/dev/null 2>&1
}

test_network_isolation() {
    local workspace_namespace="ws-${e2e_workspace_id}"
    kubectl create namespace e2e-outsider --context "kind-${E2E_KIND_CLUSTER}" >/dev/null 2>&1 || true

    assert "A pod in the app's workspace namespace reaches the app" \
        probe_reaches_app "$workspace_namespace" e2e-probe-inside
    assert_fails "A pod in another namespace cannot reach the app" \
        probe_reaches_app e2e-outsider e2e-probe-outside
}

test_controller_running() {
    assert "Controller Deployment is available in Kind" \
        kubectl rollout status deployment/controller-loco-manager \
            --namespace "$E2E_LOCO_NAMESPACE" \
            --context "kind-${E2E_KIND_CLUSTER}" \
            --timeout 60s
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
