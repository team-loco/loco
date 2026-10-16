import importlib.util
import json
import os
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("discovery_diagnostics", ROOT / "e2e/discovery_diagnostics.py")
diagnostics = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(diagnostics)


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
    assert len(calls) == 8
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
    assert len(outcomes) == 6
    assert all(outcome["timeout"] for outcome in outcomes)
    assert len(calls) == 6


def failure_environment(tmp_path):
    executable = tmp_path / "python3"
    executable.write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "$DIAGNOSTIC_ARGS"\nexit 7\n')
    executable.chmod(0o755)
    arguments = tmp_path / "args"
    env = {**os.environ, "PATH": f"{tmp_path}:{os.environ['PATH']}", "DIAGNOSTIC_ARGS": str(arguments),
           "E2E_ROOT_DIR": str(ROOT), "E2E_KIND_CLUSTER": "fixture", "E2E_BUILD_WORK_DIR": str(tmp_path),
           "E2E_RUN_ID": "fixture", "KUBECONFIG_FILE": str(tmp_path / "private-kubeconfig")}
    return env, arguments


def test_discovery_failure_trap_preserves_original_exit_even_when_collection_fails(tmp_path):
    env, arguments = failure_environment(tmp_path)
    script = f'''source "{ROOT}/e2e/lib.sh"
source "{ROOT}/e2e/tests/10_discovery.sh"
assert_contains "first workspace FQDN" workspace-one sh -c 'echo "curl: (6) Timeout while contacting DNS servers" >&2; exit 6' || true
exit 23
'''
    result = subprocess.run(["bash", "-eu", "-c", script], env=env, capture_output=True, text=True)
    assert result.returncode == 23
    assert "Timeout while contacting DNS servers" in result.stdout
    args = arguments.read_text().splitlines()
    assert args[-1] == str(ROOT / "e2e/out/discovery/fixture")
    assert args.count("--namespace") == 2
    assert "--context" in args and "kind-fixture" in args
    arguments.unlink()
    success = f'source "{ROOT}/e2e/lib.sh"\nsource "{ROOT}/e2e/tests/10_discovery.sh"\nexit 0\n'
    assert subprocess.run(["bash", "-eu", "-c", success], env=env, capture_output=True).returncode == 0
    assert not arguments.exists()


def test_uncaught_failure_collects_before_any_assertion_changes_counts(tmp_path):
    env, arguments = failure_environment(tmp_path)
    result = subprocess.run(["bash", "-eu", "-c", f'source "{ROOT}/e2e/lib.sh"\nsource "{ROOT}/e2e/tests/10_discovery.sh"\nexit 19\n'],
                            env=env, capture_output=True)
    assert result.returncode == 19
    assert arguments.exists()


def test_suite_subshell_writes_failed_counts_and_preserves_parent_failure(tmp_path):
    env, arguments = failure_environment(tmp_path)
    counts = tmp_path / "counts"
    script = f'''source "{ROOT}/e2e/lib.sh"
(
    source "{ROOT}/e2e/tests/10_discovery.sh"
    assert_contains "first workspace FQDN" workspace-one sh -c 'echo "curl: (6) Timeout while contacting DNS servers" >&2; exit 6' || true
    echo "$E2E_PASS $E2E_FAIL $E2E_SKIP" > "{counts}"
)
read -r E2E_PASS E2E_FAIL E2E_SKIP < "{counts}"
[ "$E2E_FAIL" -eq 0 ]
'''
    result = subprocess.run(["bash", "-eu", "-c", script], env=env, capture_output=True)
    assert result.returncode == 1
    assert counts.read_text().strip() == "0 1 0"
    assert arguments.exists()
