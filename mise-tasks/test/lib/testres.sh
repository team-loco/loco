# Sourced by test tasks: takes a lease on the shared test-resource registry, owned by this task's
# shell, and releases it with every resource recorded under it when the task exits. A killed task
# leaves the lease to the next harness's reap. Name resources after $testres_id and record each
# with testres_add before creating it; testres_ports N prints N reserved ports on one line.
testres_root=$(git rev-parse --show-toplevel)
testres() {
	go -C "$testres_root" run ./tools/testres "$@"
}
testres_lease=$(testres lease -name "$1" -pid $$)
testres_id=${testres_lease##*/}
testres_add() {
	testres add "$testres_lease" "$@"
}
testres_ports() {
	testres ports "$testres_lease" "$1"
}
testres_release() {
	testres release "$testres_lease"
}
