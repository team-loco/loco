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
    version=$(e2e_psql "SELECT agent_version FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")
    assert "Agent registered with correct version" \
        test "$version" = "$E2E_VERSION"
}

test_binaries_report_their_build_version() {
    assert_contains "API logs the version it was built as" "version=${E2E_VERSION}" \
        grep "starting loco api" "$E2E_LOG_DIR/api.log"
    assert_contains "Observability proxy logs the version it was built as" "\"version\":\"${E2E_VERSION}\"" \
        grep "starting observability proxy" "$E2E_LOG_DIR/obs-proxy.log"
}

agent_heartbeat_recorded() {
    test "$(e2e_psql "SELECT last_heartbeat IS NOT NULL FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")" = "t"
}

test_agent_heartbeat() {
    wait_for "the agent's first heartbeat" 30 agent_heartbeat_recorded || true

    local heartbeat
    heartbeat=$(e2e_psql "SELECT last_heartbeat IS NOT NULL FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")
    assert "Agent sent at least one heartbeat" \
        test "$heartbeat" = "t"

    local health
    health=$(e2e_psql "SELECT health_status FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")
    assert "Cluster health status is 'healthy'" \
        test "$health" = "healthy"
}

test_agent_capacity_reported() {
    local cpu
    cpu=$(e2e_psql "SELECT capacity_cpu_millicores FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")
    assert "Agent reported CPU capacity" \
        test "$cpu" -gt 0

    local mem
    mem=$(e2e_psql "SELECT capacity_memory_bytes FROM clusters WHERE id = '${E2E_CLUSTER_ID}'")
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

source "$E2E_ROOT_DIR/tests/e2e/cli.sh"

e2e_app="e2e-agent-app"
e2e_app_image="$E2E_PUBLIC_IMAGE"
e2e_app_dir="$E2E_BUILD_WORK_DIR/agent-lifecycle"
cli_home="$e2e_app_dir/home"

e2e_resource_id() {
    e2e_psql "SELECT id FROM resources WHERE name = '${e2e_app}'"
}

placement_applied() {
    test "$(e2e_psql "SELECT p.applied_revision FROM placements p JOIN resources r ON r.id = p.resource_id WHERE r.name = '${e2e_app}'")" = "1"
}

application_annotated() {
    kubectl get application "resource-$(e2e_resource_id)" \
        --namespace "$E2E_LOCO_NAMESPACE" \
        --context "kind-${E2E_KIND_CLUSTER}" \
        -o jsonpath='{.metadata.annotations.loco\.io/placement-revision}' | grep -qx 1
}

test_agent_applies_placement() {
    cli_seed_session
    rm -rf "$e2e_app_dir"
    mkdir -p "$e2e_app_dir"
    cli_write_credentials
    cli_write_image_config "$e2e_app_dir" "$e2e_app" "$e2e_app_image" 8080 "[]"

    local rc=0
    (cd "$e2e_app_dir" && loco_cli deploy --yes) >"$e2e_app_dir/deploy.out" 2>"$e2e_app_dir/deploy.err" || rc=$?
    if ! assert "loco deploy ${e2e_app} (exit ${rc})" test "$rc" -eq 0; then
        sed 's/^/    /' "$e2e_app_dir/deploy.out" "$e2e_app_dir/deploy.err"
        return 1
    fi

    wait_for "agent to apply the placement" 30 application_annotated
    assert "Agent created the Application at placement revision 1" application_annotated

    wait_for "control plane to record the applied revision" 30 placement_applied
    assert "Placement applied revision recorded as 1" placement_applied
}

application_ready() {
    kubectl get application "resource-$(e2e_resource_id)" \
        --namespace "$E2E_LOCO_NAMESPACE" \
        --context "kind-${E2E_KIND_CLUSTER}" \
        -o jsonpath='{.status.phase}' | grep -qx Ready
}

placement_reported_ready() {
    test "$(e2e_psql "SELECT p.ready AND p.observed_revision = 1 FROM placements p JOIN resources r ON r.id = p.resource_id WHERE r.name = '${e2e_app}'")" = "t"
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
    local id
    id=$(e2e_resource_id)
    local target="http://resource-${id}.${E2E_WORKSPACE_PREFIX}${cli_workspace_id}.svc.cluster.local/"
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
    local workspace_namespace="${E2E_WORKSPACE_PREFIX}${cli_workspace_id}"
    kubectl create namespace e2e-outsider --context "kind-${E2E_KIND_CLUSTER}" >/dev/null 2>&1 || true

    assert "A pod in the app's workspace namespace reaches the app" \
        probe_reaches_app "$workspace_namespace" e2e-probe-inside
    assert_fails "A pod in another namespace cannot reach the app" \
        probe_reaches_app e2e-outsider e2e-probe-outside
}

test_controller_running() {
    assert "Controller Deployment is available in Kind" \
        kubectl rollout status deployment/loco-controller \
            --namespace "$E2E_LOCO_NAMESPACE" \
            --context "kind-${E2E_KIND_CLUSTER}" \
            --timeout 60s
}

test_agent_reconnects_to_same_cluster() {
    local original_id="$E2E_CLUSTER_ID"
    kill_pid_file "$PID_DIR/agent.pid"
    E2E_JOIN_TOKEN="" start_agent
    assert "Agent restart without the join token preserves cluster ID" test "$E2E_CLUSTER_ID" = "$original_id"
    assert "Reconnect keeps a single cluster record" test "$(e2e_psql 'SELECT count(*) FROM clusters')" = 1
}
