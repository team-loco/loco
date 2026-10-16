import importlib.util
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "filter_checks.py"
spec = importlib.util.spec_from_file_location("gate_checks", SCRIPT)
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


def run(identifier, status, conclusion):
    return {
        "id": identifier,
        "workflow_id": 1,
        "check_suite_id": identifier,
        "name": "Tests",
        "status": status,
        "conclusion": conclusion,
    }


def _selected(runs, checks=()):
    return gate.current_checks(
        [{"workflow_runs": runs}], [{"check_runs": checks}], "CI Gate"
    )


def test_cancelled_matrix_placeholder_does_not_block_newer_success():
    placeholder = {
        "id": 10,
        "name": "${{ matrix.component }} Tests",
        "status": "completed",
        "conclusion": "cancelled",
        "check_suite": {"id": 1},
    }
    assert _selected(
        [run(1, "completed", "cancelled"), run(2, "completed", "success")],
        [placeholder],
    ) == [("Tests (workflow)", "completed", "success")]


def test_new_workflow_is_pending_before_its_checks_are_created():
    assert _selected([run(1, "completed", "failure"), run(2, "queued", None)]) == [
        ("Tests (workflow)", "queued", "")
    ]


def test_current_failed_or_cancelled_workflow_still_blocks(subtests):
    for conclusion in ("failure", "cancelled", "timed_out"):
        with subtests.test(conclusion=conclusion):
            assert _selected([run(2, "completed", conclusion)]) == [
                ("Tests (workflow)", "completed", conclusion)
            ]


def test_external_check_failures_are_preserved():
    check = {
        "id": 3,
        "name": "External scan",
        "status": "completed",
        "conclusion": "failure",
        "check_suite": {"id": 99},
    }
    assert _selected([], [check]) == [("External scan", "completed", "failure")]


def test_gate_does_not_wait_for_itself():
    own = run(1, "in_progress", None)
    own["name"] = "CI Gate"
    assert _selected([own]) == []
