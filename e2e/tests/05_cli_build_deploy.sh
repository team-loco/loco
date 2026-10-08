#!/usr/bin/env bash

cli_ctx="kind-${E2E_KIND_CLUSTER}"
cli_dir="$E2E_BUILD_WORK_DIR/cli"
cli_home="$cli_dir/home"
cli_app_dir="$cli_dir/app"
cli_app="e2e-cli-app"
cli_user_id='00000000-0000-7000-8000-000000000001'
cli_workspace_id='00000000-0000-7000-8000-000000000003'
cli_session_id='00000000-0000-7000-8000-000000000007'

loco_cli() {
    HOME="$cli_home" LOCO_CREDENTIAL_STORE=file LOCO_HOST="$E2E_API_URL" \
        "$E2E_ROOT_DIR/e2e/bin/loco" "$@"
}

cli_seed_session() {
    e2e_psql "
        INSERT INTO user_scopes (user_id, scope, entity_type, entity_id)
        VALUES
            ('${cli_user_id}', 'read', 'workspace', '${cli_workspace_id}'),
            ('${cli_user_id}', 'write', 'workspace', '${cli_workspace_id}')
        ON CONFLICT DO NOTHING;
        INSERT INTO session_tokens (id, access_token_hash, refresh_token_hash, user_id,
                                    access_expires_at, refresh_expires_at)
        VALUES (
            '${cli_session_id}',
            encode(sha256(convert_to('${E2E_USER_TOKEN}', 'UTF8')), 'hex'),
            encode(sha256(convert_to('refresh-${E2E_USER_TOKEN}', 'UTF8')), 'hex'),
            '${cli_user_id}',
            NOW() + INTERVAL '1 day',
            NOW() + INTERVAL '1 day'
        ) ON CONFLICT DO NOTHING;
    " >/dev/null
}

cli_setup() {
    rm -rf "$cli_dir"
    mkdir -p "$cli_home/.loco" "$cli_app_dir"
    printf '{"Host":"%s","ExpiresAt":"2099-01-01T00:00:00Z","Token":"%s","RefreshToken":""}\n' \
        "$E2E_API_URL" "$E2E_USER_TOKEN" >"$cli_home/.loco/credentials.json"
    cat >"$cli_home/.loco/config.toml" <<TOML
currentScope = "default"

[scopes.default.organization]
name = "e2e-test-org"
id = "00000000-0000-7000-8000-000000000002"

[scopes.default.workspace]
name = "e2e-test-workspace"
id = "${cli_workspace_id}"
TOML
    cp -R "$E2E_ROOT_DIR/e2e/fixtures/build-app/." "$cli_app_dir/"
    printf 'SECRET=never-uploaded\n' >"$cli_app_dir/.env"
    cat >"$cli_app_dir/loco.toml" <<TOML
[Metadata]
ConfigVersion = "0.1"
Name = "${cli_app}"
Type = "SERVICE"
Region = "us-east-1"

[Build]
DockerfilePath = "deploy/Dockerfile"
Type = "docker"

[Routing]
Port = 8080
PathPrefix = "/"
IdleTimeout = 60

[DomainConfig]
Type = "platform"
Hostname = "${cli_app}.e2e.test.local"

[RegionConfig]
[RegionConfig.us-east-1]
CPU = "100m"
Memory = "128Mi"
ReplicasMin = 1
ReplicasMax = 1

[Health]
Path = "/healthz"
Interval = 5
Timeout = 3
StartupGracePeriod = 0
FailThreshold = 3
TOML
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
