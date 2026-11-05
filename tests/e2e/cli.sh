#!/usr/bin/env bash

cli_user_id=${E2E_USER_ID:-}
cli_org_id=${E2E_ORG_ID:-}
cli_workspace_id=${E2E_WORKSPACE_ID:-}

loco_cli() {
    HOME="$cli_home" LOCO_CREDENTIAL_STORE=file LOCO_HOST="$E2E_API_URL" \
        "$E2E_BIN_DIR/loco" "$@"
}

fixture_workspace() {
    curl -sS --fail-with-body -X POST "${E2E_API_URL}/loco.workspace.v1.WorkspaceService/CreateWorkspace" \
        -H "Authorization: Bearer ${E2E_USER_TOKEN}" -H 'Content-Type: application/json' \
        -d "{\"orgId\":\"${cli_org_id}\",\"name\":\"$1\"}" | yq -p json '.workspaceId'
}

cli_write_credentials() {
    mkdir -p "$cli_home/.loco"
    yq -p json -o json '.userCredentials' "$E2E_AUTH_STATE" >"$cli_home/.loco/credentials.json"
    chmod 600 "$cli_home/.loco/credentials.json"
    cat >"$cli_home/.loco/config.yaml" <<YAML
currentScope: default
scopes:
  default:
    organization:
      name: e2e-test-org
      id: "${cli_org_id}"
    workspace:
      name: e2e-test-workspace
      id: "${cli_workspace_id}"
YAML
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
