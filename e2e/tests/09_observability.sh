#!/usr/bin/env bash

source "$E2E_ROOT_DIR/e2e/cli.sh"

if [ "${E2E_OBSERVABILITY:-false}" != true ]; then
    E2E_SKIP_REASON="the observability stack is not installed; run with --observability"
fi
if [ "${E2E_BUILDS_ENABLED:-true}" = false ]; then
    E2E_SKIP_REASON="the fixture deploys from source; run without --builds-disabled"
fi

obs_ctx="kind-${E2E_KIND_CLUSTER}"
obs_dir="$E2E_BUILD_WORK_DIR/observability"
cli_home="$obs_dir/home"
obs_app="e2e-otel-app"
obs_app_dir="$obs_dir/$obs_app"
obs_fixture_dir="$E2E_ROOT_DIR/e2e/fixtures/otel-app"
obs_app_port=8080
obs_app_host="${obs_app}.${E2E_PLATFORM_DOMAIN}"
obs_evidence="${E2E_OBS_EVIDENCE:-$E2E_ROOT_DIR/e2e/out/observability-evidence.md}"
obs_section="$obs_dir/section.md"
obs_results="$obs_dir/results"

obs_request_count=5
obs_route_timeout_seconds=180
obs_telemetry_timeout_seconds=240
obs_poll_interval_seconds=5
obs_query_window_seconds=3600
obs_output_rows=20
obs_body_chars=140
obs_proxy_limit=50
obs_curl_timeout_seconds=10
obs_http_ok=200
obs_http_forbidden=403
obs_trace_flags_sampled=01
obs_trace_id_bytes=16
obs_span_id_bytes=8
obs_marker_bytes=6

obs_second_workspace_id='00000000-0000-7000-8000-000000000013'
obs_second_workspace_name='e2e-observability-isolation'

obs_chart_values="$E2E_ROOT_DIR/charts/loco-obs/values.yaml"
obs_database=$(yq '.obsProxy.clickhouse.database' "$obs_chart_values")
obs_reader=$(yq '.obsProxy.clickhouse.readerUser' "$obs_chart_values")
obs_reader_password=$(yq ".clickhouseUserPasswords.${obs_reader}" "$E2E_OBS_VALUES")
obs_daemon_namespace=$(yq '.["otel-col-daemon"].namespaceOverride' "$obs_chart_values")
obs_fixture_scope=$(awk -F'"' '$1 ~ /^\ttracerName +=/ { print $2 }' "$obs_fixture_dir/main.go")
obs_forged_span=$(awk -F'"' '$1 ~ /^\tforgedSpanName +=/ { print $2 }' "$obs_fixture_dir/main.go")
obs_forged_workspace=$(awk -F'"' '$1 ~ /^\tforgedWorkspaceID +=/ { print $2 }' "$obs_fixture_dir/main.go")
obs_proxy_url="http://127.0.0.1:${E2E_OBS_PROXY_PORT}"
obs_query_logs="${obs_proxy_url}/loco.observability.v1.ObservabilityProxyService/QueryLogs"
obs_metric_tables="otel_metrics_sum otel_metrics_gauge otel_metrics_histogram"
obs_all_tables="otel_traces otel_logs otel_metrics_sum otel_metrics_gauge otel_metrics_histogram otel_metrics_exponential_histogram otel_metrics_summary"

obs_workspace_id=""
obs_environment_id=""
obs_resource_id=""
obs_trace_id=""
obs_client_span_id=""
obs_marker=""
obs_traffic_started=0
obs_deadline=0
obs_workspace_token=""
obs_second_token=""

obs_kube() {
    kubectl --context "$obs_ctx" "$@"
}

obs_ch() {
    local query=$1 format=$2 pod
    pod=$(obs_kube -n "$E2E_OBS_NAMESPACE" get pods -l clickhouse.altinity.com/chi -o jsonpath='{.items[0].metadata.name}')
    obs_kube -n "$E2E_OBS_NAMESPACE" exec "$pod" -c clickhouse -- clickhouse-client \
        --user "$obs_reader" --password "$obs_reader_password" --database "$obs_database" \
        --format "$format" -q "$query"
}

ev() {
    printf '%s\n' "$@" >>"$obs_section"
}

ev_query() {
    local title=$1 query=$2 output
    output=$(obs_ch "$query" Markdown 2>&1) || true
    ev "**${title}**" "" '```sql' "$query" '```' ""
    if [ -z "$output" ]; then
        ev "_no rows_" ""
        return 0
    fi
    ev "$(head -n "$((obs_output_rows + 2))" <<<"$output")" ""
}

ev_command() {
    local title=$1 command=$2 output=$3
    ev "**${title}**" "" '```console' "\$ ${command}" "$output" '```' ""
}

obs_random_hex() {
    openssl rand -hex "$1"
}

obs_now() {
    date -u +%s
}

obs_check() {
    local name=$1 predicate=$2 status=FAIL
    if [ "$obs_deadline" -eq 0 ]; then
        obs_deadline=$(($(obs_now) + obs_telemetry_timeout_seconds))
    fi
    while true; do
        : >"$obs_section"
        if "$predicate"; then
            status=PASS
            break
        fi
        if [ "$(obs_now)" -ge "$obs_deadline" ]; then
            break
        fi
        sleep "$obs_poll_interval_seconds"
    done
    {
        printf '## %s: %s\n\n' "$name" "$status"
        cat "$obs_section"
    } >>"$obs_evidence"
    printf '%s %s\n' "$name" "$status" >>"$obs_results"
    log_group "evidence: ${name}"
    sed 's/^/    /' "$obs_section"
    log_group_end
    if [ "$status" = PASS ]; then
        log_ok "PASS: ${name}"
        E2E_PASS=$((E2E_PASS + 1))
        return 0
    fi
    log_error "FAIL: ${name}"
    E2E_FAIL=$((E2E_FAIL + 1))
    return 1
}

obs_image() {
    local images
    local namespace=$1
    shift
    images=$(obs_kube -n "$namespace" get "$@" -o jsonpath='{.items[*].spec.template.spec.containers[*].image}' 2>/dev/null)
    echo "${images:-unavailable}"
}

obs_versions() {
    local commit branch kubernetes runtime clickhouse envoy_proxy gateway_api otel_sdk deps
    commit=$(git -C "$E2E_ROOT_DIR" rev-parse HEAD)
    branch=$(git -C "$E2E_ROOT_DIR" rev-parse --abbrev-ref HEAD)
    if [ -n "$(git -C "$E2E_ROOT_DIR" status --porcelain)" ]; then
        commit="${commit} (uncommitted changes)"
    fi
    kubernetes=$(obs_kube version -o json | yq -p json -r '.serverVersion.gitVersion')
    runtime=$(obs_kube get nodes -o jsonpath='{.items[0].status.nodeInfo.containerRuntimeVersion}')
    clickhouse=$(obs_ch "SELECT version()" TabSeparated 2>/dev/null || echo unavailable)
    envoy_proxy=$(obs_kube -n "$E2E_LOCO_NAMESPACE" get deployments \
        -l gateway.envoyproxy.io/owning-gateway-name=eg \
        -o jsonpath='{.items[0].spec.template.spec.containers[*].image}')
    gateway_api=$(awk '$1 == "sigs.k8s.io/gateway-api" { print $2 }' "$E2E_ROOT_DIR/controller/go.mod")
    otel_sdk=$(awk '$1 == "go.opentelemetry.io/otel/sdk" { print $2 }' "$obs_fixture_dir/go.mod")
    deps=$(yq -r '.dependencies[] | (.alias // .name) + " " + .version' "$E2E_ROOT_DIR/charts/loco-obs/Chart.yaml" |
        paste -sd ',' - | sed 's/,/, /g')
    printf '%s\n' \
        "| Component | Version |" \
        "| --- | --- |" \
        "| Commit | \`${commit}\` on \`${branch}\` |" \
        "| kind | $(kind version) |" \
        "| Kubernetes | ${kubernetes} (${runtime}) |" \
        "| Gateway API CRDs | ${gateway_api} |" \
        "| Envoy Gateway chart | $(yq '.dependencies[] | select(.name == "gateway-helm") | .version' "$E2E_ROOT_DIR/charts/loco-core/Chart.yaml") |" \
        "| Envoy Gateway controller | $(obs_image "$E2E_LOCO_NAMESPACE" deployments -l control-plane=envoy-gateway) |" \
        "| Envoy proxy | ${envoy_proxy} |" \
        "| loco-obs dependencies | ${deps} |" \
        "| ClickHouse server | ${clickhouse} |" \
        "| otel-col-deploy | $(obs_image "$E2E_OBS_NAMESPACE" deployments -l app.kubernetes.io/name=otel-col-deploy) |" \
        "| otel-col-daemon | $(obs_image "$obs_daemon_namespace" daemonsets -l app.kubernetes.io/name=otel-col-daemon) |" \
        "| loco-controller | $(obs_image "$E2E_LOCO_NAMESPACE" deployments -l app.kubernetes.io/name=loco-controller) |" \
        "| API, agent, CLI, obs-proxy | host binaries built from the commit, version ${E2E_VERSION} |" \
        "| Fixture OTel Go SDK | ${otel_sdk} |"
}

obs_resource_id_from_db() {
    e2e_psql "SELECT id FROM resources WHERE name = '${obs_app}'"
}

obs_application_field() {
    obs_kube -n "$E2E_LOCO_NAMESPACE" get application "resource-${obs_resource_id}" -o jsonpath="{.spec.$1}"
}

obs_seed_second_workspace() {
    e2e_psql "
        INSERT INTO workspaces (id, org_id, name, description, created_by)
        VALUES ('${obs_second_workspace_id}', '${cli_org_id}', '${obs_second_workspace_name}',
                'Second workspace for observability isolation', '${cli_user_id}')
        ON CONFLICT DO NOTHING;
        INSERT INTO user_scopes (user_id, scope, entity_type, entity_id)
        VALUES
            ('${cli_user_id}', 'read', 'workspace', '${obs_second_workspace_id}'),
            ('${cli_user_id}', 'write', 'workspace', '${obs_second_workspace_id}')
        ON CONFLICT DO NOTHING;
    " >/dev/null
}

obs_write_config() {
    cli_write_service_config "$obs_app_dir" "$obs_app" "$obs_app_port" /healthz
}

test_o01_deploys_the_fixture_through_loco() {
    rm -rf "$obs_dir"
    mkdir -p "$obs_app_dir" "$(dirname "$obs_evidence")"
    : >"$obs_results"
    {
        printf '# Observability evidence\n\n'
        printf 'Generated %s by `mise run e2e:observability` (run %s).\n\n' "$(utc_timestamp "$(obs_now)")" "$E2E_RUN_ID"
        printf '## Versions\n\n'
        obs_versions
        printf '\n'
    } >"$obs_evidence"

    cli_seed_session
    obs_seed_second_workspace
    cli_write_credentials
    cp -R "$obs_fixture_dir/." "$obs_app_dir/"
    obs_write_config

    local rc=0
    (cd "$obs_app_dir" && loco_cli deploy --yes) >"$obs_dir/deploy.out" 2>"$obs_dir/deploy.err" || rc=$?
    if ! assert "loco deploy built and deployed ${obs_app} from source (exit ${rc})" test "$rc" -eq 0; then
        sed 's/^/    /' "$obs_dir/deploy.out" "$obs_dir/deploy.err"
        return 1
    fi
    obs_resource_id=$(obs_resource_id_from_db)
    obs_workspace_id=$(obs_application_field workspaceId)
    obs_environment_id=$(obs_application_field environmentId)
    assert "The Application carries the workspace, environment and resource ids" \
        test -n "$obs_workspace_id" -a -n "$obs_environment_id" -a -n "$obs_resource_id"

    obs_workspace_token=$(mint_api_token "e2e-obs-$(obs_random_hex "$obs_marker_bytes")" \
        ENTITY_TYPE_WORKSPACE "$obs_workspace_id" SCOPE_READ)
    obs_second_token=$(mint_api_token "e2e-obs-$(obs_random_hex "$obs_marker_bytes")" \
        ENTITY_TYPE_WORKSPACE "$obs_second_workspace_id" SCOPE_READ)
    assert "TokenService/CreateToken minted a token for each workspace" \
        test -n "$obs_workspace_token" -a -n "$obs_second_token"

    {
        printf '## Deployed fixture\n\n'
        printf '| Field | Value |\n| --- | --- |\n'
        printf '| Resource | `%s` |\n' "$obs_app"
        printf '| Hostname | `%s` |\n' "$obs_app_host"
        printf '| WorkspaceId | `%s` |\n' "$obs_workspace_id"
        printf '| EnvironmentId | `%s` |\n' "$obs_environment_id"
        printf '| ResourceId | `%s` |\n' "$obs_resource_id"
        printf '| Second workspace | `%s` |\n\n' "$obs_second_workspace_id"
        printf '**Pod environment and labels**\n\n```console\n'
        obs_kube -n "ws-${obs_workspace_id}" get pods -l "loco.io/resource-id=${obs_resource_id}" \
            -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{.metadata.labels}{"\n"}{range .spec.containers[*].env[*]}{.name}={.value}{"\n"}{end}{end}' |
            grep -E '^(resource-|\{|OTEL_|LOCO_WORKSPACE)' || true
        printf '```\n\n'
    } >>"$obs_evidence"
}

obs_gateway_status() {
    curl -s -o /dev/null -w '%{http_code}' --max-time "$obs_curl_timeout_seconds" \
        -H "Host: ${obs_app_host}" "$@"
}

obs_route_serves() {
    test "$(obs_gateway_status "${E2E_GATEWAY_URL}/healthz")" = "$obs_http_ok"
}

test_o02_sends_traffic_through_the_gateway() {
    if [ -z "$obs_resource_id" ]; then
        log_error "  no deployed fixture; skipping traffic"
        obs_deadline=$(obs_now)
        return 1
    fi
    wait_for "the gateway to route ${obs_app_host}" "$obs_route_timeout_seconds" obs_route_serves
    obs_trace_id=$(obs_random_hex "$obs_trace_id_bytes")
    obs_client_span_id=$(obs_random_hex "$obs_span_id_bytes")
    obs_marker="obs-$(obs_random_hex "$obs_marker_bytes")"
    obs_traffic_started=$(obs_now)
    local traceparent="00-${obs_trace_id}-${obs_client_span_id}-${obs_trace_flags_sampled}"
    local i status url log="" failures=0
    for i in $(seq 1 "$obs_request_count"); do
        url="${E2E_GATEWAY_URL}/?e2e=${obs_marker}-${i}"
        if [ "$i" -eq 1 ]; then
            status=$(obs_gateway_status -H "traceparent: ${traceparent}" "$url")
            log+="curl -H 'Host: ${obs_app_host}' -H 'traceparent: ${traceparent}' '${url}' -> ${status}"$'\n'
        else
            status=$(obs_gateway_status "$url")
            log+="curl -H 'Host: ${obs_app_host}' '${url}' -> ${status}"$'\n'
        fi
        if [ "$status" != "$obs_http_ok" ]; then
            failures=$((failures + 1))
        fi
    done
    obs_deadline=$((obs_traffic_started + obs_telemetry_timeout_seconds))
    {
        printf '## Traffic\n\n'
        printf '%s requests through the Envoy gateway (port-forwarded to `%s`); the first carries a sampled traceparent with TraceId `%s` and client span `%s`. Telemetry is polled every %ss for up to %ss after the traffic.\n\n' \
            "$obs_request_count" "$E2E_GATEWAY_URL" "$obs_trace_id" "$obs_client_span_id" \
            "$obs_poll_interval_seconds" "$obs_telemetry_timeout_seconds"
        printf '```console\n%s```\n\n' "$log"
    } >>"$obs_evidence"
    assert "All ${obs_request_count} requests through the gateway returned ${obs_http_ok}" test "$failures" -eq 0
}

obs_latest_embedded_migration() {
    local file version latest=0
    for file in "$E2E_ROOT_DIR"/observability-proxy/migrations/*.sql; do
        version=$(basename "$file")
        version=$((10#${version%%_*}))
        if [ "$version" -gt "$latest" ]; then
            latest=$version
        fi
    done
    echo "$latest"
}

check_migrations() {
    local latest applied
    latest=$(obs_latest_embedded_migration)
    ev_query "Applied versions" "SELECT version_id, is_applied, tstamp FROM loco_schema_migrations ORDER BY version_id"
    applied=$(obs_ch "SELECT max(version_id) FROM loco_schema_migrations WHERE is_applied" TabSeparated) || return 1
    ev "Latest embedded migration: \`${latest}\`; latest applied: \`${applied}\`." ""
    test "$applied" = "$latest"
}

test_o03_migrations() {
    obs_check migrations check_migrations
}

check_ingress_trace() {
    local query rows
    query="SELECT Timestamp, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName, ScopeName, WorkspaceId, EnvironmentId, ResourceId FROM otel_traces WHERE TraceId = '${obs_trace_id}' ORDER BY Timestamp"
    ev "Expected WorkspaceId \`${obs_workspace_id}\`, ResourceId \`${obs_resource_id}\`; the client sent parent span \`${obs_client_span_id}\`; the app's spans have scope \`${obs_fixture_scope}\`." ""
    ev_query "Spans of the known trace" "$query"
    rows=$(obs_ch "SELECT SpanId, ParentSpanId, ScopeName, WorkspaceId, ResourceId FROM otel_traces WHERE TraceId = '${obs_trace_id}'" TabSeparated) || return 1
    awk -F'\t' -v scope="$obs_fixture_scope" -v ws="$obs_workspace_id" -v res="$obs_resource_id" '
        {
            id[NR] = $1; parent[NR] = $2; app[NR] = ($3 == scope); tenant[NR] = ($4 == ws && $5 == res)
        }
        END {
            for (i = 1; i <= NR; i++) {
                if (!app[i] || !tenant[i]) continue
                for (j = 1; j <= NR; j++) {
                    if (!app[j] && tenant[j] && id[j] == parent[i]) found = 1
                }
            }
            exit !found
        }' <<<"$rows"
}

test_o04_ingress_trace() {
    obs_check ingress_trace check_ingress_trace
}

check_app_logs() {
    local query rows
    query="SELECT Timestamp, WorkspaceId, EnvironmentId, ResourceId, TraceId, substring(Body, 1, ${obs_body_chars}) AS Body FROM otel_logs WHERE Body LIKE '%trace_id=${obs_trace_id}%' ORDER BY Timestamp LIMIT ${obs_output_rows}"
    ev "Expected WorkspaceId \`${obs_workspace_id}\`, EnvironmentId \`${obs_environment_id}\`, ResourceId \`${obs_resource_id}\`; the fixture logs \`trace_id=${obs_trace_id}\` for the traced request." ""
    ev_query "Fixture stdout lines carrying the known trace id" "$query"
    rows=$(obs_ch "SELECT WorkspaceId, EnvironmentId, ResourceId FROM otel_logs WHERE Body LIKE '%trace_id=${obs_trace_id}%'" TabSeparated) || return 1
    awk -F'\t' -v ws="$obs_workspace_id" -v env="$obs_environment_id" -v res="$obs_resource_id" '
        $1 == ws && $2 == env && $3 == res { found = 1 } END { exit !found }' <<<"$rows"
}

test_o05_app_logs() {
    obs_check app_logs check_app_logs
}

check_access_logs() {
    local query rows
    local match="(position(Body, 'e2e=${obs_marker}-') > 0 OR arrayExists(v -> position(v, 'e2e=${obs_marker}-') > 0, mapValues(LogAttributes)))"
    query="SELECT Timestamp, WorkspaceId, EnvironmentId, ResourceId, TraceId, ResourceAttributes['k8s.namespace.name'] AS Namespace, substring(concat(Body, ' ', toString(LogAttributes)), 1, ${obs_body_chars}) AS Record FROM otel_logs WHERE ${match} ORDER BY Timestamp LIMIT ${obs_output_rows}"
    ev "The request URLs carry \`e2e=${obs_marker}-<n>\`, which only the gateway's access log records, in its body or attributes (the fixture logs the path without the query). Expected WorkspaceId \`${obs_workspace_id}\`, ResourceId \`${obs_resource_id}\`." ""
    ev_query "Access log rows for the requests" "$query"
    rows=$(obs_ch "SELECT WorkspaceId, ResourceId FROM otel_logs WHERE ${match}" TabSeparated) || return 1
    awk -F'\t' -v ws="$obs_workspace_id" -v res="$obs_resource_id" '
        $1 == ws && $2 == res { found = 1 } END { exit !found }' <<<"$rows"
}

test_o06_access_logs() {
    obs_check access_logs check_access_logs
}

obs_metrics_union() {
    local columns=$1 table union=""
    for table in $obs_metric_tables; do
        if [ -n "$union" ]; then
            union+=" UNION ALL "
        fi
        union+="SELECT ${columns} FROM ${table} WHERE TimeUnix >= toDateTime(${obs_traffic_started})"
    done
    echo "$union"
}

check_envoy_metrics() {
    local query rows union route
    route="resource-${obs_resource_id}-route"
    union=$(obs_metrics_union "MetricName, WorkspaceId, ResourceId, Attributes")
    query="SELECT MetricName, WorkspaceId, ResourceId, count() AS Points FROM (${union}) WHERE ResourceId = '${obs_resource_id}' OR arrayExists(v -> position(v, '${route}') > 0, mapValues(Attributes)) GROUP BY MetricName, WorkspaceId, ResourceId ORDER BY MetricName LIMIT ${obs_output_rows}"
    ev "Metrics since the traffic that name the fixture's route \`${route}\` or carry its ResourceId. Expected a request metric (\`upstream_rq\`) with WorkspaceId \`${obs_workspace_id}\` and ResourceId \`${obs_resource_id}\`." ""
    ev_query "Envoy metrics for the fixture's route" "$query"
    rows=$(obs_ch "SELECT MetricName, WorkspaceId, ResourceId FROM (${union}) WHERE ResourceId = '${obs_resource_id}'" TabSeparated) || return 1
    awk -F'\t' -v ws="$obs_workspace_id" -v res="$obs_resource_id" '
        $1 ~ /upstream_rq/ && $2 == ws && $3 == res { found = 1 } END { exit !found }' <<<"$rows"
}

test_o07_envoy_metrics() {
    obs_check envoy_metrics check_envoy_metrics
}

check_forged_attribute() {
    local table counts="" forged landed
    for table in $obs_all_tables; do
        if [ -n "$counts" ]; then
            counts+=" UNION ALL "
        fi
        counts+="SELECT '${table}' AS Table, countIf(WorkspaceId = '${obs_forged_workspace}') AS ForgedRows FROM ${table}"
    done
    ev "The fixture exports span \`${obs_forged_span}\` with resource attribute \`loco.workspace.id=${obs_forged_workspace}\`. No row may keep the forged value; the span must land with WorkspaceId \`${obs_workspace_id}\`." ""
    ev_query "Rows with the forged WorkspaceId, per table" "$counts"
    ev_query "Where the forged span landed" "SELECT Timestamp, SpanName, ServiceName, WorkspaceId, EnvironmentId, ResourceId FROM otel_traces WHERE SpanName = '${obs_forged_span}' ORDER BY Timestamp LIMIT ${obs_output_rows}"
    forged=$(obs_ch "SELECT sum(ForgedRows) FROM (${counts})" TabSeparated) || return 1
    landed=$(obs_ch "SELECT countIf(WorkspaceId = '${obs_workspace_id}' AND ResourceId = '${obs_resource_id}'), count() FROM otel_traces WHERE SpanName = '${obs_forged_span}'" TabSeparated) || return 1
    local real total
    read -r real total <<<"$landed"
    ev "Forged rows: \`${forged}\`; forged spans with the real workspace and resource: \`${real}\` of \`${total}\`." ""
    test "$forged" -eq 0 && test "$total" -gt 0 && test "$real" -eq "$total"
}

test_o08_forged_attribute() {
    obs_check forged_attribute check_forged_attribute
}

obs_proxy_body() {
    local workspace_id=$1 resources=$2 start end
    start=$(utc_timestamp "$((obs_traffic_started - obs_query_window_seconds))")
    end=$(utc_timestamp "$(obs_now)")
    printf '{"workspaceId":"%s","resourceIds":[%s],"startTime":"%s","endTime":"%s","search":"trace_id=%s","limit":%d}' \
        "$workspace_id" "$resources" "$start" "$end" "$obs_trace_id" "$obs_proxy_limit"
}

obs_proxy_call() {
    local token=$1 body=$2
    curl -s -w '\n%{http_code}' --max-time "$obs_curl_timeout_seconds" -X POST "$obs_query_logs" \
        -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" -d "$body"
}

obs_entries_table() {
    local response=$1
    printf '| Timestamp | ResourceId | TraceId | Body |\n| --- | --- | --- | --- |\n'
    yq -p json -r '.entries // [] | .[] | "| " + .timestamp + " | " + (.resourceId // "") + " | " + (.traceId // "") + " | " + ((.body // "") | sub("\n"; " ")) + " |"' <<<"$response" |
        cut -c "1-$((obs_body_chars * 2))" | head -n "$obs_output_rows"
}

obs_proxy_evidence() {
    local title=$1 token_label=$2 body=$3 status=$4 response=$5
    ev_command "$title" \
        "curl -X POST ${obs_query_logs} -H 'Authorization: Bearer <${token_label}>' -d '${body}'" \
        "HTTP ${status}"$'\n'"$(head -c "$((obs_body_chars * 4))" <<<"$response")"
}

check_proxy_scoped() {
    local body output status response matches
    body=$(obs_proxy_body "$obs_workspace_id" "\"${obs_resource_id}\"")
    output=$(obs_proxy_call "$obs_workspace_token" "$body")
    status=$(tail -n 1 <<<"$output")
    response=$(sed '$d' <<<"$output")
    ev_command "QueryLogs with a token for workspace ${obs_workspace_id}" \
        "curl -X POST ${obs_query_logs} -H 'Authorization: Bearer <workspace token>' -d '${body}'" "HTTP ${status}"
    ev "$(obs_entries_table "$response")" ""
    test "$status" = "$obs_http_ok" || return 1
    matches=$(yq -p json -r "[.entries // [] | .[] | select(.resourceId == \"${obs_resource_id}\" and (.body | contains(\"trace_id=${obs_trace_id}\")))] | length" <<<"$response")
    ev "Entries for the fixture carrying the known trace id: \`${matches}\`." ""
    test "$matches" -gt 0
}

test_o09_proxy_scoped() {
    obs_check proxy_scoped check_proxy_scoped
}

check_proxy_isolation() {
    local body output status response leaked cross_status cross_response own_status
    body=$(obs_proxy_body "$obs_workspace_id" "\"${obs_resource_id}\"")
    output=$(obs_proxy_call "$obs_second_token" "$body")
    cross_status=$(tail -n 1 <<<"$output")
    cross_response=$(sed '$d' <<<"$output")
    obs_proxy_evidence "A token for the second workspace queries the first" "second workspace token" "$body" \
        "$cross_status" "$cross_response"

    body=$(obs_proxy_body "$obs_second_workspace_id" "")
    output=$(obs_proxy_call "$obs_workspace_token" "$body")
    own_status=$(tail -n 1 <<<"$output")
    obs_proxy_evidence "A token for the first workspace queries the second" "workspace token" "$body" \
        "$own_status" "$(sed '$d' <<<"$output")"

    output=$(obs_proxy_call "$obs_second_token" "$body")
    status=$(tail -n 1 <<<"$output")
    response=$(sed '$d' <<<"$output")
    ev_command "The second workspace queries itself for the first workspace's trace" \
        "curl -X POST ${obs_query_logs} -H 'Authorization: Bearer <second workspace token>' -d '${body}'" "HTTP ${status}"
    ev "$(obs_entries_table "$response")" ""
    leaked=$(yq -p json -r "[.entries // [] | .[] | select(.resourceId == \"${obs_resource_id}\" or (.body | contains(\"${obs_trace_id}\")))] | length" <<<"$response")
    ev "Cross-workspace status: \`${cross_status}\` and \`${own_status}\` (want PermissionDenied, HTTP ${obs_http_forbidden}); the second workspace's own query: HTTP \`${status}\` with \`${leaked}\` of the first workspace's rows." ""
    test "$cross_status" = "$obs_http_forbidden" &&
        grep -q permission_denied <<<"$cross_response" &&
        test "$own_status" = "$obs_http_forbidden" &&
        test "$status" = "$obs_http_ok" &&
        test "$leaked" -eq 0
}

test_o10_proxy_isolation() {
    obs_check proxy_isolation check_proxy_isolation
}

test_o11_writes_the_summary() {
    {
        printf '## Summary\n\n| Check | Result |\n| --- | --- |\n'
        awk '{ printf "| %s | %s |\n", $1, $2 }' "$obs_results"
        printf '\n'
    } >>"$obs_evidence"
    log_info "Evidence report: ${obs_evidence}"
    assert "The evidence report lists every check" test "$(wc -l <"$obs_results")" -gt 0
}
