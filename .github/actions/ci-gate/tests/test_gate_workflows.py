import json
import subprocess
from pathlib import Path

WORKFLOWS = Path(__file__).resolve().parents[3] / "workflows"
GATE = WORKFLOWS / "ci-gate.yml"


def _workflow(path):
    result = subprocess.run(
        ["yq", "-o", "json", ".", str(path)], capture_output=True, text=True, check=True
    )
    return json.loads(result.stdout)


def test_gate_waits_for_every_pull_request_workflow():
    gate = _workflow(GATE)
    gated = set(gate["on"]["workflow_run"]["workflows"])
    pull_request_workflows = set()
    for path in WORKFLOWS.glob("*.yml"):
        if path == GATE:
            continue
        workflow = _workflow(path)
        if "pull_request" in workflow["on"]:
            pull_request_workflows.add(workflow["name"])
    assert pull_request_workflows - gated == set()
