#!/usr/bin/env bash

source "$E2E_ROOT_DIR/e2e/cli.sh"

ingress_ctx="kind-${E2E_KIND_CLUSTER}"
ingress_dir="$E2E_BUILD_WORK_DIR/ingress"
cli_home="$ingress_dir/home"
ingress_namespace="ws-${cli_workspace_id}"
ingress_public_app="e2e-ingress-public"
ingress_private_app="e2e-ingress-private"
ingress_public_host="${ingress_public_app}.e2e.test.local"
ingress_image="$E2E_PUBLIC_IMAGE"
ingress_port=8080

ik() {
    kubectl --context "$ingress_ctx" "$@"
}

ingress_resource_id() {
    e2e_psql "SELECT id FROM resources WHERE name = '$1'"
}

ingress_deploy() {
    local app=$1
    local rc=0
    (cd "$ingress_dir/$app" && loco_cli deploy "$app" --image "$ingress_image" --wait) \
        >"$ingress_dir/$app.out" 2>"$ingress_dir/$app.err" || rc=$?
    if ! assert "loco deploy ${app} (exit ${rc})" test "$rc" -eq 0; then
        sed 's/^/    /' "$ingress_dir/$app.out" "$ingress_dir/$app.err"
        return 1
    fi
}

application_phase() {
    local id
    id=$(ingress_resource_id "$1")
    ik -n "$E2E_LOCO_NAMESPACE" get application "resource-${id}" -o jsonpath='{.status.phase}'
}

application_is_ready() {
    test "$(application_phase "$1")" = Ready
}

route_exists() {
    local id
    id=$(ingress_resource_id "$1")
    ik -n "$ingress_namespace" get httproute "resource-${id}-route"
}

route_absent() {
    ! route_exists "$1"
}

gateway_policy_exists() {
    local id
    id=$(ingress_resource_id "$1")
    ik -n "$ingress_namespace" get networkpolicy "resource-${id}-gateway"
}

route_hostnames() {
    local id
    id=$(ingress_resource_id "$1")
    ik -n "$ingress_namespace" get httproute "resource-${id}-route" -o jsonpath='{.spec.hostnames[*]}'
}

probe_reaches() {
    local namespace=$1 probe=$2 app=$3 labels=$4
    local id
    id=$(ingress_resource_id "$app")
    local target="http://resource-${id}.${ingress_namespace}.svc.cluster.local/"
    ik delete pod "$probe" --namespace "$namespace" --ignore-not-found --wait >/dev/null
    ik apply -f - >/dev/null <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: ${probe}
  namespace: ${namespace}
  labels: ${labels}
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: probe
      image: ${ingress_image}
      command: ["wget", "-q", "-T", "5", "-O", "/dev/null", "${target}"]
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: ["ALL"]
YAML
    ik wait "pod/${probe}" \
        --namespace "$namespace" \
        --for=jsonpath='{.status.phase}'=Succeeded \
        --timeout 30s >/dev/null 2>&1
}

gateway_reaches() {
    local labels="{gateway.envoyproxy.io/owning-gateway-name: eg, gateway.envoyproxy.io/owning-gateway-namespace: ${E2E_LOCO_NAMESPACE}}"
    probe_reaches "$E2E_LOCO_NAMESPACE" "e2e-gateway-probe-$2" "$1" "$labels"
}

workspace_reaches() {
    probe_reaches "$ingress_namespace" "e2e-workspace-probe-$2" "$1" "{}"
}

test_i01_deploys_with_and_without_a_domain() {
    cli_seed_session
    rm -rf "$ingress_dir"
    mkdir -p "$ingress_dir/$ingress_public_app" "$ingress_dir/$ingress_private_app"
    cli_write_credentials
    cli_write_service_config "$ingress_dir/$ingress_public_app" "$ingress_public_app" "$ingress_port" /
    cli_write_private_service_config "$ingress_dir/$ingress_private_app" "$ingress_private_app" "$ingress_port" /

    ingress_deploy "$ingress_public_app" || return 1
    assert_contains "The CLI prints the public app's URL" "Public URL: https://${ingress_public_host}" \
        cat "$ingress_dir/$ingress_public_app.out"

    ingress_deploy "$ingress_private_app" || return 1
    assert_contains "The CLI says the private app has no public URL" \
        "${ingress_private_app} has no public URL" cat "$ingress_dir/$ingress_private_app.out"
    assert "The private resource has no domain" has_no_domain "$ingress_private_app"
}

test_i02_both_apps_become_ready() {
    wait_for "the public app to be Ready" 120 application_is_ready "$ingress_public_app"
    assert "The public Application is Ready" application_is_ready "$ingress_public_app"
    wait_for "the private app to be Ready" 120 application_is_ready "$ingress_private_app"
    assert "The private Application is Ready" application_is_ready "$ingress_private_app"
}

test_i03_only_the_public_app_is_routed() {
    assert_contains "The public app's HTTPRoute serves its hostname" "$ingress_public_host" \
        route_hostnames "$ingress_public_app"
    assert "The public app has a gateway ingress policy" gateway_policy_exists "$ingress_public_app"
    assert_fails "The private app has no HTTPRoute" route_exists "$ingress_private_app"
    assert_fails "The private app has no gateway ingress policy" gateway_policy_exists "$ingress_private_app"
}

test_i04_only_the_public_app_is_reachable_from_the_gateway() {
    assert "A gateway pod reaches the public app" gateway_reaches "$ingress_public_app" public
    assert_fails "A gateway pod cannot reach the private app" gateway_reaches "$ingress_private_app" private
    assert "A pod in the workspace reaches the private app" workspace_reaches "$ingress_private_app" private
}

test_i05_status_reports_the_url() {
    assert_contains "loco resource status shows the public URL" "https://${ingress_public_host}" \
        loco_cli resource status "$ingress_public_app"
    assert_contains "loco resource status shows no URL for the private app" "URL: *none" \
        loco_cli resource status "$ingress_private_app"
}

ingress_api() {
    curl -sS --fail-with-body -X POST "${E2E_API_URL}/$1" \
        -H "Authorization: Bearer ${E2E_USER_TOKEN}" \
        -H 'Content-Type: application/json' \
        -d "$2"
}

ingress_resource() {
    local body="{\"nameKey\":{\"workspaceId\":\"${cli_workspace_id}\",\"name\":\"$1\"}}"
    ingress_api loco.resource.v1.ResourceService/GetResource "$body"
}

ingress_domain_id() {
    local resource
    resource=$(ingress_resource "$1") || return 1
    yq -p json -r '.resource.domains[0].id // ""' <<<"$resource"
}

remove_domain() {
    local id
    id=$(ingress_domain_id "$1")
    test -n "$id" || return 1
    ingress_api loco.domain.v1.DomainService/DeleteResourceDomain "{\"domainId\":\"${id}\"}"
}

has_no_domain() {
    local resource
    resource=$(ingress_resource "$1") || return 1
    test "$(yq -p json -r '.resource.domains | length' <<<"$resource")" -eq 0
}

test_i06_losing_the_domain_removes_the_route() {
    if ! assert "The API removes the public app's only domain" remove_domain "$ingress_public_app"; then
        remove_domain "$ingress_public_app" | sed 's/^/    /'
        return 1
    fi
    assert "The resource has no domain left" has_no_domain "$ingress_public_app"
    local rc=0
    loco_cli resource scale "$ingress_public_app" --replicas 2 >"$ingress_dir/scale.out" 2>&1 || rc=$?
    if ! assert "loco resource scale redeploys the app without its domain (exit ${rc})" test "$rc" -eq 0; then
        sed 's/^/    /' "$ingress_dir/scale.out"
        return 1
    fi
    wait_for "the HTTPRoute to be removed" 60 route_absent "$ingress_public_app"
    assert_fails "The app that lost its domain has no HTTPRoute" route_exists "$ingress_public_app"
    assert_fails "The app that lost its domain has no gateway ingress policy" gateway_policy_exists "$ingress_public_app"
    wait_for "the app to be Ready again" 120 application_is_ready "$ingress_public_app"
    assert "The app stays Ready without its domain" application_is_ready "$ingress_public_app"
    assert_fails "A gateway pod can no longer reach the app" gateway_reaches "$ingress_public_app" lost
}
