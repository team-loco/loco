import subprocess
import tempfile
import tomllib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def _check(tag, bunfig=None):
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        (root / "docs").mkdir()
        (root / "mise.toml").write_text((ROOT / "mise.toml").read_text())
        (root / "renovate.json5").write_text((ROOT / "renovate.json5").read_text())
        (root / "bunfig.toml").write_text(bunfig or (ROOT / "bunfig.toml").read_text())
        (root / "docs/pyproject.toml").write_text(
            (ROOT / "docs/pyproject.toml").read_text()
        )
        (root / "Dockerfile").write_text(
            f"FROM --platform=$BUILDPLATFORM golang:{tag}@sha256:"
            + "a" * 64
            + " AS builder\n"
        )
        subprocess.run(["git", "init", "-q", str(root)], check=True)
        subprocess.run(
            [
                "git",
                "-C",
                str(root),
                "add",
                "Dockerfile",
                "mise.toml",
                "docs/pyproject.toml",
            ],
            check=True,
        )
        return subprocess.run(
            ["bash", str(ROOT / "mise-tasks/lint/versions")],
            cwd=root,
            text=True,
            capture_output=True,
        )


def test_build_platform_stage_does_not_hide_version_drift():
    result = _check("0.0.0")
    assert result.returncode != 0
    assert "mise.toml pins" in result.stdout


def test_build_platform_stage_accepts_pinned_version():
    pinned = tomllib.loads((ROOT / "mise.toml").read_text())["tools"]["go"]
    result = _check(pinned)
    assert result.returncode == 0, result.stdout + result.stderr


def test_renovate_waits_as_long_as_bunfig():
    pinned = tomllib.loads((ROOT / "mise.toml").read_text())["tools"]["go"]
    result = _check(pinned, '[install]\nminimumReleaseAge = "1 hour"\n')
    assert result.returncode != 0
    assert "but bunfig.toml sets 1 hour" in result.stdout
