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
obs_metric_interval_seconds=60
obs_trace_page_limit=1
obs_trace_max_pages=100
obs_curl_timeout_seconds=10
obs_event_flush_seconds=10
obs_ingest_replicas=2
obs_http_ok=200
obs_http_forbidden=403
obs_trace_flags_sampled=01
obs_no_recorded_value_mask=1
obs_trace_id_bytes=16
obs_span_id_bytes=8
obs_marker_bytes=6

obs_second_workspace_id='00000000-0000-7000-8000-000000000013'
obs_second_workspace_name='e2e-observability-isolation'

obs_chart_values="$E2E_ROOT_DIR/charts/loco-obs/values.yaml"
obs_database=$(yq '.["otel-col-daemon"].config.exporters.clickhouse.database' "$obs_chart_values")
obs_reader=$(yq '.clickhouse.clickhouse.users[] | select(.grants[] | test("^GRANT SELECT ON ")) | .name' "$obs_chart_values")
obs_reader_password=$(yq ".clickhouseUserPasswords.${obs_reader}" "$E2E_OBS_VALUES")
obs_daemon_namespace=$(yq '.["otel-col-daemon"].namespaceOverride' "$obs_chart_values")
obs_fixture_scope=$(awk -F'"' '$1 ~ /^\ttracerName +=/ { print $2 }' "$obs_fixture_dir/main.go")
obs_forged_span=$(awk -F'"' '$1 ~ /^\tforgedSpanName +=/ { print $2 }' "$obs_fixture_dir/main.go")
obs_forged_workspace=$(awk -F'"' '$1 ~ /^\tforgedWorkspaceID +=/ { print $2 }' "$obs_fixture_dir/main.go")
obs_forged_organization=$(awk -F'"' '$1 ~ /^\tforgedOrganizationID +=/ { print $2 }' "$obs_fixture_dir/main.go")
obs_proxy_url="$E2E_API_URL"
obs_query_logs="${obs_proxy_url}/loco.observability.v1.ObservabilityProxyService/QueryLogs"
obs_query_metrics="${obs_proxy_url}/loco.observability.v1.ObservabilityProxyService/QueryMetrics"
obs_query_traces="${obs_proxy_url}/loco.observability.v1.ObservabilityProxyService/QueryTraces"
obs_metric_tables="otel_metrics_sum otel_metrics_gauge otel_metrics_histogram"
obs_all_tables="otel_traces otel_logs otel_metrics_sum otel_metrics_gauge otel_metrics_histogram otel_metrics_exponential_histogram otel_metrics_summary"

obs_workspace_id=""
obs_organization_id=""
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
        "| otel-col-cluster | $(obs_image "$E2E_OBS_NAMESPACE" deployments -l app.kubernetes.io/name=otel-col-cluster) |" \
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
    obs_app="$obs_app" yq -i '.services[strenv(obs_app)].metrics = {"port": 9090, "path": "/metrics"}' "$obs_app_dir/loco.yaml"
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
    obs_organization_id=$(obs_application_field organizationId)
    assert "The Application carries the workspace, environment and resource ids" \
        test -n "$obs_workspace_id" -a -n "$obs_environment_id" -a -n "$obs_resource_id"
    assert "The Application carries the workspace's organization id ${cli_org_id}" \
        test "$obs_organization_id" = "$cli_org_id"

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
        printf '| OrganizationId | `%s` |\n' "$obs_organization_id"
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

test_o02_cluster_events_are_collected_once() {
    local marker event_time rows query
    obs_kube -n "$E2E_OBS_NAMESPACE" rollout status deployment/otel-col-deploy \
        --timeout="${obs_route_timeout_seconds}s" || return 1
    obs_kube -n "$E2E_OBS_NAMESPACE" rollout status deployment/otel-col-cluster \
        --timeout="${obs_route_timeout_seconds}s" || return 1
    rows=$(obs_kube -n "$E2E_OBS_NAMESPACE" get deployment otel-col-deploy \
        -o jsonpath='{.status.readyReplicas}')
    assert "OTLP ingestion has two ready replicas" test "$rows" -eq "$obs_ingest_replicas" || return 1
    marker="obs-event-$(obs_random_hex "$obs_marker_bytes")"
    event_time=$(utc_timestamp "$(obs_now)" | sed 's/Z$/.000000Z/')
    assert "The Kubernetes event is created" obs_kube -n "$E2E_OBS_NAMESPACE" create -f - <<JSON || return 1
{
  "apiVersion": "events.k8s.io/v1",
  "kind": "Event",
  "metadata": {"name": "${marker}", "namespace": "${E2E_OBS_NAMESPACE}"},
  "eventTime": "${event_time}",
  "reportingController": "loco.io/e2e",
  "reportingInstance": "observability",
  "action": "CheckCollector",
  "reason": "CollectorIsolation",
  "regarding": {"apiVersion": "apps/v1", "kind": "Deployment", "name": "otel-col-cluster", "namespace": "${E2E_OBS_NAMESPACE}"},
  "note": "${marker}",
  "type": "Normal"
}
JSON
    query="SELECT count() FROM otel_logs WHERE position(Body, '${marker}') > 0"
    assert "The Kubernetes event reaches ClickHouse" \
        wait_for "the Kubernetes event in ClickHouse" "$obs_telemetry_timeout_seconds" \
        obs_event_arrived "$query" || return 1
    sleep "$obs_event_flush_seconds"
    rows=$(obs_ch "$query" TabSeparated)
    : >"$obs_section"
    ev_query "Kubernetes event copies with two OTLP replicas" "$query"
    cat "$obs_section" >>"$obs_evidence"
    assert "The Kubernetes event is stored exactly once" test "$rows" -eq 1 || return 1
    obs_kube -n "$E2E_OBS_NAMESPACE" delete events.events.k8s.io "$marker"
}

obs_event_arrived() {
    test "$(obs_ch "$1" TabSeparated)" -gt 0
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

obs_trace_body() {
    local workspace=$1 start end
    start=$(utc_timestamp "$((obs_traffic_started - obs_query_window_seconds))")
    end=$(utc_timestamp "$(obs_now)")
    printf '{"workspaceId":"%s","startTime":"%s","endTime":"%s","traceId":"%s","limit":%d}' \
        "$workspace" "$start" "$end" "$obs_trace_id" "$obs_trace_page_limit"
}

obs_trace_call() {
    curl -s -w '\n%{http_code}' --max-time "$obs_curl_timeout_seconds" -X POST "$obs_query_traces" \
        -H 'Content-Type: application/json' -H "Authorization: Bearer $1" -d "$2"
}

check_ingress_trace() {
    local body output status response cursor="" previous="" rows="" page complete=false
    body=$(obs_trace_body "$obs_workspace_id")
    for ((page=0; page<obs_trace_max_pages; page++)); do
        output=$(obs_trace_call "$obs_workspace_token" "$body") || return 1
        status=$(tail -n 1 <<<"$output")
        response=$(sed '$d' <<<"$output")
        ev_command "QueryTraces page $((page+1)) for the known trace" \
            "curl -X POST ${obs_query_traces} -H 'Authorization: Bearer <workspace token>' -d '${body}'" \
            "HTTP ${status}"$'\n'"${response}"
        test "$status" = "$obs_http_ok" || return 1
        rows+=$(yq -p json -r '.spans // [] | .[] | [.spanId, (.parentSpanId // ""), .scopeName, .workspaceId, .resourceId, (.resourceAttributes."loco.organization.id" // ""), .traceId] | @tsv' <<<"$response")$'\n'
        cursor=$(yq -p json -r '.nextCursor // ""' <<<"$response")
        if [ -z "$cursor" ]; then
            complete=true
            break
        fi
        test "$cursor" != "$previous" || return 1
        previous=$cursor
        body=$(CURSOR="$cursor" yq -p json -o json -I 0 '.cursor = strenv(CURSOR)' <<<"$body")
    done
    test "$complete" = true || return 1
    awk -F'\t' -v scope="$obs_fixture_scope" -v ws="$obs_workspace_id" -v res="$obs_resource_id" -v org="$obs_organization_id" -v trace="$obs_trace_id" '
        NF {
            if ($4 != ws || $7 != trace) invalid = 1
            id[NR] = $1; parent[NR] = $2; app[NR] = ($3 == scope && $6 == org); tenant[NR] = ($4 == ws && $5 == res)
        }
        END {
            for (i = 1; i <= NR; i++) {
                if (!app[i] || !tenant[i]) continue
                for (j = 1; j <= NR; j++) {
                    if (!app[j] && tenant[j] && id[j] == parent[i]) found = 1
                }
            }
            exit (invalid || !found)
        }' <<<"$rows"
}

test_o04_ingress_trace() {
    obs_check ingress_trace check_ingress_trace
}

check_app_logs() {
    local query rows
    query="SELECT Timestamp, OrganizationId, WorkspaceId, EnvironmentId, ResourceId, TraceId, substring(Body, 1, ${obs_body_chars}) AS Body FROM otel_logs WHERE Body LIKE '%trace_id=${obs_trace_id}%' ORDER BY Timestamp LIMIT ${obs_output_rows}"
    ev "Expected OrganizationId \`${obs_organization_id}\`, WorkspaceId \`${obs_workspace_id}\`, EnvironmentId \`${obs_environment_id}\`, ResourceId \`${obs_resource_id}\`; the fixture logs \`trace_id=${obs_trace_id}\` for the traced request." ""
    ev_query "Fixture stdout lines carrying the known trace id" "$query"
    rows=$(obs_ch "SELECT WorkspaceId, EnvironmentId, ResourceId, OrganizationId FROM otel_logs WHERE Body LIKE '%trace_id=${obs_trace_id}%'" TabSeparated) || return 1
    awk -F'\t' -v ws="$obs_workspace_id" -v env="$obs_environment_id" -v res="$obs_resource_id" -v org="$obs_organization_id" '
        $1 == ws && $2 == env && $3 == res && $4 == org { found = 1 } END { exit !found }' <<<"$rows"
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
        counts+="SELECT '${table}' AS Table, countIf(WorkspaceId = '${obs_forged_workspace}' OR OrganizationId = '${obs_forged_organization}') AS ForgedRows FROM ${table}"
    done
    ev "The fixture exports span \`${obs_forged_span}\` with resource attributes \`loco.workspace.id=${obs_forged_workspace}\` and \`loco.organization.id=${obs_forged_organization}\`. No row may keep either forged value; the span must land with OrganizationId \`${obs_organization_id}\` and WorkspaceId \`${obs_workspace_id}\`." ""
    ev_query "Rows with a forged OrganizationId or WorkspaceId, per table" "$counts"
    ev_query "Where the forged span landed" "SELECT Timestamp, SpanName, ServiceName, OrganizationId, WorkspaceId, EnvironmentId, ResourceId FROM otel_traces WHERE SpanName = '${obs_forged_span}' ORDER BY Timestamp LIMIT ${obs_output_rows}"
    forged=$(obs_ch "SELECT sum(ForgedRows) FROM (${counts})" TabSeparated) || return 1
    landed=$(obs_ch "SELECT countIf(OrganizationId = '${obs_organization_id}' AND WorkspaceId = '${obs_workspace_id}' AND ResourceId = '${obs_resource_id}'), count() FROM otel_traces WHERE SpanName = '${obs_forged_span}'" TabSeparated) || return 1
    local real total
    read -r real total <<<"$landed"
    ev "Forged rows: \`${forged}\`; forged spans with the real organization, workspace and resource: \`${real}\` of \`${total}\`." ""
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
    local body output status response matches region
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
    test "$matches" -gt 0 || return 1
    region=$(yq -p json -r '.entries[0].resourceAttributes."loco.region" // ""' <<<"$response")
    test -n "$region" || return 1
    body=$(REGION="$region" yq -p json -o json -I 0 '.regions = [strenv(REGION)]' <<<"$body")
    output=$(obs_proxy_call "$obs_workspace_token" "$body") || return 1
    test "$(tail -n 1 <<<"$output")" = "$obs_http_ok" || return 1
    response=$(sed '$d' <<<"$output")
    matches=$(REGION="$region" yq -p json -r '[.entries // [] | .[] | select(.resourceAttributes."loco.region" == strenv(REGION))] | length' <<<"$response")
    test "$matches" -gt 0 || return 1
    body=$(REGION="$region" yq -p json -o json -I 0 '.excludedRegions = [strenv(REGION)]' <<<"$body")
    output=$(obs_proxy_call "$obs_workspace_token" "$body") || return 1
    test "$(tail -n 1 <<<"$output")" = "$obs_http_ok" || return 1
    response=$(sed '$d' <<<"$output")
    matches=$(yq -p json -r '(.entries // []) | length' <<<"$response")
    ev "Recorded region inclusion returned fixture logs; excluding the same region returned ${matches} rows through the control-plane address." ""
    test "$matches" -eq 0
}

test_o09_proxy_scoped() {
    obs_check proxy_scoped check_proxy_scoped
}

check_proxy_counter() {
    local metric start end body output status response matches
    metric=$(obs_ch "SELECT MetricName FROM otel_metrics_sum WHERE WorkspaceId = '${obs_workspace_id}' AND ResourceId = '${obs_resource_id}' AND TimeUnix >= toDateTime(${obs_traffic_started}) AND Value > 0 LIMIT 1" TabSeparated) || return 1
    test -n "$metric" || return 1
    start=$(utc_timestamp "$(($(date +%s) - obs_query_window_seconds))")
    end=$(utc_timestamp "$(date +%s)")
    body=$(printf '{"workspaceId":"%s","resourceIds":["%s"],"metricName":"%s","startTime":"%s","endTime":"%s","intervalSeconds":%d,"aggregation":"max"}' \
        "$obs_workspace_id" "$obs_resource_id" "$metric" "$start" "$end" "$obs_metric_interval_seconds")
    output=$(curl -s -w '\n%{http_code}' --max-time "$obs_curl_timeout_seconds" -X POST "$obs_query_metrics" \
        -H "Authorization: Bearer ${obs_workspace_token}" -H 'Content-Type: application/json' -d "$body") || return 1
    status=$(tail -n 1 <<<"$output")
    response=$(sed '$d' <<<"$output")
    test "$status" = "$obs_http_ok" || return 1
    matches=$(yq -p json -r '[.series // [] | .[] | select(.labels.metric_type == "sum") | .points[] | select(.value > 0)] | length' <<<"$response")
    ev "QueryMetrics returned HTTP ${status} and ${matches} positive Sum points for ${metric}." ""
    test "$matches" -gt 0 || return 1
    output=$(curl -s -w '\n%{http_code}' --max-time "$obs_curl_timeout_seconds" -X POST "$obs_query_metrics" \
        -H "Authorization: Bearer ${obs_second_token}" -H 'Content-Type: application/json' -d "$body") || return 1
    status=$(tail -n 1 <<<"$output")
    test "$status" = "$obs_http_forbidden"
}

test_o09_proxy_counter() {
    obs_check proxy_counter check_proxy_counter
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

check_trace_proxy_isolation() {
    local body output status response count
    body=$(obs_trace_body "$obs_workspace_id")
    output=$(obs_trace_call "$obs_second_token" "$body")
    status=$(tail -n 1 <<<"$output")
    test "$status" = "$obs_http_forbidden" || return 1
    body=$(obs_trace_body "$obs_second_workspace_id")
    output=$(obs_trace_call "$obs_workspace_token" "$body")
    status=$(tail -n 1 <<<"$output")
    test "$status" = "$obs_http_forbidden" || return 1
    output=$(obs_trace_call "$obs_second_token" "$body")
    status=$(tail -n 1 <<<"$output")
    response=$(sed '$d' <<<"$output")
    count=$(yq -p json -r '.spans // [] | length' <<<"$response")
    ev "QueryTraces: both cross-workspace requests returned HTTP ${obs_http_forbidden}; second workspace querying the known trace returned HTTP ${status}, spans ${count}." ""
    test "$status" = "$obs_http_ok" && test "$count" -eq 0
}

test_o10_proxy_isolation() {
    obs_check proxy_isolation check_proxy_isolation
    obs_check trace_proxy_isolation check_trace_proxy_isolation
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

check_pulled_application_metrics() {
    local output status response body start end matches
    start=$(utc_timestamp "$(($(date +%s) - obs_query_window_seconds))")
    end=$(utc_timestamp "$(date +%s)")
    body=$(printf '{"workspaceId":"%s","resourceIds":["%s"],"metricName":"loco_fixture_temperature","startTime":"%s","endTime":"%s","intervalSeconds":%d,"aggregation":"max"}' \
        "$obs_workspace_id" "$obs_resource_id" "$start" "$end" "$obs_metric_interval_seconds")
    output=$(curl -s -w '\n%{http_code}' --max-time "$obs_curl_timeout_seconds" -X POST "$obs_query_metrics" \
        -H "Authorization: Bearer $obs_workspace_token" -H 'Content-Type: application/json' -d "$body") || return 1
    status=$(tail -n1 <<<"$output")
    response=$(sed '$d' <<<"$output")
    [ "$status" = "$obs_http_ok" ] || return 1
    matches=$(yq -p json -r '[.series // [] | .[] | .points[] | select(.value == 42)] | length' <<<"$response")
    test "$matches" -gt 0 || return 1
    ev "QueryMetrics returned ${matches} gauge points pulled by the node collector for the fixture resource." ""
    output=$(curl -s -w '\n%{http_code}' --max-time "$obs_curl_timeout_seconds" -X POST "$obs_query_metrics" \
        -H "Authorization: Bearer $obs_second_token" -H 'Content-Type: application/json' -d "$body") || return 1
    status=$(tail -n1 <<<"$output")
    test "$status" = "$obs_http_forbidden"
}

test_o09_pulls_application_metrics_through_otel() {
    obs_check pulled_application_metrics check_pulled_application_metrics
}

obs_metrics_discovery_removed() {
    local pods count
    pods=$(obs_kube -n "ws-${obs_workspace_id}" get pods -l "loco.io/resource-id=${obs_resource_id}" -o json) || return 1
    count=$(yq -p json -r '[.items[] | select(.metadata.deletionTimestamp == null) | select(.metadata.labels."loco.io/metrics-port" != null)] | length' <<<"$pods")
    test "$count" -eq 0
}

obs_metrics_quiet_window_elapsed() {
    test "$(obs_now)" -ge "$obs_metrics_quiet_end"
}

obs_metrics_endpoint_serves() {
    test "$(obs_gateway_status "${E2E_GATEWAY_URL}/metrics")" = "$obs_http_ok"
}

obs_metric_sample_counts() {
    local relation="$1" condition="$2"
    obs_ch "SELECT countIf(bitAnd(Flags, ${obs_no_recorded_value_mask}) = 0), countIf(bitAnd(Flags, ${obs_no_recorded_value_mask}) != 0) FROM ${relation} WHERE ${condition}" TabSeparated
}

test_o10_stops_pulling_when_the_endpoint_is_removed() {
    local rc=0 since interval rows markers counts
    counts=$(obs_metric_sample_counts "values('Flags UInt32, Value Float64', (0, 42), (0, 0), (1, 0), (2, 42), (3, 0))" "1") || return 1
    IFS=$'\t' read -r rows markers <<<"$counts"
    assert "metric sample counting includes recorded zero and excludes only no-recorded-value flags" test "$rows" -eq 3
    assert "metric sample counting identifies staleness markers separately" test "$markers" -eq 2
    obs_app="$obs_app" yq -i 'del(.services[strenv(obs_app)].metrics)' "$obs_app_dir/loco.yaml"
    (cd "$obs_app_dir" && loco_cli infra apply --yes) >"$obs_dir/metrics-remove.out" 2>"$obs_dir/metrics-remove.err" || rc=$?
    if ! assert "removing metrics applies without a source build" test "$rc" -eq 0; then
        cat "$obs_dir/metrics-remove.out" "$obs_dir/metrics-remove.err"
        return 1
    fi
    obs_kube -n "ws-${obs_workspace_id}" rollout status "deployment/resource-${obs_resource_id}" --timeout="${obs_route_timeout_seconds}s" || return 1
    assert "the replacement pod has no metrics discovery label" \
        wait_for "metrics discovery removal" "$obs_route_timeout_seconds" obs_metrics_discovery_removed
    assert "the undeclared metrics endpoint still serves HTTP" \
        wait_for "the gateway to route the replacement pod's metrics endpoint" \
            "$obs_route_timeout_seconds" obs_metrics_endpoint_serves
    since=$(($(obs_now) + obs_poll_interval_seconds))
    interval=$(yq '.["otel-col-daemon"].config.receivers."prometheus/applications".config.scrape_configs[0].scrape_interval' "$obs_chart_values")
    interval=${interval%s}
    obs_metrics_quiet_end=$((since + interval * 2))
    wait_for "two scrape intervals without a declared endpoint" "$obs_telemetry_timeout_seconds" obs_metrics_quiet_window_elapsed || return 1
    counts=$(obs_metric_sample_counts otel_metrics_gauge "WorkspaceId = '${obs_workspace_id}' AND ResourceId = '${obs_resource_id}' AND MetricName = 'loco_fixture_temperature' AND TimeUnix >= toDateTime(${since})") || return 1
    IFS=$'\t' read -r rows markers <<<"$counts"
    log_info "After discovery removal: ${rows} recorded samples and ${markers} no-recorded-value markers since ${since}."
    assert "a pod without the discovery label is not scraped" test "$rows" -eq 0
}
