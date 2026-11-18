import subprocess

import pytest
from helpers import render_chart


def render_setting(path, value):
    for key in reversed(path.split(".")):
        value = {key: value}
    return render_chart("loco-control-plane", value)


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
    with pytest.raises(subprocess.CalledProcessError) as failure:
        render_setting(path, value)
    assert "values don't meet the specifications" in failure.value.stderr


@pytest.mark.parametrize(
    "path,value",
    [
        (path, value)
        for path, values in [
            ("api.defaults.cpu", ["", "100m", "+1", "0.5", "1e0"]),
            ("api.defaults.memory", ["", "32Mi", "1G", "0.5Gi", "1e9"]),
            (
                "api.joinToken.ttl",
                ["", "1h30m", "+.5m", "1.5s", "1ms", "1us", "1µs", "1μs", "1ns"],
            ),
            ("api.joinToken.maxTtl", ["168h", "72h"]),
            ("api.retention.events", ["1h0m0s", "720h"]),
            ("api.retention.images", [0, 1]),
        ]
        for value in values
    ],
)
def test_valid_api_settings_render(path, value):
    render_setting(path, value)
