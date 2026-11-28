# Sourced by the auth conformance tasks: runs tests/conformance/auth/compose.yaml as a compose
# project under a lease of the shared test-resource registry, on ports the lease reserves, and
# exports the providers' URLs. auth_stack_up starts the named services, or all of them. The stack
# is removed when the task exits.
root=$(git rev-parse --show-toplevel)
cd "$root"
source mise-tasks/test/lib/testres.sh loco-auth-conformance
compose=(docker compose -p "$testres_id" -f "$root/tests/conformance/auth/compose.yaml")
auth_stack_cleanup() {
	status=$?
	if [ "$status" -ne 0 ]; then
		"${compose[@]}" ps -a
		"${compose[@]}" logs --no-color --tail=80
	fi
	testres_release
}
trap auth_stack_cleanup EXIT
ports=$(testres_ports 5) || exit $?
read -r CONFORMANCE_POSTGRES_PORT CONFORMANCE_SUPABASE_PORT CONFORMANCE_KEYCLOAK_PORT \
	CONFORMANCE_DEX_PORT CONFORMANCE_ZITADEL_PORT <<<"$ports"
export CONFORMANCE_POSTGRES_PORT CONFORMANCE_SUPABASE_PORT CONFORMANCE_KEYCLOAK_PORT \
	CONFORMANCE_DEX_PORT CONFORMANCE_ZITADEL_PORT
export LOCO_CONFORMANCE_SUPABASE_URL="http://localhost:$CONFORMANCE_SUPABASE_PORT"
export LOCO_CONFORMANCE_KEYCLOAK_URL="http://localhost:$CONFORMANCE_KEYCLOAK_PORT"
export LOCO_CONFORMANCE_DEX_URL="http://localhost:$CONFORMANCE_DEX_PORT/dex"
export LOCO_CONFORMANCE_ZITADEL_URL="http://localhost:$CONFORMANCE_ZITADEL_PORT"
export LOCO_TEST_DATABASE_URL="postgres://loco_test:loco_test@localhost:$CONFORMANCE_POSTGRES_PORT/loco_test?sslmode=disable"
export CONFORMANCE_DEX_CONFIG=$testres_lease/dex.yaml
ISSUER=$LOCO_CONFORMANCE_DEX_URL yq '.issuer = strenv(ISSUER)' tests/conformance/auth/dex.yaml >"$CONFORMANCE_DEX_CONFIG"
export CONFORMANCE_ZITADEL_BOOTSTRAP=$testres_lease/zitadel
mkdir "$CONFORMANCE_ZITADEL_BOOTSTRAP"
chmod 777 "$CONFORMANCE_ZITADEL_BOOTSTRAP"
keys=$(go run ./tools/authkeys)
set -a
eval "$keys"
set +a

testres_add compose "$testres_id"

auth_stack_up() {
	"${compose[@]}" up -d --wait "$@"
	for _ in $(seq 1 60); do
		curl -sf -o /dev/null "$LOCO_CONFORMANCE_DEX_URL/.well-known/openid-configuration" && return
		sleep 1
	done
}
