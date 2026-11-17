import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]


@pytest.mark.parametrize(
    "path",
    [
        "main.py",
        "tests/suite.py",
        "tools/platform-images/verify.py",
        "examples/app/main.py",
        ".agents/skills/ui-evidence/scripts/evidence.py",
    ],
)
@pytest.mark.parametrize("source", ["import os\nprint(1)\n", "print( 1 )\n"])
def test_python_tasks_check_and_fix_tracked_files(tmp_path, path, source):
    subprocess.run(["git", "init", "--quiet", tmp_path], check=True)
    (tmp_path / "ruff.toml").write_text((ROOT / "ruff.toml").read_text())
    tests = tmp_path / "tests"
    tests.mkdir()
    (tests / "pyproject.toml").write_text((ROOT / "tests/pyproject.toml").read_text())
    file = tmp_path / path
    file.parent.mkdir(parents=True, exist_ok=True)
    file.write_text(source)
    subprocess.run(["git", "add", path], cwd=tmp_path, check=True)

    def task(kind):
        return subprocess.run(
            [ROOT / "mise-tasks" / kind / "python"],
            cwd=tmp_path,
            capture_output=True,
            text=True,
            check=False,
        )

    lint = task("lint")
    assert lint.returncode == 1, lint.stdout + lint.stderr
    assert file.read_text() == source
    fix = task("fmt")
    assert fix.returncode == 0, fix.stdout + fix.stderr
    assert file.read_text() == "print(1)\n"
    lint = task("lint")
    assert lint.returncode == 0, lint.stdout + lint.stderr
