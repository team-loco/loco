#!/usr/bin/env bash

source "$E2E_ROOT_DIR/e2e/cli.sh"

if [ "${E2E_BUILDS_ENABLED:-true}" = false ]; then
    E2E_SKIP_REASON="builds are disabled"
fi

discovery_ctx="kind-${E2E_KIND_CLUSTER}"
discovery_dir="$E2E_BUILD_WORK_DIR/discovery"
discovery_namespace="${E2E_WORKSPACE_PREFIX}${cli_workspace_id}"
cli_home="$discovery_dir/home"
discovery_http=cart
discovery_caller=caller
discovery_tcp=payment
discovery_renamed=checkout
discovery_second_workspace='00000000-0000-7000-8000-000000000023'
discovery_second_environment='00000000-0000-7000-8000-000000000024'

discovery_failures_before=$E2E_FAIL

discovery_diagnostics() {
    local status=$1
    if [ "$status" -ne 0 ] || [ "$E2E_FAIL" -gt "$discovery_failures_before" ]; then
        python3 "$E2E_ROOT_DIR/e2e/discovery_diagnostics.py" \
            --kubeconfig "$KUBECONFIG_FILE" --context "$discovery_ctx" \
            --namespace "$discovery_namespace" --namespace "${E2E_WORKSPACE_PREFIX}${discovery_second_workspace}" \
            --output "$E2E_ROOT_DIR/e2e/out/discovery/$E2E_RUN_ID" || true
    fi
    return "$status"
}

trap 'discovery_diagnostics "$?"' EXIT

dk() {
    kubectl --context "$discovery_ctx" "$@"
}

discovery_api() {
    curl -sS --fail-with-body -X POST "${E2E_API_URL}/$1" \
        -H "Authorization: Bearer ${E2E_USER_TOKEN}" \
        -H 'Content-Type: application/json' -d "$2"
}

discovery_resource_id() {
    e2e_psql "SELECT id FROM resources WHERE name = '$1' AND workspace_id = '${cli_workspace_id}'"
}

discovery_deploy() {
    local rc=0
    (cd "$discovery_dir" && loco_cli deploy --yes) >"$discovery_dir/deploy.out" 2>"$discovery_dir/deploy.err" || rc=$?
    if ! assert "loco deploy applies the discovery services (exit ${rc})" test "$rc" -eq 0; then
        cat "$discovery_dir/deploy.out" "$discovery_dir/deploy.err"
        return 1
    fi
}

discovery_call() {
    local target=$1 protocol=$2 id path=${3:-/}
    id=$(discovery_resource_id "$discovery_caller")
    if [ "$protocol" = http ]; then
        dk -n "$discovery_namespace" exec "deployment/resource-${id}" -- curl --fail --silent --show-error --max-time 5 "http://${target}${path}"
    else
        dk -n "$discovery_namespace" exec "deployment/resource-${id}" -- sh -c 'printf "private-tcp-ok\n" | nc -w 5 "$1" "$2"' -- "${target%:*}" "${target##*:}"
    fi
}

discovery_alias_absent() {
    ! dk -n "$discovery_namespace" get service "$1" >/dev/null 2>&1
}

discovery_alias_exists() {
    dk -n "$discovery_namespace" get service "$1" >/dev/null 2>&1
}

discovery_is_ready() {
    local id
    id=$(discovery_resource_id "$1")
    test "$(dk -n "$E2E_LOCO_NAMESPACE" get application "resource-${id}" -o jsonpath='{.status.phase}')" = Ready
}

discovery_unreachable() {
    ! discovery_call "$1" http >/dev/null 2>&1
}

test_d01_deploys_private_http_and_tcp_services() {
    cli_seed_session
    e2e_psql "INSERT INTO user_scopes (user_id, scope, entity_type, entity_id)
        VALUES ('${cli_user_id}', 'admin', 'workspace', '${cli_workspace_id}')
        ON CONFLICT DO NOTHING;" >/dev/null
    rm -rf "$discovery_dir"
    mkdir -p "$discovery_dir/services/payment" "$discovery_dir/services/cart"
    cli_write_credentials
    cp -R "$E2E_ROOT_DIR/e2e/fixtures/tcp-app/." "$discovery_dir/services/payment/"
    cp -R "$E2E_ROOT_DIR/e2e/fixtures/build-app/." "$discovery_dir/services/cart/"
    sed 's/USER 65532:65532/USER nonroot/' "$discovery_dir/services/cart/deploy/Dockerfile" >"$discovery_dir/services/cart/deploy/Dockerfile.runtime"
    mv "$discovery_dir/services/cart/deploy/Dockerfile.runtime" "$discovery_dir/services/cart/deploy/Dockerfile"
    cat >"$discovery_dir/loco.yaml" <<YAML
version: 1
partial: discovery
services:
  ${discovery_http}:
    context: services/cart
    dockerfile: deploy/Dockerfile
    env: { MESSAGE: workspace-one }
    runAsUser: 1000
    runAsGroup: 1000
    port: 8080
    regions:
      us-east-1: { cpu: 100m, memory: 128Mi, replicas: { min: 1, max: 1 } }
  ${discovery_caller}:
    image: ${E2E_PUBLIC_IMAGE}
    port: 8080
    regions:
      us-east-1: { cpu: 100m, memory: 128Mi, replicas: { min: 1, max: 1 } }
  ${discovery_tcp}:
    context: services/payment
    dockerfile: deploy/Dockerfile
    port: 50051
    regions:
      us-east-1: { cpu: 100m, memory: 128Mi, replicas: { min: 1, max: 1 } }
YAML
    discovery_deploy || return 1
    wait_for "cart to be Ready" 120 discovery_is_ready "$discovery_http"
    wait_for "caller to be Ready" 120 discovery_is_ready "$discovery_caller"
    wait_for "payment to be Ready without HTTP health probes" 120 discovery_is_ready "$discovery_tcp"
    local id
    id=$(discovery_resource_id "$discovery_tcp")
    local probes
    probes=$(dk -n "$discovery_namespace" get deployment "resource-${id}" -o json | \
        yq -p json '.spec.template.spec.containers[0] | has("livenessProbe") or has("readinessProbe")')
    assert "payment has no HTTP probes" test "$probes" = false
}

test_d02_apps_use_logical_names_and_configured_ports() {
    assert_contains "numeric UID overrides a named image USER" '1000:1000' discovery_call cart http /identity
    assert_contains "an app reaches http://cart.${discovery_namespace}.svc.cluster.local with no port" 'workspace-one' discovery_call "cart.${discovery_namespace}.svc.cluster.local" http
    assert_contains "short native DNS name reaches cart" 'workspace-one' discovery_call cart http
    assert_contains "an app reaches cart:8080 by HTTP" 'workspace-one' discovery_call "${discovery_http}:8080" http
    assert_contains "an app exchanges non-HTTP TCP with payment:50051" 'private-tcp-ok' discovery_call "${discovery_tcp}:50051" tcp
    local id
    id=$(discovery_resource_id "$discovery_tcp")
    assert_contains "the UUID endpoint remains on port 80" 'private-tcp-ok' discovery_call "resource-${id}:80" tcp
    assert_contains "the private Service uses TCP" 'TCP' \
        dk -n "$discovery_namespace" get service "$discovery_tcp" -o 'jsonpath={.spec.ports[0].protocol}'
}

discovery_runtime_user_is_updated() {
    test "$(discovery_call cart http /identity)" = "1001:1000"
}

test_d02_runtime_user_update_reuses_the_image() {
    local before after rc=0
    before=$(e2e_psql "SELECT count(*) FROM builds")
    yq -i '.services.cart.runAsUser = 1001' "$discovery_dir/loco.yaml"
    (cd "$discovery_dir" && loco_cli infra apply --yes) >"$discovery_dir/runtime-user.out" 2>"$discovery_dir/runtime-user.err" || rc=$?
    if ! assert "runtime UID change applies without a build" test "$rc" -eq 0; then
        cat "$discovery_dir/runtime-user.out" "$discovery_dir/runtime-user.err"
        return 1
    fi
    assert "new pods run with the updated UID" wait_for "runtime UID rollout" 120 discovery_runtime_user_is_updated
    after=$(e2e_psql "SELECT count(*) FROM builds")
    assert "runtime UID update starts no build" test "$before" -eq "$after"
}

test_d03_resource_rename_moves_the_alias_without_a_build() {
    local id before after
    id=$(discovery_resource_id "$discovery_tcp")
    before=$(e2e_psql "SELECT count(*) FROM builds")
    assert "rename succeeds through the Resource API" discovery_api loco.resource.v1.ResourceService/UpdateResource \
        "{\"resourceId\":\"${id}\",\"name\":\"${discovery_renamed}\"}"
    wait_for "the renamed alias" 60 discovery_alias_exists "$discovery_renamed"
    wait_for "the previous alias to be removed" 60 discovery_alias_absent "$discovery_tcp"
    assert_contains "an app reaches the renamed TCP endpoint" 'private-tcp-ok' discovery_call "${discovery_renamed}:50051" tcp
    after=$(e2e_psql "SELECT count(*) FROM builds")
    assert "rename starts no build" test "$before" -eq "$after"
}

test_d04_resource_delete_removes_only_its_alias() {
    local id
    id=$(discovery_resource_id "$discovery_renamed")
    assert "delete succeeds through the Resource API" discovery_api loco.resource.v1.ResourceService/DeleteResource "{\"resourceId\":\"${id}\"}"
    wait_for "the deleted service alias to disappear" 60 discovery_alias_absent "$discovery_renamed"
    assert "cart still has its logical endpoint" discovery_alias_exists "$discovery_http"
    assert_contains "the other app remains reachable" 'workspace-one' discovery_call "${discovery_http}:8080" http
}

test_d05_namespace_qualified_names_reach_their_workspace() {
    e2e_psql "
        INSERT INTO workspaces (id, org_id, name, description, created_by)
        VALUES ('${discovery_second_workspace}', '${cli_org_id}', 'e2e-discovery-two', 'Private DNS isolation', '${cli_user_id}') ON CONFLICT DO NOTHING;
        INSERT INTO environments (id, workspace_id, name, environment_type, created_by)
        VALUES ('${discovery_second_environment}', '${discovery_second_workspace}', 'production', 'production', '${cli_user_id}') ON CONFLICT DO NOTHING;
        INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) VALUES
        ('${cli_user_id}', 'read', 'workspace', '${discovery_second_workspace}'),
        ('${cli_user_id}', 'write', 'workspace', '${discovery_second_workspace}') ON CONFLICT DO NOTHING;
    " >/dev/null
    discovery_deploy_second_workspace || return 1
    assert_contains "first workspace still resolves its own http://cart.${discovery_namespace}.svc.cluster.local" workspace-one discovery_call "cart.${discovery_namespace}.svc.cluster.local" http
    local other_address
    other_address=$(dk -n "${E2E_WORKSPACE_PREFIX}${discovery_second_workspace}" get service cart -o 'jsonpath={.spec.clusterIP}')
    assert "cross-workspace traffic remains blocked" discovery_unreachable "$other_address"
}


discovery_deploy_second_workspace() {
    local cli_workspace_id=$discovery_second_workspace
    local discovery_namespace="${E2E_WORKSPACE_PREFIX}${discovery_second_workspace}"
    local discovery_dir="$E2E_BUILD_WORK_DIR/discovery-two"
    local cli_home="$discovery_dir/home"
    mkdir -p "$discovery_dir/services/cart"
    cp -R "$E2E_ROOT_DIR/e2e/fixtures/build-app/." "$discovery_dir/services/cart/"
    cli_write_credentials
    cat >"$discovery_dir/loco.yaml" <<YAML
version: 1
partial: discovery-two
services:
  cart:
    context: services/cart
    dockerfile: deploy/Dockerfile
    port: 8080
    env: { MESSAGE: workspace-two }
    regions:
      us-east-1: { cpu: 100m, memory: 128Mi, replicas: { min: 1, max: 1 } }
  caller:
    image: ${E2E_PUBLIC_IMAGE}
    port: 8080
    regions:
      us-east-1: { cpu: 100m, memory: 128Mi, replicas: { min: 1, max: 1 } }
YAML
    discovery_deploy || return 1
    wait_for "second workspace cart" 120 discovery_is_ready cart
    wait_for "second-workspace caller" 120 discovery_is_ready caller
    assert_contains "second-workspace app reaches its own http://cart.${discovery_namespace}.svc.cluster.local with no port" workspace-two discovery_call "cart.${discovery_namespace}.svc.cluster.local" http
}
