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
rt_manifest_type="application/vnd.oci.image.manifest.v1+json"
rt_digest_pattern='^sha256:[a-f0-9]{64}$'
rt_deploy_timeout=180
rt_pull_timeout=120
rt_renewal_timeout=150
rt_sweep_timeout=90
rt_secret_timeout=60
rt_revocation_timeout=90
rt_build_timeout=600
rt_controller_timeout=60
rt_expiry_margin=10

rk() {
    kubectl --context "$rt_ctx" "$@"
}

rt_fail() {
    log_error "FAIL: $1"
    E2E_FAIL=$((E2E_FAIL + 1))
    return 1
}

rt_wait() {
    local desc=$1
    wait_for "$@" || rt_fail "$desc"
}

rt_present() {
    [ -n "$1" ] && [ "$1" != null ]
}

rt_save() {
    rt_present "$2" || return 1
    mkdir -p "$rt_dir"
    printf '%s' "$2" >"$rt_dir/$1"
}

rt_load() {
    local value
    value=$(cat "$rt_dir/$1" 2>/dev/null) && rt_present "$value" && echo "$value"
}

rt_query() {
    local value
    value=$(e2e_psql "$1") && rt_present "$value" && echo "$value"
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
    rt_query "SELECT id FROM resources WHERE name = '$1'"
}

rt_latest_build() {
    rt_query "SELECT b.id FROM builds b JOIN resources r ON r.id = b.resource_id
              WHERE r.name = '$1' ORDER BY b.created_at DESC LIMIT 1"
}

rt_build() {
    rt_present "$1" || return 1
    rt_query "SELECT $2 FROM builds WHERE id = '$1'"
}

rt_repository() {
    local repository
    repository=$(rt_build "$1" image_repository) || return 1
    [[ $repository == "${E2E_REGISTRY_HOST}/"?* ]] || return 1
    echo "${repository#"${E2E_REGISTRY_HOST}/"}"
}

rt_digest() {
    local digest
    digest=$(rt_build "$1" image_digest) || return 1
    [[ $digest =~ $rt_digest_pattern ]] && echo "$digest"
}

rt_image() {
    local repository digest
    repository=$(rt_build "$1" image_repository) && digest=$(rt_digest "$1") && echo "${repository}@${digest}"
}

rt_pull_password() {
    registry_secret_password "$E2E_LOCO_NAMESPACE" "$(rt_pull_secret "$1")"
}

rt_build_password() {
    registry_secret_password "$rt_builds_ns" "build-$1-registry"
}

rt_annotation() {
    local workspace=$1 key=${2//./\\.} value
    value=$(rk -n "$E2E_LOCO_NAMESPACE" get secret "$(rt_pull_secret "$workspace")" \
        -o jsonpath="{.metadata.annotations.${key}}") && rt_present "$value" && echo "$value"
}

rt_epoch() {
    local stamp=${1%Z}
    rt_present "$stamp" || return 1
    stamp=${stamp%%.*}
    date -u -j -f '%Y-%m-%dT%H:%M:%S' "$stamp" +%s 2>/dev/null || date -u -d "${stamp}Z" +%s
}

rt_seconds_left() {
    local expires
    expires=$(rt_epoch "$1") || return 1
    echo $((expires - $(date +%s)))
}

rt_pull_token() {
    local password
    password=$(rt_pull_password "$1") && registry_realm_token "$rt_pull_user" "$password" "repository:$2:pull"
}

rt_manifest_status() {
    local workspace=$1 build=$2 repository digest token
    repository=$(rt_repository "$build") && digest=$(rt_digest "$build") &&
        token=$(rt_pull_token "$workspace" "$repository") || return 1
    registry_status_with_token "$token" GET "/v2/${repository}/manifests/${digest}"
}

rt_manifest_gone() {
    test "$(rt_manifest_status "$1" "$2")" = 404
}

rt_challenge() {
    printf 'www-authenticate: Bearer realm="%s",service="%s",scope="repository:%s:%s"' \
        "$E2E_REGISTRY_REALM" "$E2E_REGISTRY_TOKEN_SERVICE" "$1" "$2"
}

rt_refused() {
    local repository=$1 action=$2 refusal
    shift 2
    refusal=$(registry_refusal_with_token "$@") || return 1
    test "$refusal" = "$(rt_challenge "$repository" "$action")"$'\n'"status 401"
}

rt_basic_refused() {
    local account=$1 repository=$2 action=$3 method=$4 path=$5 refusal
    refusal=$(curl -s -o /dev/null -D - -w 'status %{http_code}\n' -u "$account" -X "$method" \
        -H "Accept: ${rt_manifest_type}" "${E2E_REGISTRY_URL}${path}" |
        registry_refusal_filter)
    test "$refusal" = "$(rt_challenge "$repository" "$action")"$'\n'"status 401"
}

rt_access() {
    printf '[{"type":"repository","name":"%s","actions":[%s]}]' "$1" "$2"
}

rt_token_grants() {
    local access
    access=$(registry_token_access "$1") && test "$access" = "$2"
}

rt_realm_refuses() {
    test "$(registry_realm_status "$@")" = 401
}

rt_realm_accepts() {
    test "$(registry_realm_status "$@")" = 200
}

rt_absent() {
    local namespace=$1 kind=$2 name=$3 found
    found=$(rk -n "$namespace" get "$kind" "$name" --ignore-not-found -o name) && [ -z "$found" ]
}

rt_agent_log_line() {
    grep -n -F -m 1 "$1" "$E2E_LOG_DIR/agent.log" | cut -d : -f 1
}

rt_secret_written_before_application() {
    local workspace=$1 app=$2 resource_id secret_line app_line
    resource_id=$(rt_resource_id "$app") || return 1
    secret_line=$(rt_agent_log_line "\"msg\":\"wrote registry pull Secret\",\"name\":\"$(rt_pull_secret "$workspace")\"")
    app_line=$(rt_agent_log_line "\"msg\":\"applied Application\",\"name\":\"resource-${resource_id}\"")
    rt_present "$secret_line" && rt_present "$app_line" && [ "$secret_line" -lt "$app_line" ]
}

rt_pods_running() {
    local phases
    phases=$(rk -n "$(rt_namespace "$1")" get pods -o jsonpath='{.items[*].status.phase}') || return 1
    [[ " ${phases} " == *" Running "* ]]
}

rt_dns_name() {
    [[ $1 =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ ]] && [[ $1 =~ [a-z] ]]
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

rt_pod_pull_failed() {
    case "$(rk -n "$1" get pod "$2" -o jsonpath='{.status.containerStatuses[0].state.waiting.reason}')" in
        ErrImagePull) return 0 ;;
        ImagePullBackOff) return 0 ;;
    esac
    return 1
}

rt_pod_pull_error() {
    rk -n "$1" get events --field-selector "involvedObject.name=$2,reason=Failed" -o jsonpath='{.items[*].message}'
}

rt_pod_started() {
    local started
    started=$(rk -n "$1" get pod "$2" -o jsonpath='{.status.containerStatuses[0].state.running.startedAt}') &&
        rt_present "$started"
}

rt_api() {
    curl -sS --fail-with-body -X POST "${E2E_API_URL}/$1" \
        -H "Authorization: Bearer ${E2E_USER_TOKEN}" \
        -H 'Content-Type: application/json' \
        -d "$2"
}

rt_start_build() {
    local app=$1 label=$2 resource_id src tarball size created build_id upload_url
    resource_id=$(rt_resource_id "$app") || return 1
    src="$rt_dir/$label-src"
    tarball="$rt_dir/$label.tar.gz"
    rt_write_source "$src" "$label"
    COPYFILE_DISABLE=1 tar -czf "$tarball" -C "$src" .
    size=$(wc -c <"$tarball" | tr -d ' ')
    created=$(rt_api loco.build.v1.BuildService/CreateBuild \
        "{\"resourceId\":\"${resource_id}\",\"dockerfilePath\":\"deploy/Dockerfile\",\"sourceSize\":\"${size}\"}") || return 1
    build_id=$(yq -p json -e -r '.buildId' <<<"$created") || return 1
    upload_url=$(yq -p json -e -r '.uploadUrl' <<<"$created") || return 1
    curl -sS --fail-with-body -X PUT -T "$tarball" "$upload_url" >/dev/null || return 1
    rt_api loco.build.v1.BuildService/StartBuild "{\"buildId\":\"${build_id}\"}" >/dev/null || return 1
    echo "$build_id"
}

rt_build_secret_exists() {
    rt_build_password "$1" >/dev/null
}

rt_build_password_changed() {
    local current
    current=$(rt_build_password "$1") && [ "$current" != "$2" ]
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
    local pods
    pods=$(rk -n "$E2E_LOCO_NAMESPACE" get pods -l "app.kubernetes.io/name=${rt_build_controller}" -o name) &&
        [ -z "$pods" ]
}

rt_pull_password_changed() {
    local current
    current=$(rt_pull_password "$1") && [ "$current" != "$2" ]
}

rt_workspace_copy_is() {
    local copy
    rt_present "$2" || return 1
    copy=$(registry_secret_password "$(rt_namespace "$1")" loco-registry-pull) && [ "$copy" = "$2" ]
}

rt_pull_secret_gone() {
    rt_absent "$E2E_LOCO_NAMESPACE" secret "$(rt_pull_secret "$1")"
}

test_r00_registry_challenges_with_the_named_realm() {
    local refusal
    refusal=$(curl -s -o /dev/null -D - -w 'status %{http_code}\n' "${E2E_REGISTRY_URL}/v2/" |
        registry_refusal_filter)
    assert "the registry sends anonymous clients to ${E2E_REGISTRY_REALM} for ${E2E_REGISTRY_TOKEN_SERVICE}" \
        test "$refusal" = "$(printf 'www-authenticate: Bearer realm="%s",service="%s",scope=""\nstatus 401' \
            "$E2E_REGISTRY_REALM" "$E2E_REGISTRY_TOKEN_SERVICE")"
    local realm_host=${E2E_REGISTRY_REALM#*://}
    realm_host=${realm_host%%[:/]*}
    assert "the realm host ${realm_host} is a DNS name, not an IP literal" rt_dns_name "$realm_host"
}

test_r01_first_deploys_in_new_workspaces() {
    rt_seed_workspace "$rt_ws_a" "$rt_env_a" e2e-registry-a
    rt_seed_workspace "$rt_ws_b" "$rt_env_b" e2e-registry-b

    local workspace app label build
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
        build=$(rt_latest_build "$app") && rt_save "build-${label}-1" "$build" ||
            { rt_fail "find ${app}'s build"; return 1; }
    done
    for label in a b; do
        if [ "$label" = a ]; then
            workspace=$rt_ws_a app=$rt_app_a
        else
            workspace=$rt_ws_b app=$rt_app_b
        fi
        rt_wait "${app} to be Ready" "$rt_deploy_timeout" cli_application_is_ready "$app"
        if ! assert "${app}'s pods pulled the built image and run" rt_pods_running "$workspace"; then
            rk -n "$(rt_namespace "$workspace")" describe pods | sed 's/^/    /'
        fi
        assert "build of ${app} succeeded" test "$(rt_build "$(rt_load "build-${label}-1")" status)" = succeeded
        assert "the agent wrote ${app}'s pull Secret before it applied the workspace's first Application" \
            rt_secret_written_before_application "$workspace" "$app"
    done
}

test_r02_no_shared_secrets() {
    assert "no shared pull Secret loco-registry in ${E2E_LOCO_NAMESPACE}" \
        rt_absent "$E2E_LOCO_NAMESPACE" secret loco-registry
    assert "no shared push Secret loco-registry-push in ${E2E_LOCO_NAMESPACE}" \
        rt_absent "$E2E_LOCO_NAMESPACE" secret loco-registry-push
    assert "no copied registry-push Secret in ${rt_builds_ns}" rt_absent "$rt_builds_ns" secret registry-push
    assert "no copied registry-pull Secret in ${rt_builds_ns}" rt_absent "$rt_builds_ns" secret registry-pull
    local workspace password
    for workspace in "$rt_ws_a" "$rt_ws_b"; do
        assert "workspace ${workspace}'s copy is a ${rt_pull_user} credential" \
            test "$(registry_secret_field "$(rt_namespace "$workspace")" loco-registry-pull username)" = "$rt_pull_user"
        password=$(rt_pull_password "$workspace") || { rt_fail "read workspace ${workspace}'s pull credential"; continue; }
        assert "workspace ${workspace}'s copy holds the agent's credential" rt_workspace_copy_is "$workspace" "$password"
    done
}

test_r03_pull_credential_is_scoped_to_its_workspace() {
    local build_a build_b repo_a repo_b digest_a digest_b resource_a resource_b password token
    build_a=$(rt_load build-a-1) && build_b=$(rt_load build-b-1) &&
        repo_a=$(rt_repository "$build_a") && repo_b=$(rt_repository "$build_b") &&
        digest_a=$(rt_digest "$build_a") && digest_b=$(rt_digest "$build_b") &&
        password=$(rt_pull_password "$rt_ws_a") || { rt_fail "read the first builds and A's pull credential"; return 1; }
    resource_a=${repo_a##*/}
    resource_b=${repo_b##*/}

    token=$(registry_realm_token "$rt_pull_user" "$password" "repository:${repo_a}:pull") ||
        { rt_fail "exchange A's pull credential for A's repository"; return 1; }
    assert "A's pull credential gets pull on A's repository" rt_token_grants "$token" "$(rt_access "$repo_a" '"pull"')"
    assert "the registry serves A's image to that token" \
        test "$(registry_status_with_token "$token" GET "/v2/${repo_a}/manifests/${digest_a}")" = 200

    local scope
    for scope in \
        "${repo_b}" \
        "ws-${rt_ws_a}x/${resource_a}" \
        "ws-${rt_ws_a}/../ws-${rt_ws_b}/${resource_b}" \
        "other/ws-${rt_ws_a}/${resource_a}"; do
        token=$(registry_realm_token "$rt_pull_user" "$password" "repository:${scope}:pull") ||
            { rt_fail "exchange A's pull credential for ${scope}"; continue; }
        assert "A's pull credential gets no access to ${scope}" rt_token_grants "$token" "[]"
    done
    token=$(registry_realm_token "$rt_pull_user" "$password" "repository:${repo_b}:pull") ||
        { rt_fail "exchange A's pull credential for B's repository"; return 1; }
    assert "the registry answers B's manifest to A's token with 401 and a challenge for pull on B" \
        rt_refused "$repo_b" pull "$token" GET "/v2/${repo_b}/manifests/${digest_b}"

    token=$(registry_realm_token "$rt_pull_user" "$password" "repository:${repo_a}:pull,push") ||
        { rt_fail "exchange A's pull credential for push"; return 1; }
    assert "A's pull credential asking for push gets pull only" \
        rt_token_grants "$token" "$(rt_access "$repo_a" '"pull"')"
    assert "the registry answers a push with that token with 401 and a challenge for push on A" \
        rt_refused "$repo_a" push "$token" POST "/v2/${repo_a}/blobs/uploads/"

    assert "a registry access token used as a loco-pull password gets 401" \
        rt_realm_refuses "$rt_pull_user" "$token" "repository:${repo_a}:pull"
    assert "a registry access token used as a loco-build password gets 401" \
        rt_realm_refuses "$rt_build_user" "$token" "repository:${repo_a}:pull"
    assert "A's pull credential presented as loco-build gets 401" \
        rt_realm_refuses "$rt_build_user" "$password" "repository:${repo_a}:pull"
}

test_r04_old_shared_passwords_are_refused() {
    local build repository digest account
    build=$(rt_load build-a-1) && repository=$(rt_repository "$build") && digest=$(rt_digest "$build") ||
        { rt_fail "read A's first build"; return 1; }
    for account in "$E2E_REGISTRY_NODES_USER" "$E2E_REGISTRY_BUILDER_USER" "$E2E_REGISTRY_API_USER"; do
        assert "the static ${account%%:*} password gets 401 and a Bearer challenge for a pull" \
            rt_basic_refused "$account" "$repository" pull GET "/v2/${repository}/manifests/${digest}"
        assert "the static ${account%%:*} password gets 401 and a Bearer challenge for a push" \
            rt_basic_refused "$account" "$repository" push POST "/v2/${repository}/blobs/uploads/"
    done
}

test_r05_pod_cannot_pull_another_workspace_image() {
    local namespace name=rt-cross-pull image message
    namespace=$(rt_namespace "$rt_ws_a")
    image=$(rt_image "$(rt_load build-b-1)") || { rt_fail "read B's image"; return 1; }
    rt_pod "$namespace" "$name" "$image"
    rt_wait "the pull of B's image in A's namespace to fail" "$rt_pull_timeout" rt_pod_pull_failed "$namespace" "$name"
    assert "a pod in A whose image is in B's repository gets ErrImagePull" rt_pod_pull_failed "$namespace" "$name"
    message=$(rt_pod_pull_error "$namespace" "$name")
    log_info "Pull error: ${message}"
    assert "the registry refused the pull with 401 Unauthorized" \
        grep -qE "unexpected status from HEAD request to [^ ]*/manifests/[^ ]*: 401 Unauthorized" <<<"$message"
    rk -n "$namespace" delete pod "$name" --wait=false >/dev/null
}

test_r06_second_deploy_imports_the_cache() {
    if ! assert "second loco deploy of ${rt_app_a} succeeded" rt_deploy "$rt_ws_a" "$rt_app_a" rt-a-2; then
        return 1
    fi
    local first build
    first=$(rt_load build-a-1) && build=$(rt_latest_build "$rt_app_a") && rt_save build-a-2 "$build" ||
        { rt_fail "find the second build"; return 1; }
    assert "the second build is a new build" test "$build" != "$first"
    assert_contains "the second build's fetch step imported the cache with its build credential" "cache restored" \
        rk -n "$rt_builds_ns" logs "job/build-${build}" -c fetch
    rt_wait "${rt_app_a} to roll out the second build" "$rt_deploy_timeout" cli_application_is_ready "$rt_app_a"
}

test_r07_sweeper_deletes_the_replaced_image() {
    local first second
    first=$(rt_load build-a-1) && second=$(rt_load build-a-2) || { rt_fail "read A's builds"; return 1; }
    rt_wait "the image sweeper to delete the replaced image" "$rt_sweep_timeout" rt_manifest_gone "$rt_ws_a" "$first"
    assert "the replaced build's image manifest is gone (404)" rt_manifest_gone "$rt_ws_a" "$first"
    assert "the API recorded the image deletion" test "$(rt_build "$first" "image_deleted_at IS NOT NULL")" = t
    assert "the deployed build's image is still served" test "$(rt_manifest_status "$rt_ws_a" "$second")" = 200
}

test_r08_resent_build_gets_a_new_credential() {
    rk -n "$E2E_LOCO_NAMESPACE" scale deployment "$rt_build_controller" --replicas=0 >/dev/null
    rt_wait "the build controller to stop" "$rt_controller_timeout" rt_controller_pods_gone || return 1
    local build first second
    build=$(rt_start_build "$rt_app_a" rt-a-resend) && rt_save build-a-resend "$build" ||
        { rt_fail "start a build through the API"; return 1; }
    rt_wait "the agent to write the build's registry Secret" "$rt_secret_timeout" rt_build_secret_exists "$build" ||
        return 1
    first=$(rt_build_password "$build") && rt_save build-a-resend.first "$first" ||
        { rt_fail "read the build's first credential"; return 1; }
    assert "the build Secret is a ${rt_build_user} credential" \
        test "$(registry_secret_field "$rt_builds_ns" "build-${build}-registry" username)" = "$rt_build_user"

    kill_pid_file "$PID_DIR/agent.pid"
    E2E_JOIN_TOKEN="" start_agent
    rt_wait "the resent StartBuild to replace the build Secret's password" "$rt_secret_timeout" \
        rt_build_password_changed "$build" "$first" || return 1
    second=$(rt_build_password "$build") && rt_save build-a-resend.second "$second" ||
        { rt_fail "read the build's resent credential"; return 1; }
    assert "the resend replaced the build Secret's password" test "$second" != "$first"
    assert "the build is still queued" test "$(rt_build "$build" status)" = queued
}

test_r09_build_credential_is_scoped_to_its_repository() {
    local build password repo_a repo_b digest_b layer token location
    build=$(rt_load build-a-resend) && password=$(rt_load build-a-resend.second) &&
        repo_a=$(rt_repository "$build") && repo_b=$(rt_repository "$(rt_load build-b-1)") &&
        digest_b=$(rt_digest "$(rt_load build-b-1)") || { rt_fail "read the queued build and B's build"; return 1; }

    token=$(registry_realm_token "$rt_build_user" "$password" "repository:${repo_a}:pull,push") ||
        { rt_fail "exchange the queued build's credential"; return 1; }
    assert "the queued build's credential gets pull and push on its repository" \
        rt_token_grants "$token" "$(rt_access "$repo_a" '"pull","push"')"

    token=$(registry_realm_token "$rt_build_user" "$password" "repository:${repo_b}:pull,push") ||
        { rt_fail "exchange the build credential for B's repository"; return 1; }
    assert "the build credential gets no access to B's repository" rt_token_grants "$token" "[]"
    assert "the registry answers a push into B with that token with 401 and a challenge for push on B" \
        rt_refused "$repo_b" push "$token" POST "/v2/${repo_b}/blobs/uploads/"

    token=$(rt_pull_token "$rt_ws_b" "$repo_b") &&
        layer=$(curl -s -H "Authorization: Bearer ${token}" -H "Accept: ${rt_manifest_type}" \
            "${E2E_REGISTRY_URL}/v2/${repo_b}/manifests/${digest_b}" | yq -p json -e -r '.layers[-1].digest') &&
        [[ $layer =~ $rt_digest_pattern ]] || { rt_fail "read the top layer of B's image"; return 1; }
    token=$(registry_realm_token "$rt_build_user" "$password" \
        "repository:${repo_a}:pull,push" "repository:${repo_b}:pull") ||
        { rt_fail "exchange the build credential for a cross-repository mount"; return 1; }
    assert "a mount request's token covers only the build's repository" \
        rt_token_grants "$token" "$(rt_access "$repo_a" '"pull","push"')"
    assert "B's top layer ${layer} is not in A's repository" \
        test "$(registry_status_with_token "$token" GET "/v2/${repo_a}/blobs/${layer}")" = 404
    location=$(registry_with_token "$token" POST "/v2/${repo_a}/blobs/uploads/?mount=${layer}&from=${repo_b}" \
        -D - -w 'status %{http_code}\n' | tr -d '\r' | awk 'tolower($1) == "location:" || $1 == "status" { print }')
    log_info "Mount response: ${location//$'\n'/; }"
    assert "the registry answers the mount from B with 202 and an ordinary upload in A" \
        grep -qx "status 202" <<<"$location"
    assert "the upload it opened is in A's repository" \
        grep -qiE "^location: /v2/${repo_a}/blobs/uploads/[^/?]+" <<<"$location"
    assert "B's layer is still absent from A's repository" \
        test "$(registry_status_with_token "$token" GET "/v2/${repo_a}/blobs/${layer}")" = 404
}

test_r10_resent_build_succeeds() {
    local build container
    build=$(rt_load build-a-resend) || { rt_fail "read the queued build"; return 1; }
    rk -n "$E2E_LOCO_NAMESPACE" scale deployment "$rt_build_controller" --replicas=1 >/dev/null
    rt_wait "the resent build to finish" "$rt_build_timeout" rt_build_finished "$build"
    if ! assert "the resent build succeeded with the replaced credential" test "$(rt_build "$build" status)" = succeeded; then
        for container in fetch build push; do
            rk -n "$rt_builds_ns" logs "job/build-${build}" -c "$container" 2>&1 | tail -40 | sed 's/^/    /'
        done
    fi
    assert_contains "fetch and push mounted the build's own Secret" "build-${build}-registry" \
        rk -n "$rt_builds_ns" get job "build-${build}" -o jsonpath='{.spec.template.spec.volumes[*].secret.secretName}'
}

test_r11_finished_build_credential_is_refused() {
    local build repository first second
    build=$(rt_load build-a-resend) && repository=$(rt_repository "$build") &&
        first=$(rt_load build-a-resend.first) && second=$(rt_load build-a-resend.second) ||
        { rt_fail "read the finished build's credentials"; return 1; }
    assert "the finished build's first credential gets 401" \
        rt_realm_refuses "$rt_build_user" "$first" "repository:${repository}:pull"
    assert "the finished build's resent credential gets 401" \
        rt_realm_refuses "$rt_build_user" "$second" "repository:${repository}:pull"
}

test_r12_renewal_replaces_the_pull_secret() {
    local before before_expires after issued renewed repository image left name=rt-renewed-pull namespace
    before=$(rt_pull_password "$rt_ws_b") && issued=$(rt_annotation "$rt_ws_b" "$rt_issued_at") &&
        before_expires=$(rt_annotation "$rt_ws_b" "$rt_expires_at") &&
        repository=$(rt_repository "$(rt_load build-b-1)") && image=$(rt_image "$(rt_load build-b-1)") ||
        { rt_fail "read B's pull Secret and image"; return 1; }
    rt_wait "the agent to renew B's pull credential (issued ${issued})" "$rt_renewal_timeout" \
        rt_pull_password_changed "$rt_ws_b" "$before" || return 1
    after=$(rt_pull_password "$rt_ws_b") && renewed=$(rt_annotation "$rt_ws_b" "$rt_issued_at") ||
        { rt_fail "read B's renewed pull Secret"; return 1; }
    assert "the renewed issue time is later (${issued} -> ${renewed})" test "$renewed" \> "$issued"
    assert "the renewed credential works at the token endpoint" \
        rt_realm_accepts "$rt_pull_user" "$after" "repository:${repository}:pull"
    left=$(rt_seconds_left "$before_expires") || { rt_fail "parse ${before_expires}"; return 1; }
    if [ "$left" -gt "$rt_expiry_margin" ]; then
        assert "the previous credential works until it expires (${left}s left)" \
            rt_realm_accepts "$rt_pull_user" "$before" "repository:${repository}:pull"
    elif [ "$left" -lt "-$rt_expiry_margin" ]; then
        assert "the expired previous credential gets 401 (expired ${left#-}s ago)" \
            rt_realm_refuses "$rt_pull_user" "$before" "repository:${repository}:pull"
    else
        log_warn "The previous credential expires within ${rt_expiry_margin}s (${left}s); not checking it"
        E2E_SKIP=$((E2E_SKIP + 1))
    fi
    rt_wait "the controller to copy the renewed credential into B's namespace" "$rt_secret_timeout" \
        rt_workspace_copy_is "$rt_ws_b" "$after"
    assert "B's namespace holds the renewed credential" rt_workspace_copy_is "$rt_ws_b" "$after"

    namespace=$(rt_namespace "$rt_ws_b")
    rt_pod "$namespace" "$name" "$image"
    rt_wait "a pod in B to pull its image with the renewed credential" "$rt_pull_timeout" rt_pod_started "$namespace" "$name"
    if ! assert "a pod pulled B's image after the renewal" rt_pod_started "$namespace" "$name"; then
        rk -n "$namespace" describe pod "$name" | sed 's/^/    /'
    fi
    rk -n "$namespace" delete pod "$name" --wait=false >/dev/null
}

test_r13_removed_workspace_loses_its_credential() {
    local password expires repository resource_id left
    repository=$(rt_repository "$(rt_load build-a-2)") && password=$(rt_pull_password "$rt_ws_a") &&
        expires=$(rt_annotation "$rt_ws_a" "$rt_expires_at") && resource_id=$(rt_resource_id "$rt_app_a") ||
        { rt_fail "read A's pull Secret and resource"; return 1; }
    assert "A's current pull credential works before the removal" \
        rt_realm_accepts "$rt_pull_user" "$password" "repository:${repository}:pull"
    assert "the API deleted ${rt_app_a}" \
        rt_api loco.resource.v1.ResourceService/DeleteResource "{\"resourceId\":\"${resource_id}\"}"
    rt_wait "A's credential to stop working at the token endpoint" "$rt_revocation_timeout" \
        rt_realm_refuses "$rt_pull_user" "$password" "repository:${repository}:pull"
    left=$(rt_seconds_left "$expires") || { rt_fail "parse ${expires}"; return 1; }
    assert "A's credential gets 401 after the removal while it has ${left}s left" \
        rt_realm_refuses "$rt_pull_user" "$password" "repository:${repository}:pull"
    assert "the refusal came at least ${rt_expiry_margin}s before the credential's expiry" \
        test "$left" -gt "$rt_expiry_margin"
    rt_wait "the agent to delete A's pull Secret" "$rt_revocation_timeout" rt_pull_secret_gone "$rt_ws_a"
    assert "the agent deleted $(rt_pull_secret "$rt_ws_a")" rt_pull_secret_gone "$rt_ws_a"
    assert "B's pull Secret remains" rt_pull_password "$rt_ws_b"
}
