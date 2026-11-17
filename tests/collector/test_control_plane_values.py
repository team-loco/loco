import json
import subprocess

import pytest
from helpers import ROOT


def render_setting(path, value):
    chart = ROOT / "charts/loco-control-plane"
    return subprocess.run(
        [
            "helm",
            "template",
            "settings",
            str(chart),
            "-f",
            str(chart / "ci/default-values.yaml"),
            "--set-json",
            f"{path}={json.dumps(value)}",
        ],
        capture_output=True,
        text=True,
    )


@pytest.mark.parametrize(
    "path,value",
    [
        ("api.defaults.cpu", "bogus"),
        ("api.defaults.cpu", "0"),
        ("api.defaults.cpu", "-100m"),
        ("api.defaults.memory", "0Mi"),
        ("api.defaults.memory", "32ki"),
        ("api.defaults.memory", "32 megabytes"),
        ("api.retention.events", "0s"),
        ("api.retention.events", "+0h0.0m.0s"),
        ("api.joinToken.ttl", "0"),
        ("api.joinToken.ttl", 1),
        ("api.retention.events", False),
        ("api.joinToken.ttl", "1d"),
        ("api.joinToken.ttl", "-1h"),
        ("api.joinToken.maxTtl", "0.000h"),
        ("api.joinToken.maxTtl", "bogus"),
        ("api.limits.orgsPerUser", 0),
        ("api.limits.orgsPerUser", "4"),
        ("api.defaults.minReplicas", 11),
        ("api.defaults.healthTimeout", 61),
    ],
)
def test_invalid_api_settings_fail_rendering(path, value):
    rendered = render_setting(path, value)
    assert rendered.returncode != 0
    assert "values don't meet the specifications" in rendered.stderr


@pytest.mark.parametrize(
    "path,values",
    [
        ("api.defaults.cpu", ["", "100m", "+1", "0.5", "1e0"]),
        ("api.defaults.memory", ["", "32Mi", "1G", "0.5Gi", "1e9"]),
        (
            "api.joinToken.ttl",
            ["", "1h30m", "+.5m", "1.5s", "1ms", "1us", "1µs", "1μs", "1ns"],
        ),
        ("api.joinToken.maxTtl", ["168h", "72h"]),
        ("api.retention.events", ["1h0m0s", "720h"]),
        ("api.retention.images", [0, 1]),
    ],
)
def test_valid_api_settings_render(path, values):
    for value in values:
        rendered = render_setting(path, value)
        assert rendered.returncode == 0, rendered.stderr
