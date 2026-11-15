import importlib.util
import sys
from pathlib import Path

SOURCE = Path(__file__).parents[1]
sys.path.insert(0, str(SOURCE))
SPEC = importlib.util.spec_from_file_location("gate", SOURCE / "gate.py")
gate = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(gate)

OSV = "OSV / Source Baseline / osv-scan"
JOBS = {
    1: {OSV: "failure", "Tests / unit": "success", "CI Gate": "failure"},
    2: {"OSV": "skipped", "Tests / unit": "success", "CI Gate": "success"},
    3: {OSV: "success", "CI Gate": "success"},
    4: {OSV: "failure", "Tests / unit": "failure", "CI Gate": "failure"},
}


def ci(run_id, conclusion):
    return {"databaseId": run_id, "conclusion": conclusion}


def blocking(*commits):
    return gate.blocking(list(commits), JOBS.__getitem__)


def test_failure_blocks_until_a_later_run_passes_the_failed_jobs():
    assert blocking(("a", ci(1, "failure")), ("b", ci(2, "success"))) == [
        ("a", "failure (" + OSV + ")")
    ]
    assert (
        blocking(
            ("a", ci(1, "failure")),
            ("b", ci(2, "success")),
            ("c", None),
            ("d", ci(3, "success")),
        )
        == []
    )


def test_a_later_run_clears_only_the_jobs_it_passed():
    assert blocking(
        ("a", ci(4, "failure")), ("b", ci(3, "success"))
    ) == [("a", "failure (Tests / unit)")]


def test_the_released_commit_needs_its_own_successful_run():
    assert blocking(("a", ci(2, "success")), ("b", None)) == [("b", "no run")]
    assert blocking(("a", ci(2, "success")), ("b", ci(1, "failure"))) == [
        ("b", "failure")
    ]
    assert blocking(("a", None), ("b", ci(3, ""))) == [("b", "in progress")]


def test_runs_that_did_not_fail_are_not_cleared_by_later_runs():
    assert blocking(("a", ci(1, "cancelled")), ("b", ci(3, "success"))) == [
        ("a", "cancelled")
    ]
