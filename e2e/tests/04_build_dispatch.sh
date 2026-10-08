#!/usr/bin/env bash

if [ "${E2E_BUILDS_ENABLED:-true}" = false ]; then
    E2E_SKIP_REASON="builds are disabled"
fi

dispatch_ctx="kind-${E2E_KIND_CLUSTER}"
dispatch_ns="$E2E_BUILD_NAMESPACE"
dispatch_dir="$E2E_BUILD_WORK_DIR/dispatch"
dispatch_user_id='00000000-0000-7000-8000-000000000001'
dispatch_workspace_id='00000000-0000-7000-8000-000000000003'
dispatch_environment_id='00000000-0000-7000-8000-000000000004'
dispatch_platform_domain_id='00000000-0000-7000-8000-000000000006'
dispatch_session_id='00000000-0000-7000-8000-000000000007'
dispatch_resource_name='e2e-built-app'
dispatch_digest_pattern='^sha256:[a-f0-9]{64}$'

dk() {
    kubectl --context "$dispatch_ctx" "$@"
}

api() {
    local method=$1 body=$2
    curl -sS --fail-with-body -X POST "${E2E_API_URL}/${method}" \
        -H "Authorization: Bearer ${E2E_USER_TOKEN}" \
        -H 'Content-Type: application/json' \
        -d "$body"
}

json() {
    yq -p json -o json -I 0 "$@"
}

field() {
    yq -p json -r "$1 // \"\""
}

dispatch_state() {
    cat "$dispatch_dir/$1" 2>/dev/null
}

save_state() {
    mkdir -p "$dispatch_dir"
    printf '%s' "$2" >"$dispatch_dir/$1"
}

seed_session() {
    e2e_psql "
        INSERT INTO user_scopes (user_id, scope, entity_type, entity_id)
        VALUES
            ('${dispatch_user_id}', 'read', 'workspace', '${dispatch_workspace_id}'),
            ('${dispatch_user_id}', 'write', 'workspace', '${dispatch_workspace_id}'),
            ('${dispatch_user_id}', 'admin', 'workspace', '${dispatch_workspace_id}')
        ON CONFLICT DO NOTHING;
        INSERT INTO session_tokens (id, access_token_hash, refresh_token_hash, user_id,
                                    access_expires_at, refresh_expires_at)
        VALUES (
            '${dispatch_session_id}',
            encode(sha256(convert_to('${E2E_USER_TOKEN}', 'UTF8')), 'hex'),
            encode(sha256(convert_to('refresh-${E2E_USER_TOKEN}', 'UTF8')), 'hex'),
            '${dispatch_user_id}',
            NOW() + INTERVAL '1 day',
            NOW() + INTERVAL '1 day'
        ) ON CONFLICT DO NOTHING;
    " >/dev/null
}

create_resource() {
    local body
    body=$(json -n "
        .workspaceId = \"${dispatch_workspace_id}\" |
        .name = \"${dispatch_resource_name}\" |
        .type = \"RESOURCE_TYPE_SERVICE\" |
        .domain.domainSource = \"DOMAIN_TYPE_PLATFORM_PROVIDED\" |
        .domain.subdomain = \"${dispatch_resource_name}\" |
        .domain.platformDomainId = \"${dispatch_platform_domain_id}\" |
        .spec.service.routing.port = 8080 |
        .spec.service.routing.pathPrefix = \"/\" |
        .spec.service.regions.\"us-east-1\".enabled = true |
        .spec.service.regions.\"us-east-1\".primary = true |
        .spec.service.regions.\"us-east-1\".cpu = \"100m\" |
        .spec.service.regions.\"us-east-1\".memory = \"64Mi\" |
        .spec.service.regions.\"us-east-1\".minReplicas = 1 |
        .spec.service.regions.\"us-east-1\".maxReplicas = 1 |
        .spec.service.healthCheck.path = \"/healthz\" |
        .spec.service.healthCheck.intervalSeconds = 5 |
        .spec.service.healthCheck.timeoutSeconds = 3 |
        .spec.service.healthCheck.failureThreshold = 3
    ")
    api loco.resource.v1.ResourceService/CreateResource "$body" | field .resourceId
}

source_tarball() {
    local name=$1
    local src="$dispatch_dir/$name-src"
    rm -rf "$src"
    mkdir -p "$src"
    cp -R "$E2E_ROOT_DIR/e2e/fixtures/build-app/." "$src/"
    printf '\nfunc init() { log.SetPrefix("%s ") }\n' "$name" >>"$src/main.go"
    COPYFILE_DISABLE=1 tar -czf "$dispatch_dir/$name.tar.gz" -C "$src" .
    echo "$dispatch_dir/$name.tar.gz"
}

file_size() {
    wc -c <"$1" | tr -d ' '
}

start_build() {
    local name=$1 resource_id tarball size created build_id upload_url started status
    resource_id=$(dispatch_state resource)
    tarball=$(source_tarball "$name")
    size=$(file_size "$tarball")
    created=$(api loco.build.v1.BuildService/CreateBuild \
        "{\"resourceId\":\"${resource_id}\",\"dockerfilePath\":\"deploy/Dockerfile\",\"sourceSize\":\"${size}\"}")
    build_id=$(field .buildId <<<"$created")
    upload_url=$(field .uploadUrl <<<"$created")
    save_state "$name.upload-url" "$upload_url"
    if ! curl -sS --fail-with-body -X PUT -T "$tarball" "$upload_url" >/dev/null; then
        log_error "Upload of ${name} to the presigned URL failed" >&2
    fi
    started=$(api loco.build.v1.BuildService/StartBuild "{\"buildId\":\"${build_id}\"}")
    status=$(field .build.status <<<"$started")
    save_state "$name.start-status" "$status"
    save_state "$name.id" "$build_id"
    echo "$build_id"
}

get_build() {
    api loco.build.v1.BuildService/GetBuild "{\"buildId\":\"$1\"}"
}

build_status() {
    get_build "$1" | field .build.status
}

build_db() {
    e2e_psql "SELECT $2 FROM builds WHERE id = '$1'"
}

wait_build_finished() {
    local id=$1 max=${2:-900} elapsed=0 status=""
    log_info "Waiting for build ${id} (up to ${max}s)..."
    while [ "$elapsed" -lt "$max" ]; do
        status=$(build_status "$id")
        case "$status" in
            BUILD_STATUS_SUCCEEDED) break ;;
            BUILD_STATUS_FAILED) break ;;
            BUILD_STATUS_CANCELED) break ;;
        esac
        sleep 3
        elapsed=$((elapsed + 3))
    done
    log_info "Build ${id}: ${status} after ${elapsed}s ($(get_build "$id" | field .build.message))"
    if [ "$status" != BUILD_STATUS_SUCCEEDED ]; then
        dump_dispatch "$id"
    fi
}

dump_dispatch() {
    local id=$1
    log_error "Diagnostics for build ${id}:"
    {
        echo "--- api build"
        get_build "$id"
        echo
        echo "--- Build"
        dk -n "$dispatch_ns" get build "build-$id" -o yaml
        for container in fetch build push; do
            echo "--- logs: $container"
            dk -n "$dispatch_ns" logs "job/build-$id" -c "$container" 2>&1 | tail -60
        done
        echo "--- agent"
        grep -i build "$E2E_LOG_DIR/agent.log" | tail -30
        echo "--- api"
        grep -i build "$E2E_LOG_DIR/api.log" | tail -30
    } 2>&1 | sed 's/^/    /'
}

build_seconds() {
    e2e_psql "SELECT EXTRACT(EPOCH FROM finished_at - started_at)::int FROM builds WHERE id = '$1'"
}

digest_valid() {
    [[ $1 =~ $dispatch_digest_pattern ]]
}

source_object_exists() {
    docker run --rm --network kind \
        -e AWS_ACCESS_KEY_ID="$LOCO_SOURCE_BUCKET_ACCESS_KEY_ID" \
        -e AWS_SECRET_ACCESS_KEY="$LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY" \
        -e AWS_DEFAULT_REGION="$LOCO_SOURCE_BUCKET_REGION" \
        "$E2E_AWS_CLI_IMAGE" --endpoint-url "$E2E_S3_ENDPOINT" \
        s3api head-object --bucket "$LOCO_SOURCE_BUCKET" --key "sources/$1.tar.gz" >/dev/null 2>&1
}

agent_created_build() {
    dk -n "$dispatch_ns" get build "build-$1" >/dev/null 2>&1
}

build_cr_gone() {
    ! agent_created_build "$1"
}

test_d01_build_through_the_api() {
    seed_session
    local resource_id
    resource_id=$(create_resource)
    save_state resource "$resource_id"
    if ! assert "API created the resource (${resource_id})" test -n "$resource_id"; then
        return 1
    fi

    local id
    id=$(start_build first)
    assert "CreateBuild returned a path-style upload URL on the bucket" \
        grep -q "^${E2E_S3_ENDPOINT}/${LOCO_SOURCE_BUCKET}/sources/${id}.tar.gz" "$dispatch_dir/first.upload-url"
    assert "StartBuild queued the build" test "$(dispatch_state first.start-status)" = BUILD_STATUS_QUEUED

    wait_for "the agent to create the Build" 60 agent_created_build "$id"
    assert "Agent created Build build-${id} in ${dispatch_ns}" agent_created_build "$id"
    assert_contains "Build was sent a presigned GET for the source" "X-Amz-Signature" \
        dk -n "$dispatch_ns" get build "build-$id" -o jsonpath='{.spec.sourceURL}'

    wait_build_finished "$id"
    assert "First build reached SUCCEEDED in the API" test "$(build_status "$id")" = BUILD_STATUS_SUCCEEDED
    local image_digest cache_digest
    image_digest=$(get_build "$id" | field .build.imageDigest)
    cache_digest=$(build_db "$id" "cache_digest")
    assert "API recorded the image digest (${image_digest})" digest_valid "$image_digest"
    assert "API recorded the cache digest (${cache_digest})" digest_valid "$cache_digest"
    assert "API recorded when the build started and finished" \
        test "$(build_db "$id" "started_at IS NOT NULL AND finished_at IS NOT NULL")" = t
    assert_fails "API deleted the source after the build finished" source_object_exists "$id"
    save_state first.cache "$cache_digest"
    log_info "First build took $(build_seconds "$id")s"
}

deployment_status() {
    api loco.deployment.v1.DeploymentService/GetDeployment "{\"deploymentId\":\"$1\"}" | field .deployment.status
}

deployment_running() {
    test "$(deployment_status "$1")" = DEPLOYMENT_PHASE_RUNNING
}

deploy_build() {
    local resource_id body
    resource_id=$(dispatch_state resource)
    body=$(json -n "
        .resourceId = \"${resource_id}\" |
        .region = \"us-east-1\" |
        .environmentId = \"${dispatch_environment_id}\" |
        .spec.service.build.type = \"dockerfile\" |
        .spec.service.build.buildId = \"$1\" |
        .spec.service.port = 8080
    ")
    api loco.deployment.v1.DeploymentService/CreateDeployment "$body" 2>&1 || true
}

deploy_and_wait() {
    local build_id=$1 created deployment_id
    created=$(deploy_build "$build_id")
    deployment_id=$(field .deploymentId <<<"$created" 2>/dev/null)
    if ! assert "API created a deployment of build ${build_id} (${deployment_id})" test -n "$deployment_id"; then
        log_error "  CreateDeployment: ${created}"
        return 1
    fi

    wait_for "build ${build_id} to roll out" 240 deployment_running "$deployment_id"
    if ! assert "Deployment of build ${build_id} is RUNNING" deployment_running "$deployment_id"; then
        dk -n "ws-${dispatch_workspace_id}" describe pods | sed 's/^/    /'
        return 1
    fi
}

test_d02_deploy_the_built_image() {
    local build_id
    build_id=$(dispatch_state first.id)
    if ! deploy_and_wait "$build_id"; then
        return 1
    fi

    local repository digest
    repository=$(get_build "$build_id" | field .build.imageRepository)
    digest=$(get_build "$build_id" | field .build.imageDigest)
    assert_contains "The app runs the built image by digest" "${repository}@${digest}" \
        dk -n "ws-${dispatch_workspace_id}" get deployments \
        -o jsonpath='{.items[*].spec.template.spec.containers[*].image}'
}

test_d03_rebuild_reuses_the_cache() {
    local first second repository
    first=$(dispatch_state first.id)
    second=$(start_build second)
    repository=$(get_build "$second" | field .build.imageRepository)

    wait_for "the agent to create the second Build" 60 agent_created_build "$second"
    assert_contains "Second build was sent the first build's cache by digest" \
        "${repository}@$(dispatch_state first.cache)" \
        dk -n "$dispatch_ns" get build "build-$second" -o jsonpath='{.spec.cacheRef}'

    wait_build_finished "$second"
    assert "Second build reached SUCCEEDED in the API" test "$(build_status "$second")" = BUILD_STATUS_SUCCEEDED
    assert_contains "fetch step restored the cache" "cache restored" \
        dk -n "$dispatch_ns" logs "job/build-$second" -c fetch
    assert_contains "build step reused cached layers" CACHED \
        dk -n "$dispatch_ns" logs "job/build-$second" -c build
    log_info "Cached build took $(build_seconds "$second")s (first build: $(build_seconds "$first")s)"
}

test_d04_cancel_reaches_the_cluster() {
    local id
    id=$(start_build third)
    wait_for "the agent to create the third Build" 60 agent_created_build "$id"
    api loco.build.v1.BuildService/CancelBuild "{\"buildId\":\"${id}\"}" >/dev/null

    assert "API marked the build CANCELED" test "$(build_status "$id")" = BUILD_STATUS_CANCELED
    wait_for "the agent to delete the canceled Build" 60 build_cr_gone "$id"
    assert "Agent deleted the canceled Build" build_cr_gone "$id"
    assert_fails "API deleted the canceled build's source" source_object_exists "$id"
}

image_path() {
    local repository
    repository=$(get_build "$1" | field .build.imageRepository)
    echo "${repository#"${E2E_REGISTRY_HOST}/"}"
}

manifest_status() {
    registry_manifest_status "$E2E_REGISTRY_NODES_USER" "$1" "$2"
}

manifest_gone() {
    test "$(manifest_status "$1" "$2")" = 404
}

test_d05_retention_deletes_replaced_images() {
    local first second path first_image first_cache second_image refused
    first=$(dispatch_state first.id)
    second=$(dispatch_state second.id)
    path=$(image_path "$first")
    first_image=$(get_build "$first" | field .build.imageDigest)
    first_cache=$(dispatch_state first.cache)
    second_image=$(get_build "$second" | field .build.imageDigest)

    if ! assert "The second build's source change produced a new image digest" \
        test "$first_image" != "$second_image"; then
        return 1
    fi
    assert "A newer build exists, but the deployed first build's image is kept" \
        test "$(manifest_status "$path" "$first_image")" = 200
    assert "The first build is not reported as deleted while deployed" \
        test -z "$(get_build "$first" | field .build.imageDeletedAt)"

    if ! deploy_and_wait "$second"; then
        return 1
    fi
    wait_for "image retention to delete the replaced image" 60 manifest_gone "$path" "$first_image"
    assert "The replaced build's image manifest is gone (404 as the nodes account)" \
        manifest_gone "$path" "$first_image"
    assert "The replaced build's cache manifest is gone" manifest_gone "$path" "$first_cache"
    assert "The deployed build's image is still pullable" \
        test "$(manifest_status "$path" "$second_image")" = 200
    assert "GetBuild reports when the replaced build's image was deleted" \
        test -n "$(get_build "$first" | field .build.imageDeletedAt)"
    refused=$(deploy_build "$first")
    assert_contains "Deploying the replaced build is refused before anything is scheduled" \
        failed_precondition echo "$refused"
}

test_d06_deleted_resource_images_are_purged() {
    local resource_id second path second_image
    resource_id=$(dispatch_state resource)
    second=$(dispatch_state second.id)
    path=$(image_path "$second")
    second_image=$(get_build "$second" | field .build.imageDigest)

    api loco.resource.v1.ResourceService/DeleteResource "{\"resourceId\":\"${resource_id}\"}" >/dev/null
    wait_for "the deleted resource's images to be purged" 60 manifest_gone "$path" "$second_image"
    assert "The deleted resource's image is gone from the registry" manifest_gone "$path" "$second_image"
}
