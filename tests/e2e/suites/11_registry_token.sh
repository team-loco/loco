#!/usr/bin/env bash

source "$E2E_ROOT_DIR/tests/e2e/cli.sh"

if [ "${E2E_REGISTRY_AUTH:-static}" != token ]; then
    E2E_SKIP_REASON="the registry runs in static mode; run with --registry-token"
fi

rt_ctx="kind-${E2E_KIND_CLUSTER}"
rt_dir="$E2E_BUILD_WORK_DIR/registry-token"
rt_builds_ns="$E2E_BUILD_NAMESPACE"
rt_build_controller="loco-build-controller"
rt_ws_a='00000000-0000-7000-8000-00000000a001'
rt_env_a='00000000-0000-7000-8000-00000000a002'
rt_ws_b='00000000-0000-7000-8000-00000000b001'
rt_env_b='00000000-0000-7000-8000-00000000b002'
rt_app_a="rt-app-a"
rt_app_b="rt-app-b"
rt_pull_user="loco-pull"
rt_build_user="loco-build"
rt_issued_at="loco.io/registry-pull-issued-at"
rt_expires_at="loco.io/registry-pull-expires-at"
rt_deploy_timeout=180
rt_pull_timeout=120
rt_renewal_timeout=150

rk() {
    kubectl --context "$rt_ctx" "$@"
}

rt_save() {
    mkdir -p "$rt_dir"
    printf '%s' "$2" >"$rt_dir/$1"
}

rt_load() {
    cat "$rt_dir/$1" 2>/dev/null
}

rt_namespace() {
    echo "${E2E_WORKSPACE_PREFIX}$1"
}

rt_pull_secret() {
    echo "loco-registry-pull-$1"
}

rt_seed_workspace() {
    local workspace=$1 environment=$2 name=$3
    e2e_psql "
        INSERT INTO workspaces (id, org_id, name, description, created_by)
        VALUES ('${workspace}', '${cli_org_id}', '${name}', 'Registry token mode', '${cli_user_id}') ON CONFLICT DO NOTHING;
        INSERT INTO environments (id, workspace_id, name, environment_type, created_by)
        VALUES ('${environment}', '${workspace}', 'production', 'production', '${cli_user_id}') ON CONFLICT DO NOTHING;
        INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) VALUES
        ('${cli_user_id}', 'read', 'workspace', '${workspace}'),
        ('${cli_user_id}', 'write', 'workspace', '${workspace}'),
        ('${cli_user_id}', 'admin', 'workspace', '${workspace}') ON CONFLICT DO NOTHING;
    " >/dev/null
}

rt_write_source() {
    local dir=$1 label=$2
    rm -rf "$dir"
    mkdir -p "$dir"
    cp -R "$E2E_ROOT_DIR/tests/e2e/fixtures/build-app/." "$dir/"
    printf '\nfunc init() { log.SetPrefix("%s ") }\n' "$label" >>"$dir/main.go"
}

rt_deploy() {
    local workspace=$1 app=$2 label=$3 rc=0
    local cli_workspace_id=$workspace
    local cli_home="$rt_dir/$app/home"
    local app_dir="$rt_dir/$app/src"
    rt_write_source "$app_dir" "$label"
    cli_write_credentials
    cli_write_private_service_config "$app_dir" "$app" 8080 /healthz
    (cd "$app_dir" && loco_cli deploy --yes) >"$rt_dir/$label.out" 2>"$rt_dir/$label.err" || rc=$?
    if [ "$rc" -ne 0 ]; then
        sed 's/^/    /' "$rt_dir/$label.out" "$rt_dir/$label.err"
    fi
    return "$rc"
}

rt_resource_id() {
    e2e_psql "SELECT id FROM resources WHERE name = '$1'"
}

rt_latest_build() {
    e2e_psql "SELECT b.id FROM builds b JOIN resources r ON r.id = b.resource_id
              WHERE r.name = '$1' ORDER BY b.created_at DESC LIMIT 1"
}

rt_build() {
    e2e_psql "SELECT $2 FROM builds WHERE id = '$1'"
}

rt_repository() {
    local repository
    repository=$(rt_build "$1" image_repository)
    echo "${repository#"${E2E_REGISTRY_HOST}/"}"
}

rt_image() {
    echo "$(rt_build "$1" image_repository)@$(rt_build "$1" image_digest)"
}

rt_pull_password() {
    registry_secret_password "$E2E_LOCO_NAMESPACE" "$(rt_pull_secret "$1")"
}

rt_build_password() {
    registry_secret_password "$rt_builds_ns" "build-$1-registry"
}

rt_annotation() {
    local workspace=$1 key=${2//./\\.}
    rk -n "$E2E_LOCO_NAMESPACE" get secret "$(rt_pull_secret "$workspace")" -o jsonpath="{.metadata.annotations.${key}}"
}

rt_epoch() {
    local stamp=${1%Z}
    stamp=${stamp%%.*}
    date -u -j -f '%Y-%m-%dT%H:%M:%S' "$stamp" +%s 2>/dev/null || date -u -d "${stamp}Z" +%s
}

rt_pull_token() {
    registry_realm_token "$rt_pull_user" "$(rt_pull_password "$1")" "repository:$2:pull"
}

rt_manifest_status() {
    local workspace=$1 build=$2 repository token
    repository=$(rt_repository "$build")
    token=$(rt_pull_token "$workspace" "$repository") || return 1
    registry_status_with_token "$token" GET "/v2/${repository}/manifests/$(rt_build "$build" image_digest)"
}

rt_refused_for() {
    local repository=$1 refusal
    shift
    refusal=$(registry_refusal_with_token "$@")
    grep -qx 'status 401' <<<"$refusal" &&
        grep -qiE "^www-authenticate: bearer .*scope=\"repository:${repository}:" <<<"$refusal"
}

rt_access() {
    printf '[{"type":"repository","name":"%s","actions":[%s]}]' "$1" "$2"
}

rt_application_created() {
    rk -n "$E2E_LOCO_NAMESPACE" get application "resource-$(rt_resource_id "$1")" -o jsonpath='{.metadata.creationTimestamp}'
}

rt_secret_created() {
    rk -n "$E2E_LOCO_NAMESPACE" get secret "$(rt_pull_secret "$1")" -o jsonpath='{.metadata.creationTimestamp}'
}

rt_pods_running() {
    [[ $(rk -n "$(rt_namespace "$1")" get pods -o jsonpath='{.items[*].status.phase}') == *Running* ]]
}

rt_pod() {
    local namespace=$1 name=$2 image=$3
    rk -n "$namespace" delete pod "$name" --ignore-not-found --wait >/dev/null
    rk apply -f - >/dev/null <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: $name
  namespace: $namespace
spec:
  automountServiceAccountToken: false
  terminationGracePeriodSeconds: 0
  imagePullSecrets:
    - name: loco-registry-pull
  securityContext:
    runAsNonRoot: true
    runAsUser: 65534
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: app
      image: $image
      imagePullPolicy: Always
      resources:
        requests: { cpu: 10m, memory: 32Mi }
        limits: { cpu: 100m, memory: 64Mi }
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: ["ALL"]
YAML
}

rt_pod_waiting_reason() {
    rk -n "$1" get pod "$2" -o jsonpath='{.status.containerStatuses[0].state.waiting.reason}'
}

rt_pod_pull_failed() {
    case "$(rt_pod_waiting_reason "$1" "$2")" in
        ErrImagePull) return 0 ;;
        ImagePullBackOff) return 0 ;;
    esac
    return 1
}

rt_pod_pull_error() {
    rk -n "$1" get events --field-selector "involvedObject.name=$2,reason=Failed" -o jsonpath='{.items[*].message}'
}

rt_pod_started() {
    test -n "$(rk -n "$1" get pod "$2" -o jsonpath='{.status.containerStatuses[0].state.running.startedAt}')"
}

rt_api() {
    curl -sS --fail-with-body -X POST "${E2E_API_URL}/$1" \
        -H "Authorization: Bearer ${E2E_USER_TOKEN}" \
        -H 'Content-Type: application/json' \
        -d "$2"
}

rt_start_build() {
    local app=$1 label=$2 resource_id src tarball size created build_id upload_url
    resource_id=$(rt_resource_id "$app")
    src="$rt_dir/$label-src"
    tarball="$rt_dir/$label.tar.gz"
    rt_write_source "$src" "$label"
    COPYFILE_DISABLE=1 tar -czf "$tarball" -C "$src" .
    size=$(wc -c <"$tarball" | tr -d ' ')
    created=$(rt_api loco.build.v1.BuildService/CreateBuild \
        "{\"resourceId\":\"${resource_id}\",\"dockerfilePath\":\"deploy/Dockerfile\",\"sourceSize\":\"${size}\"}") || return 1
    build_id=$(yq -p json -r '.buildId' <<<"$created")
    upload_url=$(yq -p json -r '.uploadUrl' <<<"$created")
    curl -sS --fail-with-body -X PUT -T "$tarball" "$upload_url" >/dev/null || return 1
    rt_api loco.build.v1.BuildService/StartBuild "{\"buildId\":\"${build_id}\"}" >/dev/null || return 1
    echo "$build_id"
}

rt_build_secret_exists() {
    rk -n "$rt_builds_ns" get secret "build-$1-registry" >/dev/null 2>&1
}

rt_build_password_changed() {
    local current
    current=$(rt_build_password "$1") && [ -n "$current" ] && [ "$current" != "$2" ]
}

rt_build_finished() {
    case "$(rt_build "$1" status)" in
        succeeded) return 0 ;;
        failed) return 0 ;;
        canceled) return 0 ;;
    esac
    return 1
}

rt_controller_pods_gone() {
    test -z "$(rk -n "$E2E_LOCO_NAMESPACE" get pods -l "app.kubernetes.io/name=${rt_build_controller}" -o name)"
}

rt_pull_password_changed() {
    local current
    current=$(rt_pull_password "$1") && [ -n "$current" ] && [ "$current" != "$2" ]
}

rt_workspace_copy_is() {
    test "$(registry_secret_password "$(rt_namespace "$1")" loco-registry-pull)" = "$2"
}

rt_realm_refuses() {
    test "$(registry_realm_status "$@")" = 401
}

rt_pull_secret_gone() {
    ! rk -n "$E2E_LOCO_NAMESPACE" get secret "$(rt_pull_secret "$1")" >/dev/null 2>&1
}

rt_manifest_gone() {
    test "$(rt_manifest_status "$1" "$2")" = 404
}

test_r01_first_deploys_in_new_workspaces() {
    cli_seed_session
    rt_seed_workspace "$rt_ws_a" "$rt_env_a" e2e-registry-a
    rt_seed_workspace "$rt_ws_b" "$rt_env_b" e2e-registry-b

    local workspace app label
    for label in a b; do
        if [ "$label" = a ]; then
            workspace=$rt_ws_a app=$rt_app_a
        else
            workspace=$rt_ws_b app=$rt_app_b
        fi
        if ! assert "loco deploy built and deployed ${app} in brand-new workspace ${workspace}" \
            rt_deploy "$workspace" "$app" "rt-${label}-1"; then
            return 1
        fi
        rt_save "build-${label}-1" "$(rt_latest_build "$app")"
    done
    for label in a b; do
        if [ "$label" = a ]; then
            workspace=$rt_ws_a app=$rt_app_a
        else
            workspace=$rt_ws_b app=$rt_app_b
        fi
        wait_for "${app} to be Ready" "$rt_deploy_timeout" cli_application_is_ready "$app"
        if ! assert "${app}'s pods pulled the built image and run" rt_pods_running "$workspace"; then
            rk -n "$(rt_namespace "$workspace")" describe pods | sed 's/^/    /'
        fi
        assert "build of ${app} succeeded" test "$(rt_build "$(rt_load "build-${label}-1")" status)" = succeeded
        assert "the agent wrote ${app}'s pull Secret before its Application ($(rt_secret_created "$workspace") <= $(rt_application_created "$app"))" \
            test ! "$(rt_secret_created "$workspace")" \> "$(rt_application_created "$app")"
    done
}

test_r02_no_shared_secrets() {
    assert_fails "no shared pull Secret loco-registry in ${E2E_LOCO_NAMESPACE}" \
        rk -n "$E2E_LOCO_NAMESPACE" get secret loco-registry
    assert_fails "no shared push Secret loco-registry-push in ${E2E_LOCO_NAMESPACE}" \
        rk -n "$E2E_LOCO_NAMESPACE" get secret loco-registry-push
    assert_fails "no copied registry-push Secret in ${rt_builds_ns}" rk -n "$rt_builds_ns" get secret registry-push
    assert_fails "no copied registry-pull Secret in ${rt_builds_ns}" rk -n "$rt_builds_ns" get secret registry-pull
    local workspace
    for workspace in "$rt_ws_a" "$rt_ws_b"; do
        assert_contains "workspace ${workspace} has its own loco-pull credential" "$rt_pull_user" \
            bash -c "kubectl --context $rt_ctx -n $(rt_namespace "$workspace") get secret loco-registry-pull \
                -o jsonpath='{.data.\\.dockerconfigjson}' | base64 -d"
        assert "workspace ${workspace}'s copy holds the agent's credential" \
            rt_workspace_copy_is "$workspace" "$(rt_pull_password "$workspace")"
    done
}

test_r03_pull_credential_is_scoped_to_its_workspace() {
    local build_a build_b repo_a repo_b resource_a resource_b password token access
    build_a=$(rt_load build-a-1)
    build_b=$(rt_load build-b-1)
    repo_a=$(rt_repository "$build_a")
    repo_b=$(rt_repository "$build_b")
    resource_a=${repo_a##*/}
    resource_b=${repo_b##*/}
    password=$(rt_pull_password "$rt_ws_a")

    token=$(registry_realm_token "$rt_pull_user" "$password" "repository:${repo_a}:pull")
    assert "A's pull credential gets pull on A's repository" \
        test "$(registry_token_access "$token")" = "$(rt_access "$repo_a" '"pull"')"
    assert "the registry serves A's image to that token" test "$(rt_manifest_status "$rt_ws_a" "$build_a")" = 200

    local scope
    for scope in \
        "${repo_b}" \
        "ws-${rt_ws_a}x/${resource_a}" \
        "ws-${rt_ws_a}/../ws-${rt_ws_b}/${resource_b}" \
        "other/ws-${rt_ws_a}/${resource_a}"; do
        token=$(registry_realm_token "$rt_pull_user" "$password" "repository:${scope}:pull")
        access=$(registry_token_access "$token")
        assert "A's pull credential gets no access to ${scope} (got ${access})" test "$access" = "[]"
    done
    token=$(registry_realm_token "$rt_pull_user" "$password" "repository:${repo_b}:pull")
    assert "the registry refuses B's manifest to A's token and asks for a token scoped to it" \
        rt_refused_for "$repo_b" "$token" GET "/v2/${repo_b}/manifests/$(rt_build "$build_b" image_digest)"

    token=$(registry_realm_token "$rt_pull_user" "$password" "repository:${repo_a}:pull,push")
    access=$(registry_token_access "$token")
    assert "A's pull credential asking for push gets pull only (got ${access})" \
        test "$access" = "$(rt_access "$repo_a" '"pull"')"
    assert "the registry refuses a push with that token and asks for a token scoped to it" \
        rt_refused_for "$repo_a" "$token" POST "/v2/${repo_a}/blobs/uploads/"

    assert "a registry access token used as a loco-pull password gets 401" \
        rt_realm_refuses "$rt_pull_user" "$token" "repository:${repo_a}:pull"
    assert "a registry access token used as a loco-build password gets 401" \
        rt_realm_refuses "$rt_build_user" "$token" "repository:${repo_a}:pull"
    assert "A's pull credential presented as loco-build gets 401" \
        rt_realm_refuses "$rt_build_user" "$password" "repository:${repo_a}:pull"
}

test_r04_pod_cannot_pull_another_workspace_image() {
    local namespace name=rt-cross-pull message
    namespace=$(rt_namespace "$rt_ws_a")
    rt_pod "$namespace" "$name" "$(rt_image "$(rt_load build-b-1)")"
    wait_for "the pull of B's image in A's namespace to fail" "$rt_pull_timeout" rt_pod_pull_failed "$namespace" "$name"
    assert "a pod in A whose image is in B's repository gets ErrImagePull" rt_pod_pull_failed "$namespace" "$name"
    message=$(rt_pod_pull_error "$namespace" "$name")
    log_info "Pull error: ${message}"
    assert "the registry refused the pull with 401 Unauthorized" \
        grep -qE "unexpected status from HEAD request to [^ ]*/manifests/[^ ]*: 401 Unauthorized" <<<"$message"
    rk -n "$namespace" delete pod "$name" --wait=false >/dev/null
}

test_r05_second_deploy_imports_the_cache() {
    if ! assert "second loco deploy of ${rt_app_a} succeeded" rt_deploy "$rt_ws_a" "$rt_app_a" rt-a-2; then
        return 1
    fi
    local build
    build=$(rt_latest_build "$rt_app_a")
    rt_save build-a-2 "$build"
    assert "the second build is a new build" test "$build" != "$(rt_load build-a-1)"
    assert_contains "the second build's fetch step imported the cache with its build credential" "cache restored" \
        rk -n "$rt_builds_ns" logs "job/build-${build}" -c fetch
    wait_for "${rt_app_a} to roll out the second build" "$rt_deploy_timeout" cli_application_is_ready "$rt_app_a"
}

test_r06_sweeper_deletes_the_replaced_image() {
    local first
    first=$(rt_load build-a-1)
    wait_for "the image sweeper to delete the replaced image" 90 rt_manifest_gone "$rt_ws_a" "$first"
    assert "the replaced build's image manifest is gone (404)" rt_manifest_gone "$rt_ws_a" "$first"
    assert "the API recorded the image deletion" test "$(rt_build "$first" "image_deleted_at IS NOT NULL")" = t
    assert "the deployed build's image is still served" \
        test "$(rt_manifest_status "$rt_ws_a" "$(rt_load build-a-2)")" = 200
}

test_r07_resent_build_gets_a_new_credential() {
    rk -n "$E2E_LOCO_NAMESPACE" scale deployment "$rt_build_controller" --replicas=0 >/dev/null
    wait_for "the build controller to stop" 60 rt_controller_pods_gone
    local build first second
    if ! build=$(rt_start_build "$rt_app_a" rt-a-resend); then
        log_error "Starting a build through the API failed"
        return 1
    fi
    rt_save build-a-resend "$build"
    wait_for "the agent to write the build's registry Secret" 60 rt_build_secret_exists "$build"
    first=$(rt_build_password "$build")
    rt_save build-a-resend.first "$first"
    assert "the build Secret holds a loco-build credential" test -n "$first"

    kill_pid_file "$PID_DIR/agent.pid"
    E2E_JOIN_TOKEN="" start_agent
    wait_for "the resent StartBuild to replace the build Secret's password" 60 rt_build_password_changed "$build" "$first"
    second=$(rt_build_password "$build")
    rt_save build-a-resend.second "$second"
    assert "the resend replaced the build Secret's password" test -n "$second" -a "$second" != "$first"
    assert "the build is still queued" test "$(rt_build "$build" status)" = queued
}

test_r08_build_credential_is_scoped_to_its_repository() {
    local build password repo_a repo_b layer token access status
    build=$(rt_load build-a-resend)
    password=$(rt_load build-a-resend.second)
    repo_a=$(rt_repository "$build")
    repo_b=$(rt_repository "$(rt_load build-b-1)")

    token=$(registry_realm_token "$rt_build_user" "$password" "repository:${repo_a}:pull,push")
    assert "the queued build's credential gets pull and push on its repository" \
        test "$(registry_token_access "$token")" = "$(rt_access "$repo_a" '"pull","push"')"

    token=$(registry_realm_token "$rt_build_user" "$password" "repository:${repo_b}:pull,push")
    access=$(registry_token_access "$token")
    assert "the build credential gets no access to B's repository (got ${access})" test "$access" = "[]"
    assert "the registry refuses a push into B with that token and asks for a token scoped to it" \
        rt_refused_for "$repo_b" "$token" POST "/v2/${repo_b}/blobs/uploads/"

    layer=$(curl -s -H "Authorization: Bearer $(rt_pull_token "$rt_ws_b" "$repo_b")" \
        -H 'Accept: application/vnd.oci.image.manifest.v1+json' \
        "${E2E_REGISTRY_URL}/v2/${repo_b}/manifests/$(rt_build "$(rt_load build-b-1)" image_digest)" |
        yq -p json -r '.layers[-1].digest')
    token=$(registry_realm_token "$rt_build_user" "$password" \
        "repository:${repo_a}:pull,push" "repository:${repo_b}:pull")
    access=$(registry_token_access "$token")
    assert "a mount request's token covers only the build's repository (got ${access})" \
        test "$access" = "$(rt_access "$repo_a" '"pull","push"')"
    assert "B's top layer ${layer} is not in A's repository" \
        test "$(registry_status_with_token "$token" GET "/v2/${repo_a}/blobs/${layer}")" = 404
    status=$(registry_status_with_token "$token" POST "/v2/${repo_a}/blobs/uploads/?mount=${layer}&from=${repo_b}")
    assert "the registry does not mount B's blob into A (status ${status})" test "$status" != 201
    assert "B's layer is still absent from A's repository" \
        test "$(registry_status_with_token "$token" GET "/v2/${repo_a}/blobs/${layer}")" = 404
}

test_r09_resent_build_succeeds() {
    local build
    build=$(rt_load build-a-resend)
    rk -n "$E2E_LOCO_NAMESPACE" scale deployment "$rt_build_controller" --replicas=1 >/dev/null
    wait_for "the resent build to finish" 600 rt_build_finished "$build"
    if ! assert "the resent build succeeded with the replaced credential" test "$(rt_build "$build" status)" = succeeded; then
        for container in fetch build push; do
            rk -n "$rt_builds_ns" logs "job/build-${build}" -c "$container" 2>&1 | tail -40 | sed 's/^/    /'
        done
    fi
    assert_contains "fetch and push mounted the build's own Secret" "build-${build}-registry" \
        rk -n "$rt_builds_ns" get job "build-${build}" -o jsonpath='{.spec.template.spec.volumes[*].secret.secretName}'
}

test_r10_finished_build_credential_is_refused() {
    local build repository
    build=$(rt_load build-a-resend)
    repository=$(rt_repository "$build")
    assert "the finished build's first credential gets 401" \
        rt_realm_refuses "$rt_build_user" "$(rt_load build-a-resend.first)" "repository:${repository}:pull"
    assert "the finished build's resent credential gets 401" \
        rt_realm_refuses "$rt_build_user" "$(rt_load build-a-resend.second)" "repository:${repository}:pull"
}

test_r11_renewal_replaces_the_pull_secret() {
    local before before_expires after issued repository image name=rt-renewed-pull namespace
    before=$(rt_pull_password "$rt_ws_b")
    issued=$(rt_annotation "$rt_ws_b" "$rt_issued_at")
    before_expires=$(rt_annotation "$rt_ws_b" "$rt_expires_at")
    wait_for "the agent to renew B's pull credential (issued ${issued})" "$rt_renewal_timeout" \
        rt_pull_password_changed "$rt_ws_b" "$before"
    after=$(rt_pull_password "$rt_ws_b")
    assert "the agent replaced B's pull credential" test -n "$after" -a "$after" != "$before"
    assert "the renewed issue time is later (${issued} -> $(rt_annotation "$rt_ws_b" "$rt_issued_at"))" \
        test "$(rt_annotation "$rt_ws_b" "$rt_issued_at")" \> "$issued"
    repository=$(rt_repository "$(rt_load build-b-1)")
    assert "the renewed credential works at the token endpoint" \
        test "$(registry_realm_status "$rt_pull_user" "$after" "repository:${repository}:pull")" = 200
    local expected=401
    if [ "$(rt_epoch "$before_expires")" -gt "$(date +%s)" ]; then
        expected=200
    fi
    assert "the previous credential, expiring ${before_expires}, gets ${expected}" \
        test "$(registry_realm_status "$rt_pull_user" "$before" "repository:${repository}:pull")" = "$expected"
    wait_for "the controller to copy the renewed credential into B's namespace" 60 rt_workspace_copy_is "$rt_ws_b" "$after"
    assert "B's namespace holds the renewed credential" rt_workspace_copy_is "$rt_ws_b" "$after"

    namespace=$(rt_namespace "$rt_ws_b")
    image=$(rt_image "$(rt_load build-b-1)")
    rt_pod "$namespace" "$name" "$image"
    wait_for "a pod in B to pull its image with the renewed credential" "$rt_pull_timeout" rt_pod_started "$namespace" "$name"
    if ! assert "a pod pulled B's image after the renewal" rt_pod_started "$namespace" "$name"; then
        rk -n "$namespace" describe pod "$name" | sed 's/^/    /'
    fi
    rk -n "$namespace" delete pod "$name" --wait=false >/dev/null
}

test_r12_removed_workspace_loses_its_credential() {
    local password expires repository resource_id
    repository=$(rt_repository "$(rt_load build-a-2)")
    password=$(rt_pull_password "$rt_ws_a")
    expires=$(rt_annotation "$rt_ws_a" "$rt_expires_at")
    assert "A's current pull credential works before the removal" \
        test "$(registry_realm_status "$rt_pull_user" "$password" "repository:${repository}:pull")" = 200
    resource_id=$(rt_resource_id "$rt_app_a")
    assert "the API deleted ${rt_app_a}" \
        rt_api loco.resource.v1.ResourceService/DeleteResource "{\"resourceId\":\"${resource_id}\"}"
    wait_for "A's credential to stop working at the token endpoint" 90 \
        rt_realm_refuses "$rt_pull_user" "$password" "repository:${repository}:pull"
    assert "A's credential gets 401 after the removal" \
        rt_realm_refuses "$rt_pull_user" "$password" "repository:${repository}:pull"
    assert "the credential was refused before it expired (expires ${expires})" \
        test "$(rt_epoch "$expires")" -gt "$(date +%s)"
    wait_for "the agent to delete A's pull Secret" 90 rt_pull_secret_gone "$rt_ws_a"
    assert "the agent deleted $(rt_pull_secret "$rt_ws_a")" rt_pull_secret_gone "$rt_ws_a"
    assert "B's pull Secret remains" rk -n "$E2E_LOCO_NAMESPACE" get secret "$(rt_pull_secret "$rt_ws_b")"
}
