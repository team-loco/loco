import json
import os
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]


def test_gate_runs_for_pull_request_head():
    workflow = ROOT / ".github/workflows/ci-gate.yml"
    event = subprocess.check_output(
        ["yq", "-r", ".on | keys | .[]", str(workflow)], text=True
    )
    assert event.strip() == "pull_request"
    condition = subprocess.check_output(
        ["yq", "-r", ".jobs.gate.if", str(workflow)], text=True
    )
    assert condition.strip() == "null"
    sha = subprocess.check_output(
        ["yq", "-r", ".jobs.gate.steps[1].env.SHA", str(workflow)], text=True
    )
    assert sha.strip() == "${{ github.event.pull_request.head.sha }}"


def _run_gate(rows):
    workflow = ROOT / ".github/workflows/ci-gate.yml"
    script = subprocess.check_output(
        ["yq", "-r", ".jobs.gate.steps[1].run", str(workflow)], text=True
    )
    script = script.replace("settle_seconds=30", "settle_seconds=0").replace(
        "settle_seconds_empty=120", "settle_seconds_empty=0"
    )
    with tempfile.TemporaryDirectory() as directory:
        command = Path(directory) / "gh"
        command.write_text(
            '#!/bin/sh\ncase "$*" in\n*actions/runs*) printf "%s" "$WORKFLOW_PAGES" ;;\n*) printf "%s" "$CHECK_PAGES" ;;\nesac\n'
        )
        command.chmod(0o755)
        checks = []
        for row in rows.splitlines():
            name, identifier, status, conclusion = row.split("\t")
            checks.append(
                {
                    "name": name,
                    "id": int(identifier),
                    "status": status,
                    "conclusion": conclusion,
                    "check_suite": {"id": 0},
                }
            )
        env = {
            **os.environ,
            "PATH": directory + os.pathsep + os.environ["PATH"],
            "WORKFLOW_PAGES": json.dumps([{"workflow_runs": []}]),
            "CHECK_PAGES": json.dumps([{"check_runs": checks}]),
            "SELF": "CI Gate",
            "REPO": "fixture/repo",
            "SHA": "fixture",
        }
        return subprocess.run(
            ["bash", "-c", script], cwd=ROOT, env=env, capture_output=True, text=True
        )


def test_successful_and_skipped_checks_pass():
    result = _run_gate(
        "Proxy Tests\t1\tcompleted\tsuccess\nAPI Tests\t2\tcompleted\tskipped\n"
    )
    assert result.returncode == 0, result.stdout + result.stderr


def test_failed_check_blocks_merging():
    result = _run_gate("Proxy Tests\t1\tcompleted\tfailure\n")
    assert result.returncode != 0
    assert "failing checks" in result.stdout


def test_latest_success_supersedes_an_old_failure():
    result = _run_gate(
        "Proxy Tests\t1\tcompleted\tfailure\nProxy Tests\t2\tcompleted\tsuccess\n"
    )
    assert result.returncode == 0, result.stdout + result.stderr
