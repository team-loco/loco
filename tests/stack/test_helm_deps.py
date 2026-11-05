import os
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

DEPS_TASK = ROOT / "mise-tasks/helm/deps"
DOWNLOAD_ERROR = "Error: could not download opentelemetry-collector-0.178.2.tgz: 504 Gateway Time-out"


def run_deps(tmp_path, failures):
    stubs = tmp_path / "bin"
    stubs.mkdir()
    (stubs / "helm").write_text(
        "#!/usr/bin/env bash\n"
        'echo "$*" >> "$HELM_CALLS"\n'
        'if [ "$1" = dependency ] && [ "$(grep -c "^dependency" "$HELM_CALLS")" -le "$DOWNLOAD_FAILURES" ]; then\n'
        f"  echo '{DOWNLOAD_ERROR}' >&2\n"
        "  exit 1\n"
        "fi\n"
    )
    (stubs / "sleep").write_text("#!/usr/bin/env bash\n")
    for stub in stubs.iterdir():
        stub.chmod(0o755)
    calls = tmp_path / "helm-calls"
    calls.write_text("")
    result = subprocess.run(
        ["bash", str(DEPS_TASK)],
        cwd=ROOT,
        env={
            **os.environ,
            "PATH": f"{stubs}{os.pathsep}{os.environ['PATH']}",
            "HELM_CALLS": str(calls),
            "DOWNLOAD_FAILURES": str(failures),
        },
        capture_output=True,
        text=True,
    )
    builds = [call for call in calls.read_text().splitlines() if call.startswith("dependency build")]
    return result, builds


def test_a_failed_download_is_retried(tmp_path):
    result, builds = run_deps(tmp_path, failures=2)
    assert result.returncode == 0, result.stderr
    assert builds == [
        "dependency build --skip-refresh ./charts/loco-worker",
        "dependency build --skip-refresh ./charts/loco-worker",
        "dependency build --skip-refresh ./charts/loco-worker",
        "dependency build --skip-refresh ./charts/loco-control-plane",
    ]


def test_a_download_that_keeps_failing_fails_the_task(tmp_path):
    result, builds = run_deps(tmp_path, failures=3)
    assert result.returncode != 0
    assert DOWNLOAD_ERROR in result.stderr
    assert builds == ["dependency build --skip-refresh ./charts/loco-worker"] * 3
