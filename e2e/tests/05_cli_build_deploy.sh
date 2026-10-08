#!/usr/bin/env bash

source "$E2E_ROOT_DIR/e2e/cli.sh"

if [ "${E2E_BUILDS_ENABLED:-true}" = false ]; then
    E2E_SKIP_REASON="builds are disabled"
fi

cli_ctx="kind-${E2E_KIND_CLUSTER}"
cli_dir="$E2E_BUILD_WORK_DIR/cli"
cli_home="$cli_dir/home"
cli_app_dir="$cli_dir/app"
cli_app="e2e-cli-app"

cli_setup() {
    rm -rf "$cli_dir"
    mkdir -p "$cli_app_dir"
    cli_write_credentials
    cp -R "$E2E_ROOT_DIR/e2e/fixtures/build-app/." "$cli_app_dir/"
    printf 'SECRET=never-uploaded\n' >"$cli_app_dir/.env"
    cli_write_service_config "$cli_app_dir" "$cli_app" 8080 /healthz
}

cli_built_image() {
    loco_cli builds list "$cli_app" --output json |
        yq -p json -r '.builds[0] | .imageRepository + "@" + .imageDigest'
}

test_c01_cli_deploys_from_source() {
    cli_seed_session
    cli_setup

    local started elapsed rc=0
    started=$(date +%s)
    (cd "$cli_app_dir" && loco_cli deploy "$cli_app" --wait) >"$cli_dir/deploy.out" 2>"$cli_dir/deploy.err" || rc=$?
    elapsed=$(($(date +%s) - started))
    if ! assert "loco deploy built and deployed ${cli_app} (exit ${rc}, ${elapsed}s)" test "$rc" -eq 0; then
        sed 's/^/    /' "$cli_dir/deploy.out" "$cli_dir/deploy.err"
        return 1
    fi
    log_info "loco deploy took ${elapsed}s"
    assert_contains "CLI packed the fixture" "Packed" cat "$cli_dir/deploy.out"
    assert_contains "CLI followed the build to success" "succeeded in" cat "$cli_dir/deploy.out"
    assert_contains "CLI waited for the deployment to run" "\[running\]" cat "$cli_dir/deploy.out"
    assert_contains "Without a log proxy, the CLI says so and keeps following the build" \
        "Build logs are unavailable" cat "$cli_dir/deploy.out"
}

test_c02_cli_lists_the_build() {
    assert_contains "loco builds list shows the build succeeded" "succeeded" \
        loco_cli builds list "$cli_app"
    (cd "$cli_app_dir" && loco_cli builds list) >"$cli_dir/list.out" 2>&1
    assert_contains "loco builds list reads the service from loco.toml" "succeeded" cat "$cli_dir/list.out"
    assert_contains "loco builds list --output json reports the digest" '"imageDigest":"sha256:' \
        loco_cli builds list "$cli_app" --output json
}

test_c03_cli_deployment_runs_the_build() {
    local image
    image=$(cli_built_image)
    if ! assert "loco builds list reported the built image (${image})" grep -q '@sha256:[a-f0-9]\{64\}$' <<<"$image"; then
        return 1
    fi
    assert_contains "The deployed app runs the CLI's build by digest" "$image" \
        kubectl --context "$cli_ctx" -n "ws-${cli_workspace_id}" get deployments \
        -o jsonpath='{.items[*].spec.template.spec.containers[*].image}'
}
