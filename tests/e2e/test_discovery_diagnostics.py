import json
import os
import shlex
import subprocess
from pathlib import Path

import discovery_diagnostics as diagnostics
from test_run import run_harness_functions

ROOT = Path(__file__).resolve().parents[2]


def test_dns_failure_keeps_only_allowlisted_diagnostics(monkeypatch, tmp_path):
    sensitive = "private-environment-secret-sentinel"
    calls = []
    pod = {"metadata": {"name": "caller-pod", "uid": "caller-uid", "labels": {"app": "caller"},
                        "annotations": {"private": sensitive}},
           "spec": {"nodeName": "node", "containers": [{"env": [{"name": "PASSWORD", "value": sensitive}]}]},
           "status": {"phase": "Running", "containerStatuses": [{"name": "caller", "ready": True, "restartCount": 0}]}}
    objects = {
        "services": [{"metadata": {"name": "caller", "annotations": {"private": sensitive}},
                      "spec": {"clusterIP": "10.0.0.1", "selector": {"app": "caller"}, "ports": [{"port": 80}]}}],
        "endpointslices": [{"metadata": {"name": "caller-endpoints", "annotations": {"private": sensitive}},
                            "addressType": "IPv4", "endpoints": [{"addresses": ["10.0.0.2"], "targetRef": {"private": sensitive}}]}],
        "networkpolicies": [{"metadata": {"name": "allow-dns", "annotations": {"private": sensitive}}, "spec": {"egress": [{"ports": [{"port": 53}]}]}}],
        "pods": [pod],
    }

    def run(command, **kwargs):
        assert kwargs["timeout"] == diagnostics.COMMAND_TIMEOUT_SECONDS
        calls.append(command)
        args = command[command.index("-n") + 2:]
        if args[0] == "get":
            return subprocess.CompletedProcess(command, 0, json.dumps({"items": objects[args[1]]}), "")
        if args[0] == "logs":
            return subprocess.CompletedProcess(command, 0, "CoreDNS healthy\n", "")
        if "nslookup" in args:
            return subprocess.CompletedProcess(command, 1, "", "Timeout while contacting DNS servers\n")
        assert args[-2:] == ["cat", "/etc/resolv.conf"]
        return subprocess.CompletedProcess(command, 0, "nameserver 10.96.0.10\n", "")

    monkeypatch.setattr(diagnostics.subprocess, "run", run)
    diagnostics.collect(tmp_path / "kubeconfig", "kind-fixture", ["ws-fixture"], tmp_path / "out")
    files = {path.name: path.read_text() for path in (tmp_path / "out").iterdir()}
    assert set(files) == {"ws-fixture-services.json", "ws-fixture-endpointslices.json", "ws-fixture-networkpolicies.json",
                          "ws-fixture-pods.json", "ws-fixture-caller-resolv.conf", "ws-fixture-caller-nslookup.txt",
                          "coredns-pods.json", "coredns.log", "commands.json"}
    assert all(sensitive not in text for text in files.values())
    assert "Timeout while contacting DNS servers" in files["commands.json"]
    assert all("secrets" not in command and "describe" not in command and "env" not in command for command in calls)
    assert all(command[command.index("-n") + 1] in ("ws-fixture", "kube-system") for command in calls)


def test_unreachable_cluster_times_out_and_finishes_remaining_diagnostics(monkeypatch, tmp_path):
    calls = []

    def run(command, **kwargs):
        calls.append(command)
        raise subprocess.TimeoutExpired(command, kwargs["timeout"])

    monkeypatch.setattr(diagnostics.subprocess, "run", run)
    diagnostics.collect(tmp_path / "kubeconfig", "kind-fixture", ["ws-fixture"], tmp_path / "out")
    outcomes = json.loads((tmp_path / "out/commands.json").read_text())
    assert len(outcomes) == len(calls) > 1
    assert all(outcome["timeout"] for outcome in outcomes)


def failure_environment(tmp_path):
    executable = tmp_path / "python3"
    executable.write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "$DIAGNOSTIC_ARGS"\nexit 7\n')
    executable.chmod(0o755)
    arguments = tmp_path / "args"
    env = os.environ | {
        "PATH": f"{tmp_path}:{os.environ['PATH']}",
        "DIAGNOSTIC_ARGS": str(arguments),
        "E2E_RUN_ID": "fixture",
    }
    return env, arguments


def run_discovery_fixture(tmp_path, env, commands, *, summary=False):
    fixture = tmp_path / "discovery.sh"
    counts = shlex.quote(str(tmp_path / "counts"))
    discovery_suite = shlex.quote(str(ROOT / "tests/e2e/suites/10_discovery.sh"))
    fixture.write_text(f"source {discovery_suite}\ntest_fixture() {{\n{commands}\n}}\n")
    script = f'E2E_KIND_CLUSTER=fixture\nrun_suite {shlex.quote(str(fixture))} discovery {counts}\n'
    if summary:
        script += f'read -r E2E_PASS E2E_FAIL E2E_SKIP < {counts}\nprint_summary\n'
    return run_harness_functions(script, env=env)


def test_discovery_failure_trap_preserves_original_exit_even_when_collection_fails(tmp_path):
    env, arguments = failure_environment(tmp_path)
    result = run_discovery_fixture(
        tmp_path,
        env,
        """assert_contains "first workspace FQDN" workspace-one sh -c 'echo "curl: (6) Timeout while contacting DNS servers" >&2; exit 6' || true
exit 23""",
    )
    assert result.returncode == 23
    assert "Timeout while contacting DNS servers" in result.stdout
    args = arguments.read_text().splitlines()
    assert args[-1] == str(ROOT / "tests/e2e/out/discovery/fixture")
    assert args.count("--namespace") == 2
    assert "--context" in args and "kind-fixture" in args
    arguments.unlink()
    assert run_discovery_fixture(tmp_path, env, "true").returncode == 0
    assert not arguments.exists()


def test_uncaught_failure_collects_before_any_assertion_changes_counts(tmp_path):
    env, arguments = failure_environment(tmp_path)
    result = run_discovery_fixture(tmp_path, env, "exit 19")
    assert result.returncode == 19
    assert arguments.exists()


def test_suite_subshell_writes_failed_counts_and_preserves_parent_failure(tmp_path):
    env, arguments = failure_environment(tmp_path)
    result = run_discovery_fixture(
        tmp_path,
        env,
        'assert "first workspace FQDN" false || true',
        summary=True,
    )
    assert result.returncode == 1
    assert (tmp_path / "counts").read_text().strip() == "0 1 0"
    assert arguments.exists()


def test_rename_waits_for_tcp_after_the_service_exists(tmp_path):
    calls = tmp_path / "calls"
    result = run_harness_functions(
        r'''
        source "$E2E_ROOT_DIR/tests/e2e/suites/10_discovery.sh"
        discovery_resource_id() { echo resource; }
        e2e_psql() { echo 0; }
        discovery_api() { :; }
        discovery_alias_exists() { :; }
        discovery_alias_absent() { :; }
        sleep() { :; }
        discovery_call() {
            echo attempt >> "$TCP_CALLS"
            [ "$(wc -l < "$TCP_CALLS")" -gt 2 ] || return 1
            echo private-tcp-ok
        }
        test_d03_resource_rename_moves_the_alias_without_a_build
        ''',
        env=os.environ | {"TCP_CALLS": str(calls)},
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert len(calls.read_text().splitlines()) >= 3
