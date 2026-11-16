import importlib.util
import json
import os
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
    assert str(tmp_path / "dev/compose.yaml") in cmd
    assert cmd[-5:] == ["-p", "capture", "down", "-v", "--remove-orphans"]
    assert options["env"]["OPENBAO_IMAGE"] == "pinned-openbao"
    assert options["env"]["OPENBAO_PORT"] == "127.0.0.1:"


def test_cleanup_reports_failure(tmp_path):
    with patch.object(evidence, "openbao_env", return_value={}), patch.object(
        evidence.subprocess, "run", return_value=subprocess.CompletedProcess([], 1)
    ), pytest.raises(evidence.EvidenceError, match="command failed"):
        evidence.stop_compose("capture", tmp_path)


@pytest.mark.parametrize("subnet", ["", "10.243.17.0/24"])
def test_compose_subnet_with_historical_configuration(tmp_path, subnet):
    (tmp_path / "dev").mkdir()
    (tmp_path / "dev/compose.yaml").write_text("services:\n  openbao:\n    image: ${OPENBAO_IMAGE:?}\n    ports: [\"${OPENBAO_PORT:?}:8200\"]\n")
    env = dict(os.environ, OPENBAO_IMAGE="openbao:test", OPENBAO_PORT="127.0.0.1:", LOCO_TEST_SUBNET=subnet)
    config = json.loads(evidence.compose(tmp_path, "capture", env, "config", "--format", "json", capture=True))
    assert config["networks"]["default"]["ipam"]["config"][0].get("subnet", "") == subnet
