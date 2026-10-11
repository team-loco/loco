#!/usr/bin/env bash

cli_user_id='00000000-0000-7000-8000-000000000001'
cli_org_id='00000000-0000-7000-8000-000000000002'
cli_workspace_id='00000000-0000-7000-8000-000000000003'
cli_session_id='00000000-0000-7000-8000-000000000007'

loco_cli() {
    HOME="$cli_home" LOCO_CREDENTIAL_STORE=file LOCO_HOST="$E2E_API_URL" \
        "$E2E_BIN_DIR/loco" "$@"
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

cli_write_credentials() {
    mkdir -p "$cli_home/.loco"
    printf '{"Host":"%s","ExpiresAt":"2099-01-01T00:00:00Z","Token":"%s","RefreshToken":""}\n' \
        "$E2E_API_URL" "$E2E_USER_TOKEN" >"$cli_home/.loco/credentials.json"
    cat >"$cli_home/.loco/config.toml" <<TOML
currentScope = "default"

[scopes.default.organization]
name = "e2e-test-org"
id = "${cli_org_id}"

[scopes.default.workspace]
name = "e2e-test-workspace"
id = "${cli_workspace_id}"
TOML
}

# Write a loco.yaml with one service. source is "dockerfile: deploy/Dockerfile" or
# "image: <ref>"; domains is a YAML list such as "[name.e2e.test.local]" or "[]".
cli_write_loco_yaml() {
    local dir=$1 name=$2 source=$3 port=$4 health_path=$5 domains=$6
    cat >"$dir/loco.yaml" <<YAML
version: 1
partial: ${name}
services:
  ${name}:
    ${source}
    port: ${port}
    routing: { pathPrefix: /, idleTimeout: 60 }
    health: { path: ${health_path}, interval: 5, timeout: 3, failThreshold: 3, startupGracePeriod: 0 }
    domains: ${domains}
    regions:
      us-east-1: { cpu: 100m, memory: 128Mi, replicas: { min: 1, max: 1 } }
YAML
}

cli_write_service_config() {
    local dir=$1 name=$2 port=$3 health_path=$4
    cli_write_loco_yaml "$dir" "$name" "dockerfile: deploy/Dockerfile" "$port" "$health_path" "[${name}.e2e.test.local]"
}

cli_write_private_service_config() {
    local dir=$1 name=$2 port=$3 health_path=$4
    cli_write_loco_yaml "$dir" "$name" "dockerfile: deploy/Dockerfile" "$port" "$health_path" "[]"
}

cli_write_image_config() {
    local dir=$1 name=$2 image=$3 port=$4 domains=$5
    cli_write_loco_yaml "$dir" "$name" "image: ${image}" "$port" / "$domains"
}

cli_application_phase() {
    local id
    id=$(e2e_psql "SELECT id FROM resources WHERE name = '$1'")
    kubectl --context "kind-${E2E_KIND_CLUSTER}" -n "$E2E_LOCO_NAMESPACE" \
        get application "resource-${id}" -o jsonpath='{.status.phase}'
}

cli_application_is_ready() {
    test "$(cli_application_phase "$1")" = Ready
}
