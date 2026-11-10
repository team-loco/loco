import os
import shlex
import subprocess
from pathlib import Path

import pytest

LIB = Path(__file__).resolve().parent / "lib.sh"


def selected(name, *filters):
    result = subprocess.run(
        ["bash", "-c", 'source "$1"; shift; suite_selected "$@"', "suite_selected", str(LIB), name, *filters],
        check=False,
    )
    return result.returncode == 0


def test_no_filter_selects_every_suite():
    assert selected("03_builds")


@pytest.mark.parametrize(
    ("name", "filters", "expected"),
    [
        ("03_builds", ["build"], True),
        ("06_builds_disabled", ["build"], True),
        ("06_builds_disabled", ["03_builds"], False),
        ("04_build_dispatch", ["04_build_dispatch", "05_cli_build_deploy"], True),
        ("05_cli_build_deploy", ["04_build_dispatch", "05_cli_build_deploy"], True),
        ("03_builds", ["04_build_dispatch", "05_cli_build_deploy"], False),
    ],
)
def test_filters_select_suites_whose_name_contains_any(name, filters, expected):
    assert selected(name, *filters) is expected


RUNNER = Path(__file__).resolve().parent / "run.sh"


def run_harness_functions(body, *arguments, env=None):
    # Load the real allocation and functions without executing infrastructure setup.
    source, _ = RUNNER.read_text().rsplit('\nif [ "$BUILD_ONLY" = true ]; then', 1)
    return subprocess.run(
        ["bash", "-c", source + "\n" + body, str(RUNNER), *arguments],
        check=False,
        capture_output=True,
        text=True,
        env=env,
    )


def test_listener_ports_are_distinct_within_the_run_block():
    result = run_harness_functions(
        '''printf '%s\\n' "$API_PORT" "$API_REPLICA_PORT" "$AUTH_PORT" "$OBS_PROXY_PORT" \\
            "$OTLP_GRPC_PORT" "$OTLP_HTTP_PORT" "$GATEWAY_PORT" "$CLICKHOUSE_PORT" \\
            "$S3_PORT" "$REGISTRY_HOST_PORT"'''
    )
    assert result.returncode == 0, result.stderr
    ports = [int(value) for value in result.stdout.splitlines()]
    assert len(set(ports)) == len(ports)
    assert max(ports) - min(ports) == len(ports) - 1


def test_port_check_accepts_the_runs_retained_identity_provider():
    result = run_harness_functions(
        '''
        log_step() { :; }
        log_ok() { :; }
        e2e_compose() { return 0; }
        docker() { return 0; }
        require_free_port() { test "$1" != "$AUTH_PORT"; }
        check_ports
        '''
    )
    assert result.returncode == 0, result.stderr


@pytest.mark.parametrize(("failed_downloads", "succeeds"), [(1, True), (2, False)])
def test_postgres_setup_survives_one_failed_image_download(failed_downloads, succeeds):
    # Both pull and up download a missing image; each failure drops one download.
    result = run_harness_functions(
        '''
        failures=$FAILED_DOWNLOADS
        pulled=false
        sleep() { :; }
        e2e_compose() {
            case $1 in pull | up) ;; *) return 0 ;; esac
            if [ "$pulled" = false ]; then
                if [ "$failures" -gt 0 ]; then
                    failures=$((failures - 1))
                    return 1
                fi
                pulled=true
            fi
        }
        compose_host_port() { echo 5432; }
        wait_for() { :; }
        setup_postgres
        ''',
        env=os.environ | {"FAILED_DOWNLOADS": str(failed_downloads)},
    )
    assert (result.returncode == 0) is succeeds, result.stdout + result.stderr


def run_fixture_suites(tmp_path, files, *filters):
    for name, content in files.items():
        path = tmp_path / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
    # Enter main through the normal selection and teardown path. Replace its
    # first infrastructure call with the runner itself.
    return run_harness_functions(
        '''
        SCRIPT_DIR=$FIXTURE_ROOT
        LOG_DIR=$FIXTURE_ROOT
        stop_jobs() { :; }
        dump_logs() { :; }
        teardown() { echo "teardown ran"; }
        check_prerequisites() {
            echo "provisioning started"
            run_tests
            print_summary
            exit
        }
        main
        ''',
        *filters,
        env=os.environ | {"FIXTURE_ROOT": str(tmp_path)},
    )


@pytest.mark.parametrize("filters", [("missing",), ("example", "missing"), ("",)])
def test_unmatched_filter_fails_before_provisioning(tmp_path, filters):
    result = run_fixture_suites(tmp_path, {"suites/01_example.sh": "test_example() { :; }"}, *filters)
    assert result.returncode != 0
    assert "suite filter" in result.stdout
    assert "provisioning started" not in result.stdout
    assert "teardown ran" not in result.stdout


def test_empty_suite_directory_fails_before_provisioning(tmp_path):
    result = run_fixture_suites(tmp_path, {})
    assert result.returncode != 0
    assert "No e2e suites found" in result.stdout
    assert "provisioning started" not in result.stdout


@pytest.mark.parametrize(
    ("commands", "passed"),
    [
        ("assert 'setup passed' true; false; echo 'continued after failure'", 1),
        ("return 42", 0),
        ("assert 'fixture assertion' false; echo 'continued after failure'", 0),
        ("assert 'guarded assertion' false || :; true", 0),
        ("assert 'guarded assertion' false || :; return 42", 0),
    ],
)
def test_failed_case_fails_the_run_and_preserves_counts_and_teardown(tmp_path, commands, passed):
    result = run_fixture_suites(tmp_path, {"suites/01_example.sh": f"test_example() {{ {commands}; }}\n"})
    assert result.returncode != 0
    assert "continued after failure" not in result.stdout
    assert f"Passed:\x1b[0m  {passed}" in result.stdout
    assert "Failed:\x1b[0m  1" in result.stdout
    assert "teardown ran" in result.stdout


def test_suite_diagnostics_run_before_teardown_on_failure(tmp_path):
    result = run_fixture_suites(
        tmp_path,
        {"suites/01_example.sh": '''
            suite_diagnostics() { echo "diagnostics saw status $1"; }
            test_example() { return 42; }
        '''},
    )
    assert result.returncode != 0
    assert "diagnostics saw status 42" in result.stdout
    assert result.stdout.index("diagnostics saw status 42") < result.stdout.index("teardown ran")


def test_only_cases_defined_by_the_suite_run_and_share_state(tmp_path):
    result = run_fixture_suites(
        tmp_path,
        {
            "helper.sh": "test_imported() { echo 'ran imported case'; }\n",
            "suites/01_example.sh": '''
                source "$SCRIPT_DIR/helper.sh"
                test_first() { value=from_first; }
                test_second() { assert 'shared state' test "$value" = from_first; }
            ''',
        },
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert "PASS: shared state" in result.stdout
    assert "ran imported case" not in result.stdout


def test_selected_suite_can_skip_intentionally(tmp_path):
    result = run_fixture_suites(tmp_path, {"suites/01_example.sh": "E2E_SKIP_REASON='fixture unavailable'\n"}, "example")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "Skipping 01_example: fixture unavailable" in result.stdout
    assert "Skipped:\x1b[0m 1" in result.stdout


def selected_suites(*arguments):
    result = run_harness_functions("select_suites", *arguments)
    assert result.returncode == 0, result.stdout + result.stderr
    _, _, suites = result.stdout.partition("Selected e2e suites: ")
    return suites.split()


def test_default_selection_includes_each_suite_once():
    assert selected_suites() == sorted(path.stem for path in RUNNER.with_name("suites").glob("*.sh"))


def test_cluster_attachment_runs_lifecycle_first():
    assert selected_suites("--builds-disabled", "cluster_attachment") == [
        "01_agent_lifecycle",
        "09_cluster_attachment",
    ]


def test_ci_matrix_covers_every_suite():
    workflow = RUNNER.parents[2] / ".github" / "workflows" / "e2e.yml"
    matrix = subprocess.check_output(["yq", ".jobs.suite.strategy.matrix.include[].args", str(workflow)], text=True)
    assert matrix.splitlines()
    ci_suites = {suite for arguments in matrix.splitlines() for suite in selected_suites(*shlex.split(arguments))}
    assert ci_suites == set(selected_suites())


def test_resource_status_stops_when_crash_deployment_is_not_accepted(tmp_path):
    suite = Path(__file__).resolve().parent / "suites/09_resource_status.sh"
    result = run_fixture_suites(
        tmp_path,
        {"suites/01_runtime.sh": f'''
            source {shlex.quote(str(suite))}
            status_dir="$FIXTURE_ROOT/status"
            cli_write_image_config() {{ :; }}
            yq() {{ :; }}
            loco_cli() {{ echo "fixture plan failed"; return 42; }}
            e2e_psql() {{ :; }}
            runtime_event_list() {{ :; }}
            wait_for() {{ echo "incorrectly waited for an event"; }}
            test_fixture() {{ test_s06_crash_loop_records_one_event_across_reconnect; }}
        '''},
    )
    assert result.returncode != 0
    assert "fixture plan failed" in result.stdout
    assert "incorrectly waited for an event" not in result.stdout
