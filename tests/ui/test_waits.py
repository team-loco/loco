import ast
from pathlib import Path

import pytest

SUITES = sorted(
    path
    for path in Path(__file__).parent.glob("*.py")
    if not path.name.startswith("test_")
)
RESPONSE_WAITS = {"expect_response", "wait_for_response"}
MATCH = "wait with harness.expect_rpc on the request body, or for the outcome on screen"


def raw_waits(path):
    for node in ast.walk(ast.parse(path.read_text(), str(path))):
        if (
            isinstance(node, ast.Attribute)
            and node.attr in RESPONSE_WAITS
            and path.name != "harness.py"
        ):
            yield node.lineno, f"{node.attr} accepts any matching response: {MATCH}"
        elif isinstance(node, ast.Constant) and node.value == "networkidle":
            yield node.lineno, f"networkidle never comes with an open stream: {MATCH}"
        elif (
            isinstance(node, ast.Call)
            and isinstance(node.func, ast.Attribute)
            and node.func.attr == "clear"
        ):
            yield (
                node.lineno,
                f"a cleared request log misses requests still in flight: {MATCH}",
            )


@pytest.mark.parametrize("path", SUITES, ids=lambda path: path.name)
def test_waits_name_what_they_wait_for(path):
    assert list(raw_waits(path)) == []
