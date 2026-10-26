import json
import os
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
WORKFLOW = ROOT / ".github/workflows/ci-gate.yml"
FAKE_GH = """#!/bin/sh
printf '%s\\n' "$*" >> "$GH_LOG"
case "$*" in
*actions/runs*) printf '%s' "$RUN_PAGES" ;;
*check-runs\\?*) printf '%s' "$CHECK_PAGES" ;;
*) printf '{}' ;;
esac
"""


def _yq(expression, path=WORKFLOW):
    return subprocess.check_output(["yq", "-r", expression, str(path)], text=True).strip()


def test_gate_hears_every_pull_request_workflow():
    names = set()
    for path in (ROOT / ".github/workflows").glob("*.yml"):
        if _yq('.on | has("pull_request")', path) == "true":
            names.add(_yq(".name", path))
    assert set(_yq(".on.workflow_run.workflows[]").splitlines()) == names


def test_gate_reports_on_the_triggering_run_head():
    assert _yq(".on | keys | .[]") == "workflow_run"
    assert _yq(".on.workflow_run.types[]") == "completed"
    assert _yq(".jobs.gate.steps[1].env.SHA") == "${{ github.event.workflow_run.head_sha }}"


def run(identifier, status, conclusion, workflow=1, name="Tests"):
    return {
        "id": identifier,
        "workflow_id": workflow,
        "check_suite_id": identifier,
        "name": name,
        "status": status,
        "conclusion": conclusion,
        "head_sha": "fixture",
    }


def gate(identifier, status, conclusion):
    return {
        "id": identifier,
        "name": "CI Gate",
        "status": status,
        "conclusion": conclusion,
        "check_suite": {"id": 0},
    }


def _run_gate(runs, current, checks=()):
    script = _yq(".jobs.gate.steps[1].run")
    with tempfile.TemporaryDirectory() as directory:
        temp = Path(directory)
        command = temp / "gh"
        command.write_text(FAKE_GH)
        command.chmod(0o755)
        event = temp / "event.json"
        event.write_text(json.dumps({"workflow_run": current}))
        log = temp / "gh.log"
        log.touch()
        env = {
            **os.environ,
            "PATH": directory + os.pathsep + os.environ["PATH"],
            "RUN_PAGES": json.dumps([{"workflow_runs": list(runs)}]),
            "CHECK_PAGES": json.dumps([{"check_runs": list(checks)}]),
            "GH_LOG": str(log),
            "GITHUB_EVENT_PATH": str(event),
            "RUNNER_TEMP": directory,
            "SELF": "CI Gate",
            "REPO": "fixture/repo",
            "SHA": "fixture",
            "DETAILS": "https://example.test/run",
        }
        result = subprocess.run(
            ["bash", "-c", script], cwd=ROOT, env=env, capture_output=True, text=True
        )
        assert result.returncode == 0, result.stdout + result.stderr
        body = temp / "gate.json"
        written = json.loads(body.read_text()) if body.exists() else None
        return log.read_text().splitlines(), written


def _writes(calls):
    return [call for call in calls if "--method" in call]


def test_evaluation_reads_twice_and_writes_once():
    calls, body = _run_gate(
        [run(1, "completed", "success")], run(1, "completed", "success")
    )
    assert len(calls) == 3
    assert len(_writes(calls)) == 1
    assert _writes(calls)[0].startswith("api --method POST repos/fixture/repo/check-runs ")
    assert (body["name"], body["head_sha"], body["status"], body["conclusion"]) == (
        "CI Gate",
        "fixture",
        "completed",
        "success",
    )


def test_unchanged_verdict_writes_nothing():
    calls, body = _run_gate(
        [run(1, "completed", "success"), run(2, "in_progress", None, workflow=2)],
        run(1, "completed", "success"),
        [gate(5, "in_progress", None)],
    )
    assert len(calls) == 2
    assert body is None


def test_pending_gate_is_completed_in_place():
    calls, body = _run_gate(
        [run(1, "completed", "failure")],
        run(1, "completed", "failure"),
        [gate(5, "in_progress", None)],
    )
    assert _writes(calls)[0].startswith("api --method PATCH repos/fixture/repo/check-runs/5 ")
    assert body["conclusion"] == "failure"


def test_rerun_after_a_verdict_creates_a_new_gate():
    calls, body = _run_gate(
        [run(1, "completed", "success"), run(2, "queued", None, workflow=2)],
        run(1, "completed", "success"),
        [gate(5, "completed", "failure")],
    )
    assert _writes(calls)[0].startswith("api --method POST repos/fixture/repo/check-runs ")
    assert body["status"] == "in_progress"
    assert "conclusion" not in body


def test_triggering_run_overrides_a_stale_listing():
    calls, body = _run_gate(
        [run(1, "in_progress", None)], run(1, "completed", "success")
    )
    assert body["conclusion"] == "success"


def test_superseded_cancelled_run_waits_for_its_replacement():
    calls, body = _run_gate(
        [run(1, "completed", "cancelled"), run(2, "queued", None)],
        run(1, "completed", "cancelled"),
    )
    assert body["status"] == "in_progress"
    assert "conclusion" not in body


def test_runs_of_every_event_are_listed():
    calls, _ = _run_gate([run(1, "completed", "success")], run(1, "completed", "success"))
    assert "event=" not in calls[0]
