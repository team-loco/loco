#!/usr/bin/env bash

source "$E2E_ROOT_DIR/e2e/cli.sh"

if [ "${E2E_BUILDS_ENABLED:-true}" = true ]; then
    E2E_SKIP_REASON="builds are enabled; run with --builds-disabled"
fi

nobuild_ctx="kind-${E2E_KIND_CLUSTER}"
nobuild_cluster_id='00000000-0000-7000-8000-000000000005'
nobuild_dir="$E2E_BUILD_WORK_DIR/no-builds"
cli_home="$nobuild_dir/home"
nobuild_image_dir="$nobuild_dir/image-app"
nobuild_source_dir="$nobuild_dir/source-app"
nobuild_image_app="e2e-public-image"
nobuild_source_app="e2e-no-builds-source"
nobuild_public_image="$E2E_PUBLIC_IMAGE"

nk() {
    kubectl --context "$nobuild_ctx" "$@"
}

nobuild_setup() {
    rm -rf "$nobuild_dir"
    mkdir -p "$nobuild_image_dir" "$nobuild_source_dir"
    cli_write_credentials
    cli_write_service_config "$nobuild_image_dir" "$nobuild_image_app" 8080 /
    cp -R "$E2E_ROOT_DIR/e2e/fixtures/build-app/." "$nobuild_source_dir/"
    cli_write_service_config "$nobuild_source_dir" "$nobuild_source_app" 8080 /healthz
}

test_n01_no_build_objects_are_installed() {
    assert_fails "The Build CRD is not installed" nk get crd builds.infra.loco.io
    assert_fails "The builds namespace does not exist" nk get namespace "$E2E_BUILD_NAMESPACE"
    assert_fails "The build controller Deployment does not exist" \
        nk -n "$E2E_LOCO_NAMESPACE" get deployment loco-build-controller
    assert_fails "The build controller ClusterRole does not exist" nk get clusterrole loco-build-controller
    assert "The Application controller is available" \
        nk -n "$E2E_LOCO_NAMESPACE" rollout status deployment/loco-controller --timeout 60s
    local images
    images=$(nk get pods -A -o jsonpath='{.items[*].spec.containers[*].image}')
    assert_fails "No pod runs a builder or BuildKit image" grep -qE 'loco-builder|buildkit' <<<"$images"
}

test_n02_agent_reports_builds_disabled() {
    local enabled
    enabled=$(e2e_psql "SELECT builds_enabled FROM clusters WHERE id = '${nobuild_cluster_id}'")
    assert "The agent reported that the cluster does not run builds" test "$enabled" = "f"
}

test_n03_public_image_deploys() {
    cli_seed_session
    nobuild_setup
    local rc=0
    (cd "$nobuild_image_dir" && loco_cli deploy "$nobuild_image_app" --image "$nobuild_public_image" --wait) \
        >"$nobuild_dir/image.out" 2>"$nobuild_dir/image.err" || rc=$?
    if ! assert "loco deploy --image deployed ${nobuild_public_image} (exit ${rc})" test "$rc" -eq 0; then
        sed 's/^/    /' "$nobuild_dir/image.out" "$nobuild_dir/image.err"
        return 1
    fi
    assert_contains "The CLI waited for the deployment to run" "\[running\]" cat "$nobuild_dir/image.out"
    local repository=${nobuild_public_image%%:*}
    assert_contains "The deployed app runs the public image, pinned by digest" "${repository}@sha256:" \
        nk -n "ws-${cli_workspace_id}" get deployments \
        -o jsonpath='{.items[*].spec.template.spec.containers[*].image}'
}

test_n04_source_deploy_explains_builds_are_unavailable() {
    local rc=0
    (cd "$nobuild_source_dir" && loco_cli deploy "$nobuild_source_app") \
        >"$nobuild_dir/source.out" 2>"$nobuild_dir/source.err" || rc=$?
    assert "loco deploy from source fails (exit ${rc})" test "$rc" -ne 0
    local stderr
    stderr=$(tr -s ' \n' ' ' <"$nobuild_dir/source.err")
    assert_contains "The CLI says the install cannot build from source" \
        "cannot build from source: no cluster in this install accepts builds" echo "$stderr"
    assert_contains "The CLI suggests deploying a prebuilt image" "--image <image>" echo "$stderr"
    local builds
    builds=$(e2e_psql "SELECT count(*) FROM builds")
    assert "No build was created" test "$builds" -eq 0
}
