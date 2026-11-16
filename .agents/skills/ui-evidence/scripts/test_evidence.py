import importlib.util
import subprocess
from pathlib import Path
from unittest.mock import patch

import pytest

spec = importlib.util.spec_from_file_location("evidence", Path(__file__).with_name("evidence.py"))
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)


def test_cleanup_uses_compose_configuration(tmp_path):
    commands = []

    def execute(cmd, **kwargs):
        commands.append((cmd, kwargs))
        return subprocess.CompletedProcess(cmd, 0, "pinned-openbao\n", "")

    with patch.object(evidence.subprocess, "run", side_effect=execute):
        evidence.stop_compose("capture", tmp_path)
    cmd, options = commands[-1]
    assert cmd == ["docker", "compose", "-f", str(tmp_path / "dev/compose.yaml"),
                   "-p", "capture", "down", "-v", "--remove-orphans"]
    assert options["env"]["OPENBAO_IMAGE"] == "pinned-openbao"
    assert options["env"]["OPENBAO_PORT"] == "127.0.0.1:"


def test_cleanup_reports_failure(tmp_path):
    with patch.object(evidence, "openbao_env", return_value={}), patch.object(
        evidence.subprocess, "run", return_value=subprocess.CompletedProcess([], 1)
    ), pytest.raises(evidence.EvidenceError, match="command failed"):
        evidence.stop_compose("capture", tmp_path)
