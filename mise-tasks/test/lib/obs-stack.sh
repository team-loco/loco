root=$(git rev-parse --show-toplevel)
cd "$root"
chart=charts/loco-obs
control_plane_chart=charts/loco-control-plane
namespace=observability
control_plane_namespace=loco
control_plane_release=loco-control-plane
run_id=loco-obs-$1-$$
clickhouse=$run_id-clickhouse
workdir=$(mktemp -d)
ready_attempts=60
ports_per_run=4
source mise-tasks/test/lib/ports.sh
first_port=$(first_run_port "$ports_per_run")
query_port=$first_port
ingest_port=$((first_port + 1))
ingest_grpc_port=$((first_port + 2))
ingest_http_port=$((first_port + 3))
proxy_pids=()
failures=0
containers=("$clickhouse")

obs_cleanup() {
	if [ "${#proxy_pids[@]}" -gt 0 ]; then
		kill "${proxy_pids[@]}" >/dev/null 2>&1 || true
	fi
	docker rm -fv "${containers[@]}" >/dev/null 2>&1 || true
	rm -rf "$workdir"
}
trap obs_cleanup EXIT

fail() {
	echo "FAIL: $1"
	failures=$((failures + 1))
}

query() {
	docker exec "$clickhouse" clickhouse-client -q "$1"
}

wait_for() {
	local what=$1
	shift
	for _ in $(seq "$ready_attempts"); do
		"$@" >/dev/null 2>&1 && return 0
		sleep 1
	done
	echo "$what did not happen within ${ready_attempts}s"
	return 1
}

secret_value() {
	yq "select(.kind == \"Secret\" and .metadata.name == \"$1\") | .stringData.$2" <<<"$control_plane_manifest"
}

collector_config() {
	yq "select(.kind == \"ConfigMap\" and .metadata.name == \"$1\") | .data.relay" <<<"$manifest"
}

exporter_extensions() {
	yq '
		[.exporters[] | (.sending_queue.storage, .auth.authenticator) | select(. != null)] as $used |
		.extensions |= with_entries(select(.key as $name | $used | any_c(. == $name))) |
		.service.extensions = (.extensions | keys)'
}

password_sets=()
for user in $(yq '.clickhouse.clickhouse.users[].name' "$control_plane_chart/values.yaml"); do
	password_sets+=("--set" "clickhouseUserPasswords.$user=$(openssl rand -hex 16)")
done
region=obs-stack-region
ingest_endpoint=http://host.docker.internal:$ingest_http_port
manifest=$(helm template loco-obs "$chart" --namespace "$namespace" --set global.region="$region" \
	--set global.exporter.endpoint="$ingest_endpoint")
control_plane_manifest=$(helm template "$control_plane_release" "$control_plane_chart" \
	--namespace "$control_plane_namespace" --set obsProxy.image.tag="$1" \
	--set api.image.tag="$1" --set api.existingSecret=fixture-api-env --set ui.enabled=false --set certManager.enabled=false \
	--set clickhouse.enabled=true --set obsProxy.ingest.enabled=true "${password_sets[@]}")
chi=$(yq -o json -I 0 'select(.kind == "ClickHouseInstallation")' <<<"$control_plane_manifest")
clickhouse_image=$(yq '.spec.templates.podTemplates[0].spec.containers[0].image' <<<"$chi")
collector_image=$(yq 'select(.kind == "StatefulSet" and .metadata.name == "otel-col-deploy") | .spec.template.spec.containers[0].image' <<<"$manifest")
ingest_user=$(yq '.clickhouse.clickhouse.users[] | select(.grants[] | test("^GRANT INSERT")) | .name' "$control_plane_chart/values.yaml")
database=$(yq '.clickhouse.clickhouse.users[] | select(.grants[] | test("^GRANT INSERT")) | .grants[0] | sub("^.* ON ([^.]+)\..*$", "${1}")' "$control_plane_chart/values.yaml")
yq '.spec.configuration.files["config.d/extra_config.xml"]' <<<"$chi" >"$workdir/extra_config.xml"

users=$(yq '.spec.configuration.users | keys | .[] | split("/") | .[0]' <<<"$chi" | sort -u | grep -vx default)
{
	echo "<clickhouse><users>"
	for user in $users; do
		secret=$(yq ".spec.configuration.users[\"$user/password\"].valueFrom.secretKeyRef.name" <<<"$chi")
		echo "<$user>"
		echo "<password>$(secret_value "$secret" password)</password>"
		echo "<networks><ip>$(yq ".spec.configuration.users[\"$user/networks/ip\"]" <<<"$chi")</ip></networks>"
		echo "<profile>default</profile><quota>default</quota>"
		echo "<access_management>$(yq ".spec.configuration.users[\"$user/access_management\"]" <<<"$chi")</access_management>"
		echo "<grants>"
		yq ".spec.configuration.users[\"$user/grants/query\"][] | \"<query>\" + . + \"</query>\"" <<<"$chi"
		echo "</grants>"
		echo "</$user>"
	done
	echo "</users></clickhouse>"
} >"$workdir/loco_users.xml"

docker run -d --name "$clickhouse" -p 127.0.0.1::9000 \
	-e CLICKHOUSE_SKIP_USER_SETUP=1 \
	-v "$workdir/extra_config.xml:/etc/clickhouse-server/config.d/extra_config.xml:ro" \
	-v "$workdir/loco_users.xml:/etc/clickhouse-server/users.d/loco_users.xml:ro" \
	"$clickhouse_image" >/dev/null

if ! wait_for "ClickHouse start" query "SELECT 1"; then
	docker logs "$clickhouse" 2>&1 | tail -20
	exit 1
fi
query "SYSTEM FLUSH LOGS"
clickhouse_port=$(docker port "$clickhouse" 9000/tcp | head -1)

(cd observability-proxy && go build -o "$workdir/loco-obs-proxy" .)

start_proxy() {
	local role=$1 port=$2 env vars=()
	shift 2
	env=$(yq -o json -I 0 "select(.kind == \"Deployment\" and .metadata.name == \"$control_plane_release-obs-proxy-$role\") | .spec.template.spec.containers[0].env" <<<"$control_plane_manifest")
	for name in $(yq '.[] | select(has("value")) | .name' <<<"$env"); do
		vars+=("$name=$(NAME="$name" yq '.[] | select(.name == strenv(NAME)) | .value' <<<"$env")")
	done
	for name in $(yq '.[] | select(.valueFrom.secretKeyRef) | .name' <<<"$env"); do
		local secret key url
		secret=$(NAME="$name" yq '.[] | select(.name == strenv(NAME)) | .valueFrom.secretKeyRef.name' <<<"$env")
		key=$(NAME="$name" yq '.[] | select(.name == strenv(NAME)) | .valueFrom.secretKeyRef.key' <<<"$env")
		url=$(secret_value "$secret" "$key")
		vars+=("$name=${url%@*}@$clickhouse_port")
	done
	env "${vars[@]}" PORT="$port" "$@" "$workdir/loco-obs-proxy" >"$workdir/proxy-$role.log" 2>&1 &
	proxy_pids+=($!)
}

wait_for_proxy() {
	if ! wait_for "Observability proxy $1 readiness" curl -sf "http://127.0.0.1:$2/readyz"; then
		tail -20 "$workdir/proxy-$1.log"
		exit 1
	fi
}

start_proxy query "$query_port"
start_proxy ingest "$ingest_port" OTLP_GRPC_PORT="$ingest_grpc_port" OTLP_HTTP_PORT="$ingest_http_port"
wait_for_proxy query "$query_port"
wait_for_proxy ingest "$ingest_port"

start_collector() {
	local name=$run_id-$1 config=$2 mounts=()
	containers+=("$name")
	for directory in $(yq '.extensions | with_entries(select(.key | test("^file_storage"))) | .[].directory' "$config"); do
		mounts+=("--tmpfs" "$directory")
	done
	docker run -d --name "$name" -p 127.0.0.1::4318 --add-host host.docker.internal:host-gateway \
		"${mounts[@]}" -v "$config:/etc/collector.yaml:ro" \
		"$collector_image" --config /etc/collector.yaml >/dev/null
}

proxy_logs() {
	tail -20 "$workdir/proxy-ingest.log"
}

collector_logs() {
	docker logs "$run_id-$1" 2>&1 | tail -20
}

otlp_endpoint() {
	echo "http://$(docker port "$run_id-$1" 4318/tcp | head -1)"
}

send() {
	curl -sf -X POST -H 'Content-Type: application/json' "$1/v1/$2" -d "$3" >/dev/null
}

has_rows() {
	test "$(query "$1")" -gt 0
}
