# Sourced by test tasks: unless LOCO_TEST_CLICKHOUSE_URL names a running server, start the
# ClickHouse image and Keeper config the loco-control-plane chart pins under a lease of the shared
# test-resource registry, export its URL, and remove the container when the task exits.
if [ -z "${LOCO_TEST_CLICKHOUSE_URL:-}" ]; then
	clickhouse_root=$(git rev-parse --show-toplevel)
	source "$clickhouse_root/mise-tasks/test/lib/testres.sh" loco-clickhouse-test
	clickhouse_values="$clickhouse_root/charts/loco-control-plane/values.yaml"
	clickhouse_image=$(yq '.clickhouse.clickhouse.image.repository + ":" + .clickhouse.clickhouse.image.tag' "$clickhouse_values")
	clickhouse_container=$testres_id
	clickhouse_keeper_config=$testres_lease/keeper.xml
	trap testres_release EXIT
	printf '<clickhouse>%s</clickhouse>\n' "$(yq '.clickhouse.clickhouse.keeperConfig' "$clickhouse_values")" >"$clickhouse_keeper_config"
	chmod a+r "$clickhouse_keeper_config"
	go -C "$clickhouse_root" run ./tools/pullimage "$clickhouse_image"
	testres_add container "$clickhouse_container"
	docker run -d --name "$clickhouse_container" -p 127.0.0.1::9000 \
		-e CLICKHOUSE_USER=loco_test -e CLICKHOUSE_PASSWORD=loco_test \
		-e CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 \
		-v "$clickhouse_keeper_config:/etc/clickhouse-server/config.d/keeper.xml:ro" \
		"$clickhouse_image" >/dev/null
	for _ in $(seq 60); do
		docker exec "$clickhouse_container" clickhouse-client --user loco_test --password loco_test -q "SELECT 1" >/dev/null 2>&1 && break
		sleep 1
	done
	docker exec "$clickhouse_container" clickhouse-client --user loco_test --password loco_test -q "SELECT 1" >/dev/null
	export LOCO_TEST_CLICKHOUSE_URL="clickhouse://loco_test:loco_test@$(docker port "$clickhouse_container" 9000/tcp | head -1)"
fi
