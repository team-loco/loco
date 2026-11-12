import json
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


@pytest.mark.parametrize("pull_fails", [False, True])
def test_compose_services_use_the_shared_pull_path_before_startup(pull_fails):
    result = run_harness_functions(
        '''
        e2e_compose() {
            case $1 in
                config)
                    test "$*" = "config --images postgres openbao registry s3 dex" || return 1
                    printf '%s\\n' registry.test/postgres:tag@sha256:one registry.test/openbao:tag@sha256:two
                    ;;
                up) echo "compose started: $*" ;;
            esac
        }
        go() {
            test "$1" = -C && test "$2" = "$ROOT_DIR" && test "$3" = run && test "$4" = ./tools/pullimage
            shift 4
            printf 'shared pull: %s\\n' "$@"
            test "$PULL_FAILS" = false
        }
        compose_host_port() { echo 5432; }
        wait_for() { :; }
        pull_compose_images
        setup_postgres
        ''',
        env=os.environ | {"PULL_FAILS": str(pull_fails).lower()},
    )
    assert (result.returncode == 0) is (not pull_fails), result.stdout + result.stderr
    assert "shared pull: registry.test/postgres:tag@sha256:one" in result.stdout
    assert "shared pull: registry.test/openbao:tag@sha256:two" in result.stdout
    assert ("compose started: up -d --wait --pull never postgres" in result.stdout) is (not pull_fails)


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
            E2E_PUBLIC_IMAGE=fixture-image
            cli_write_image_config() {{ :; }}
            yq() {{ :; }}
            loco_cli() {{ echo "fixture plan failed"; return 42; }}
            e2e_psql() {{ :; }}
            wait_for() {{ echo "incorrectly waited for an event"; }}
            test_fixture() {{ test_s06_crash_loop_records_one_event_across_reconnect; }}
        '''},
    )
    assert result.returncode != 0
    assert "fixture plan failed" in result.stdout
    assert "incorrectly waited for an event" not in result.stdout


TRANSIT_FAILURE = (
    '2026/11/10 16:43:52 ERROR <api/main.go:149> loco api failed error="verify the transit keys: '
    'transit kubernetes login as loco-api: context deadline exceeded"'
)


def check_api_restarts(tmp_path, previous_log, restarts="1"):
    # Stub kubectl with one pod whose previous container wrote previous_log.
    (tmp_path / "previous.log").write_text(previous_log)
    return subprocess.run(
        [
            "bash",
            "-c",
            '''
            source "$1"
            kubectl() {
                case "$*" in
                    *"get pods"*) echo regional-api-0 ;;
                    *restartCount*) echo "$RESTARTS" ;;
                    *lastState*) echo '{"exitCode":1,"reason":"Error"}' ;;
                    *--previous*) cat "$PREVIOUS_LOG" ;;
                esac
            }
            check_api_restarts loco-control-plane app=regional-api
            ''',
            "check_api_restarts",
            str(LIB),
        ],
        check=False,
        capture_output=True,
        text=True,
        env=os.environ | {"PREVIOUS_LOG": str(tmp_path / "previous.log"), "RESTARTS": restarts},
    )


def test_api_restart_check_accepts_a_restart_after_the_transit_keys_failed(tmp_path):
    result = check_api_restarts(tmp_path, "2026/11/10 16:43:22 INFO starting loco api\n" + TRANSIT_FAILURE)
    assert result.returncode == 0, result.stderr


def test_api_restart_check_accepts_the_transit_failure_ahead_of_a_large_log(tmp_path):
    result = check_api_restarts(tmp_path, TRANSIT_FAILURE + "\n" + "later output\n" * 100_000)
    assert result.returncode == 0, result.stderr


def test_api_restart_check_rejects_a_restart_for_another_reason(tmp_path):
    result = check_api_restarts(tmp_path, '2026/11/10 16:43:22 ERROR loco api failed error="apply migrations: timeout"')
    assert result.returncode == 1
    assert "regional-api-0 restarted 1 times" in result.stderr
    assert '{"exitCode":1,"reason":"Error"}' in result.stderr


def test_api_restart_check_ignores_pods_that_never_restarted(tmp_path):
    assert check_api_restarts(tmp_path, "", restarts="0").returncode == 0


@pytest.mark.parametrize("endpoint", ["oldest", "newest", None])
def test_cluster_creation_uses_the_selected_pinned_image(endpoint):
    images = json.loads((RUNNER.parents[2] / "agent/pkg/cluster/kubernetes.json").read_text())
    env = {key: value for key, value in os.environ.items() if key != "E2E_KUBERNETES_VERSION"}
    if endpoint is not None:
        env["E2E_KUBERNETES_VERSION"] = endpoint
    result = run_harness_functions('kind() { printf "%s\\n" "$@"; }; create_cluster', env=env)
    assert result.returncode == 0, result.stderr
    arguments = result.stdout.splitlines()
    assert arguments[arguments.index("--image") + 1] == images[endpoint or "newest"]


def test_compatibility_workflow_covers_required_suites():
    workflow = RUNNER.parents[2] / ".github/workflows/kubernetes-compatibility.yml"
    command = subprocess.check_output(
        ["yq", '.jobs.compatibility.steps[] | select(.run != null) | .run', str(workflow)], text=True
    )
    _, *arguments = shlex.split(command)
    assert set(selected_suites(*arguments)) == {
        "01_agent_lifecycle",
        "08_config_apply",
        "10_discovery",
        "11_registry_token",
    }


@pytest.mark.parametrize("matches", [True, False])
def test_retained_cluster_requires_the_selected_image(tmp_path, matches):
    images = json.loads((RUNNER.parents[2] / "agent/pkg/cluster/kubernetes.json").read_text())
    result = run_harness_functions(
        '''
        KUBECONFIG_FILE="$FIXTURE_KUBECONFIG"
        cluster_exists() { return 0; }
        docker() { printf '%s\\n' "$RETAINED_IMAGE"; }
        kind() { return 0; }
        kube() { return 0; }
        setup_kind
        ''',
        env=os.environ | {
            "E2E_KUBERNETES_VERSION": "oldest",
            "RETAINED_IMAGE": images["oldest" if matches else "newest"],
            "FIXTURE_KUBECONFIG": str(tmp_path / "kubeconfig"),
        },
    )
    if matches:
        assert result.returncode == 0, result.stdout + result.stderr
        assert "already exists, reusing" in result.stdout
    else:
        assert result.returncode != 0
        assert "mise run e2e:teardown before changing Kubernetes versions" in result.stdout + result.stderr
