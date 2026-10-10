#!/usr/bin/env bash

source "$E2E_ROOT_DIR/e2e/cli.sh"

if [ "${E2E_BUILDS_ENABLED:-true}" = false ]; then
    E2E_SKIP_REASON="builds are disabled"
fi

config_ctx="kind-${E2E_KIND_CLUSTER}"
config_dir="$E2E_BUILD_WORK_DIR/config"
cli_home="$config_dir/home"
config_repo="$config_dir/repo"
config_env=production
config_web=e2e-config-web
config_cache=e2e-config-cache
config_image="$E2E_PUBLIC_IMAGE"

config_cli() {
    (cd "$config_repo" && loco_cli "$@")
}

config_run() {
    local name=$1
    shift
    local rc=0
    config_cli "$@" >"$config_dir/$name.out" 2>"$config_dir/$name.err" || rc=$?
    return "$rc"
}

config_show() {
    sed 's/^/    /' "$config_dir/$1.out" "$config_dir/$1.err"
}

config_out_contains() {
    grep -q -- "$2" "$config_dir/$1.out"
}

config_resource_count() {
    test "$(e2e_psql "SELECT count(*) FROM resources WHERE name = '$1'")" -eq "$2"
}

config_placement_count() {
    local id
    id=$(e2e_psql "SELECT id FROM resources WHERE name = '$1'")
    test "$(e2e_psql "SELECT count(*) FROM placements WHERE resource_id = '${id}'")" -eq "$2"
}

config_undeleted_placement_count() {
    local id
    id=$(e2e_psql "SELECT id FROM resources WHERE name = '$1'")
    test "$(e2e_psql "SELECT count(*) FROM placements WHERE resource_id = '${id}' AND NOT desired_deleted")" -eq "$2"
}

config_application_absent() {
    local id=$1
    ! kubectl --context "$config_ctx" -n "$E2E_LOCO_NAMESPACE" get application "resource-${id}" >/dev/null 2>&1
}

config_edit() {
    yq -i "$1" "$config_repo/loco.yaml"
}

config_grant_admin() {
    e2e_psql "
        INSERT INTO user_scopes (user_id, scope, entity_type, entity_id)
        VALUES ('${cli_user_id}', 'admin', 'workspace', '${cli_workspace_id}')
        ON CONFLICT DO NOTHING;
    " >/dev/null
}

config_write_repo() {
    rm -rf "$config_dir"
    mkdir -p "$config_repo/services/web"
    cli_write_credentials
    cp -R "$E2E_ROOT_DIR/e2e/fixtures/build-app/." "$config_repo/services/web/"
    cat >"$config_repo/loco.yaml" <<YAML
version: 1
partial: e2e-config
services:
  ${config_web}:
    dockerfile: deploy/Dockerfile
    context: services/web
    port: 8080
    routing: { pathPrefix: /, idleTimeout: 60 }
    health: { path: /healthz, interval: 5, timeout: 3, failThreshold: 3, startupGracePeriod: 0 }
    domains: [${config_web}.e2e.test.local]
    env: { TIER: base }
    regions:
      us-east-1: { cpu: 100m, memory: 128Mi, replicas: { min: 1, max: 1 } }
    environments:
      ${config_env}:
        env: { TIER: ${config_env} }
  ${config_cache}:
    image: ${config_image}
    port: 8080
    routing: { pathPrefix: /, idleTimeout: 60 }
    health: { path: /, interval: 5, timeout: 3, failThreshold: 3, startupGracePeriod: 0 }
    regions:
      us-east-1: { cpu: 100m, memory: 128Mi, replicas: { min: 1, max: 1 } }
YAML
}

test_g01_validate_accepts_the_file() {
    cli_seed_session
    config_grant_admin
    config_write_repo
    if ! assert "loco infra validate accepts the file" config_run validate infra validate; then
        config_show validate
        return 1
    fi
    assert "validate lists both services" config_out_contains validate "${config_cache}, ${config_web}"
}

test_g02_plan_creates_both_services() {
    if ! assert "loco infra plan succeeds" config_run plan infra plan --env "$config_env"; then
        config_show plan
        return 1
    fi
    assert "the plan creates two services" config_out_contains plan "Plan: 2 to create"
    assert "the source service needs a deploy" config_out_contains plan "needs deploy"
    assert "the environment override reaches the plan" config_out_contains plan "env.TIER: ${config_env}"
    assert "plan creates nothing" config_resource_count "$config_web" 0
}

test_g03_apply_creates_the_image_service() {
    if ! assert "loco infra apply --yes succeeds" config_run apply infra apply --env "$config_env" --yes; then
        config_show apply
        return 1
    fi
    assert "the image service has a resource" config_resource_count "$config_cache" 1
    assert "the image service starts a deployment" config_out_contains apply "Started deployment .* for ${config_cache}"
    wait_for "the image service to be Ready" 120 cli_application_is_ready "$config_cache"
    config_run plan_after_apply infra plan --env "$config_env" || true
    assert "the source service still needs a deploy" config_out_contains plan_after_apply "needs deploy"
}

test_g04_deploy_builds_and_applies() {
    local rc=0 started
    started=$(date +%s)
    config_run deploy deploy --env "$config_env" --yes || rc=$?
    if ! assert "loco deploy builds and applies (exit ${rc}, $(($(date +%s) - started))s)" test "$rc" -eq 0; then
        config_show deploy
        return 1
    fi
    assert "deploy built the source service from its context" config_out_contains deploy "Building ${config_web} from deploy/Dockerfile in services/web"
    assert "deploy followed the build" config_out_contains deploy "succeeded in"
    assert "deploy prints the public URL" config_out_contains deploy "${config_web}: https://${config_web}.e2e.test.local"
    wait_for "the source service to be Ready" 120 cli_application_is_ready "$config_web"
    assert "the image service is still Ready" cli_application_is_ready "$config_cache"
}

test_g05_second_plan_is_clean() {
    if ! assert "loco infra plan succeeds" config_run clean infra plan --env "$config_env"; then
        config_show clean
        return 1
    fi
    assert "the second plan has no changes" config_out_contains clean "No changes"
}

test_g06_replicas_change_is_one_update() {
    config_edit ".services.${config_cache}.regions.us-east-1.replicas.max = 2"
    config_run scale_plan infra plan --env "$config_env" || true
    assert "the plan has one update" config_out_contains scale_plan "Plan: 1 to update"
    assert "the plan changes replicas" config_out_contains scale_plan "replicas"
    if ! assert "loco infra apply --yes applies the update" config_run scale_apply infra apply --env "$config_env" --yes; then
        config_show scale_apply
        return 1
    fi
    config_run scale_clean infra plan --env "$config_env" || true
    assert "the plan is clean after the update" config_out_contains scale_clean "No changes"
}

test_g07_removing_a_service_deletes_it() {
    local id
    id=$(e2e_psql "SELECT id FROM resources WHERE name = '${config_cache}'")
    config_edit "del(.services.${config_cache})"
    config_run delete_plan infra plan --env "$config_env" || true
    assert "the plan deletes one service" config_out_contains delete_plan "Plan: 1 to delete"
    assert "the delete is destructive" config_out_contains delete_plan "destructive"
    assert_fails "apply refuses without --confirm-destructive" config_run delete_refused infra apply --env "$config_env" --yes
    assert "the refused apply kept the service" config_resource_count "$config_cache" 1
    if ! assert "apply --confirm-destructive deletes the service" \
        config_run delete_apply infra apply --env "$config_env" --yes --confirm-destructive; then
        config_show delete_apply
        return 1
    fi
    assert "the resource is gone" config_resource_count "$config_cache" 0
    wait_for "the Application to be removed" 60 config_application_absent "$id"
}

test_g08_disabling_the_environment_stops_the_service() {
    config_edit ".services.${config_web}.environments.${config_env}.enabled = false"
    config_run stop_plan infra plan --env "$config_env" || true
    assert "the plan updates the service" config_out_contains stop_plan "Plan: 1 to update"
    assert "the plan disables the service" config_out_contains stop_plan "enabled: true -> false"
    if ! assert "apply --confirm-destructive stops the service" \
        config_run stop_apply infra apply --env "$config_env" --yes --confirm-destructive; then
        config_show stop_apply
        return 1
    fi
    local id
    id=$(e2e_psql "SELECT id FROM resources WHERE name = '${config_web}'")
    assert "the resource still exists" config_resource_count "$config_web" 1
    assert "apply marks the placement deleted" config_undeleted_placement_count "$config_web" 0
    wait_for "the agent to delete the placement" 60 config_placement_count "$config_web" 0
    wait_for "the Application to be removed" 60 config_application_absent "$id"
    config_run stop_clean infra plan --env "$config_env" || true
    assert "the plan is clean once the service is stopped" config_out_contains stop_clean "No changes"
}
