#!/usr/bin/env bash

cli_user_id='00000000-0000-7000-8000-000000000001'
cli_org_id='00000000-0000-7000-8000-000000000002'
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

cli_write_service_config() {
    local dir=$1 name=$2 port=$3 health_path=$4
    cli_write_private_service_config "$dir" "$name" "$port" "$health_path"
    cat >>"$dir/loco.toml" <<TOML

[DomainConfig]
Type = "platform"
Hostname = "${name}.e2e.test.local"
TOML
}

cli_write_private_service_config() {
    local dir=$1 name=$2 port=$3 health_path=$4
    cat >"$dir/loco.toml" <<TOML
[Metadata]
ConfigVersion = "0.1"
Name = "${name}"
Type = "SERVICE"
Region = "us-east-1"

[Build]
DockerfilePath = "deploy/Dockerfile"
Type = "docker"

[Routing]
Port = ${port}
PathPrefix = "/"
IdleTimeout = 60

[RegionConfig]
[RegionConfig.us-east-1]
CPU = "100m"
Memory = "128Mi"
ReplicasMin = 1
ReplicasMax = 1

[Health]
Path = "${health_path}"
Interval = 5
Timeout = 3
StartupGracePeriod = 0
FailThreshold = 3
TOML
}
