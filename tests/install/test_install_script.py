import subprocess
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[2] / "web/public/install.sh"


def run(*args):
    return subprocess.run(["sh", str(SCRIPT), *args], capture_output=True, text=True, check=False)


def test_help_lists_the_installable_binaries():
    result = run("--help")
    assert result.returncode == 0
    assert "--binary NAME" in result.stdout
    assert "loco or locoadm" in result.stdout


def test_rejects_an_unknown_binary_before_downloading():
    result = run("--binary", "bogus", "--bin-dir", "/nonexistent")
    assert result.returncode == 1
    assert "unknown binary: bogus" in result.stderr
    assert "Downloading" not in result.stdout


def test_binary_flag_requires_a_value():
    result = run("--binary")
    assert result.returncode == 1
    assert "--binary requires a value" in result.stderr
