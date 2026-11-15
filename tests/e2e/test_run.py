import shlex
import subprocess
from pathlib import Path

RUNNER = Path(__file__).resolve().parent / "run.sh"


def selected_suites(*arguments):
    # Load the real suite selection without executing infrastructure setup.
    source, _ = RUNNER.read_text().rsplit('\nif [ "$BUILD_ONLY" = true ]; then', 1)
    result = subprocess.run(
        ["bash", "-c", source + "\nselect_suites", str(RUNNER), *arguments],
        check=False,
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    _, _, suites = result.stdout.partition("Selected e2e suites: ")
    return suites.split()


def test_ci_matrix_covers_every_suite():
    workflow = RUNNER.parents[2] / ".github" / "workflows" / "e2e.yml"
    matrix = subprocess.check_output(["yq", ".jobs.suite.strategy.matrix.include[].args", str(workflow)], text=True)
    assert matrix.splitlines()
    ci_suites = {suite for arguments in matrix.splitlines() for suite in selected_suites(*shlex.split(arguments))}
    assert ci_suites == set(selected_suites())
