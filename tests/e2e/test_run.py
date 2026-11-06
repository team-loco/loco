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


def run_harness_functions(body):
    # Load the real allocation and functions without executing infrastructure setup.
    source, _ = RUNNER.read_text().rsplit('\nif [ "$BUILD_ONLY" = true ]; then', 1)
    return subprocess.run(
        ["bash", "-c", source + "\n" + body, str(RUNNER)],
        check=False,
        capture_output=True,
        text=True,
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
