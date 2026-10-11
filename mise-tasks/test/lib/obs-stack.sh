root=$(git rev-parse --show-toplevel)
cd "$root"
chart=charts/loco-obs
namespace=observability
run_id=loco-obs-$1-$$
network=$run_id
clickhouse=$run_id-clickhouse
workdir=$(mktemp -d)
ready_attempts=60
proxy_port=$((20000 + $$ % 10000))
proxy_pid=""
failures=0
containers=("$clickhouse")

obs_cleanup() {
	if [ -n "$proxy_pid" ]; then
		kill "$proxy_pid" >/dev/null 2>&1 || true
	fi
	docker rm -f "${containers[@]}" >/dev/null 2>&1 || true
	docker network rm "$network" >/dev/null 2>&1 || true
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

secret_password() {
	yq "select(.kind == \"Secret\" and .metadata.namespace == \"$namespace\" and .metadata.name == \"$1\") | .stringData.password" <<<"$manifest"
}

collector_config() {
	yq "select(.kind == \"ConfigMap\" and .metadata.name == \"$1\") | .data.relay" <<<"$manifest"
}

password_sets=()
for user in $(yq '.clickhouseUserPasswords | keys | .[]' "$chart/values.yaml"); do
	password_sets+=("--set" "clickhouseUserPasswords.$user=$(openssl rand -hex 16)")
done
manifest=$(helm template loco-obs "$chart" --namespace "$namespace" --set obsProxy.image.tag="$1" "${password_sets[@]}")
chi=$(yq -o json -I 0 'select(.kind == "ClickHouseInstallation")' <<<"$manifest")
clickhouse_image=$(yq '.spec.templates.podTemplates[0].spec.containers[0].image' <<<"$chi")
collector_image=$(yq 'select(.kind == "Deployment" and .metadata.name == "otel-col-deploy") | .spec.template.spec.containers[0].image' <<<"$manifest")
proxy_env=$(yq -o json -I 0 'select(.kind == "Deployment" and .metadata.name == "loco-obs-obs-proxy") | .spec.template.spec.containers[0].env' <<<"$manifest")
clickhouse_host=$(yq '.[] | select(.name == "CLICKHOUSE_MIGRATOR_URL") | .value | sub(".*@", "")' <<<"$proxy_env")
ingest_secret=$(yq 'select(.kind == "Deployment" and .metadata.name == "otel-col-deploy") | .spec.template.spec.containers[0].env[] | select(.name == "CLICKHOUSE_INGEST_PASSWORD") | .valueFrom.secretKeyRef.name' <<<"$manifest")
yq '.spec.configuration.files["config.d/extra_config.xml"]' <<<"$chi" >"$workdir/extra_config.xml"

users=$(yq '.spec.configuration.users | keys | .[] | split("/") | .[0]' <<<"$chi" | sort -u | grep -vx default)
{
	echo "<clickhouse><users>"
	for user in $users; do
		secret=$(yq ".spec.configuration.users[\"$user/password\"].valueFrom.secretKeyRef.name" <<<"$chi")
		echo "<$user>"
		echo "<password>$(secret_password "$secret")</password>"
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

docker network create "$network" >/dev/null
docker run -d --name "$clickhouse" --network "$network" -p 127.0.0.1::9000 \
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

proxy_value() {
	local item value secret
	item=$(NAME="$1" yq -o json -I 0 '.[] | select(.name == strenv(NAME))' <<<"$proxy_env")
	value=$(yq '.value // ""' <<<"$item")
	secret=$(yq '.valueFrom.secretKeyRef.name // ""' <<<"$item")
	if [ -n "$secret" ]; then
		value=$(secret_password "$secret")
	fi
	while [[ $value =~ \$\(([A-Z_]+)\) ]]; do
		value=${value//"${BASH_REMATCH[0]}"/$(proxy_value "${BASH_REMATCH[1]}")}
	done
	echo "${value//"$clickhouse_host"/$clickhouse_port}"
}

(cd observability-proxy && go build -o "$workdir/loco-obs-proxy" .)
proxy_vars=()
for name in $(yq '.[].name' <<<"$proxy_env"); do
	[ "$name" = PROXY_AUTH_TOKEN ] && continue
	[ "$name" = MIGRATION_LOCK ] && continue
	proxy_vars+=("$name=$(proxy_value "$name")")
done
proxy_vars+=("MIGRATION_LOCK=none")
env "${proxy_vars[@]}" PORT="$proxy_port" "$workdir/loco-obs-proxy" >"$workdir/proxy.log" 2>&1 &
proxy_pid=$!
if ! wait_for "Observability proxy readiness" curl -sf "http://127.0.0.1:$proxy_port/readyz"; then
	tail -20 "$workdir/proxy.log"
	exit 1
fi
kill "$proxy_pid"
proxy_pid=""
database=$(proxy_value CLICKHOUSE_DB)

clickhouse_exporter() {
	collector_config "$1" | yq -o json -I 0 '.exporters.clickhouse' |
		CLICKHOUSE_HOST="$clickhouse_host" CLICKHOUSE_CONTAINER="$clickhouse:9000" \
			yq -o json -I 0 '.endpoint |= sub(strenv(CLICKHOUSE_HOST), strenv(CLICKHOUSE_CONTAINER))'
}

start_collector() {
	local name=$run_id-$1 config=$2
	containers+=("$name")
	docker run -d --name "$name" --network "$network" -p 127.0.0.1::4318 \
		-e CLICKHOUSE_INGEST_PASSWORD="$(secret_password "$ingest_secret")" \
		-v "$config:/etc/collector.yaml:ro" \
		"$collector_image" --config /etc/collector.yaml >/dev/null
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
