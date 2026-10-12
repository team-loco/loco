#!/usr/bin/env bash

source "$E2E_ROOT_DIR/e2e/cli.sh"

inventory_dir="$E2E_BUILD_WORK_DIR/inventory"
cli_home="$inventory_dir/home"
inventory_app=e2e-inventory
inventory_app_dir="$inventory_dir/$inventory_app"
inventory_timeout=180
inventory_app_port=8080
inventory_placement_id=""
inventory_first_seen=""
inventory_previous_reports=0

inventory_recorded() {
    test "$(e2e_psql "SELECT count(*) FROM cluster_unknown_placements
        WHERE placement_id = '${inventory_placement_id}'")" = 1
}

inventory_reconciled() {
    local reports
    reports=$(grep -c 'inventory reconciled' "$LOG_DIR/api.log" || true)
    test "${reports:-0}" -gt "$inventory_previous_reports"
}

inventory_restart_agent() {
    inventory_previous_reports=$(grep -c 'inventory reconciled' "$LOG_DIR/api.log" || true)
    kill_pid_file "$PID_DIR/agent.pid"
    start_agent
    wait_for "the restarted agent inventory to be reconciled" "$inventory_timeout" inventory_reconciled
}

test_i01_deploy_workload() {
    cli_seed_session
    mkdir -p "$inventory_app_dir"
    cli_write_credentials
    cli_write_image_config "$inventory_app_dir" "$inventory_app" "$E2E_PUBLIC_IMAGE" "$inventory_app_port" "[]"
    local rc=0
    (cd "$inventory_app_dir" && loco_cli deploy --yes) >"$inventory_dir/deploy.out" 2>"$inventory_dir/deploy.err" || rc=$?
    assert "The inventory fixture deploys successfully" test "$rc" -eq 0
    wait_for "the inventory fixture to become ready" "$inventory_timeout" cli_application_is_ready "$inventory_app"
    inventory_placement_id=$(e2e_psql "SELECT p.id FROM placements p JOIN resources r ON r.id = p.resource_id
        WHERE r.workspace_id = '${cli_workspace_id}' AND r.name = '${inventory_app}'")
    assert "The fixture has a placement" test -n "$inventory_placement_id"
}

test_i02_report_orphan_without_removing_it() {
    e2e_psql "DELETE FROM placements WHERE id = '${inventory_placement_id}'" >/dev/null
    inventory_restart_agent
    wait_for "the orphan placement to be recorded" "$inventory_timeout" inventory_recorded
    inventory_first_seen=$(e2e_psql "SELECT first_seen_at FROM cluster_unknown_placements
        WHERE placement_id = '${inventory_placement_id}'")
    assert "The first observation has a timestamp" test -n "$inventory_first_seen"
    assert "The orphaned Application stays ready" cli_application_is_ready "$inventory_app"
    local reports
    reports=$(e2e_psql "SELECT count(*) FROM events WHERE type = 'cluster.unknown_placements_reported'
        AND data->'placements' @> '[{\"placementId\": \"${inventory_placement_id}\"}]'::jsonb")
    assert "Operators receive one report containing the placement ID" test "$reports" = 1
}

test_i03_reconnect_preserves_first_observation() {
    inventory_restart_agent
    wait_for "the Application to remain ready after reconnect" "$inventory_timeout" cli_application_is_ready "$inventory_app"
    local first_seen reports
    first_seen=$(e2e_psql "SELECT first_seen_at FROM cluster_unknown_placements
        WHERE placement_id = '${inventory_placement_id}'")
    reports=$(e2e_psql "SELECT count(*) FROM events WHERE type = 'cluster.unknown_placements_reported'
        AND data->'placements' @> '[{\"placementId\": \"${inventory_placement_id}\"}]'::jsonb")
    assert "Reconnect keeps the original first-seen timestamp" test "$first_seen" = "$inventory_first_seen"
    assert "Reconnect does not duplicate the operator report" test "$reports" = 1
}
