import subprocess

import pytest

from helpers import agent_env, chart_values, render_template

CHART = "loco-worker"
DEFAULTS = chart_values(CHART)["agent"]
JOIN_TOKEN = "join-token-value"


def test_agent_reads_a_chart_rendered_join_token():
    env = agent_env({"agent": {"joinToken": {"value": JOIN_TOKEN}}})
    (secret,) = render_template(
        CHART, "agent-join-token-secret.yaml", {"agent": {"joinToken": {"value": JOIN_TOKEN}}}
    )
    assert env["LOCO_JOIN_TOKEN"]["valueFrom"]["secretKeyRef"] == {
        "name": secret["metadata"]["name"],
        "key": DEFAULTS["joinToken"]["secretKey"],
        "optional": True,
    }
    assert secret["stringData"] == {DEFAULTS["joinToken"]["secretKey"]: JOIN_TOKEN}


def test_agent_reads_an_existing_join_token_secret():
    joined = {"existingSecret": "enrollment", "secretKey": "token"}
    env = agent_env({"agent": {"joinToken": joined}})
    reference = env["LOCO_JOIN_TOKEN"]["valueFrom"]["secretKeyRef"]
    assert (reference["name"], reference["key"]) == (joined["existingSecret"], joined["secretKey"])
    rendered = render_template(CHART, "agent-join-token-secret.yaml", {"agent": {"joinToken": joined}})
    assert rendered == []


def test_enrolled_agent_needs_no_join_token():
    env = agent_env({})
    assert "LOCO_JOIN_TOKEN" not in env
    assert env["LOCO_AGENT_KEY_SECRET"]["value"].startswith(CHART)
    assert env["LOCO_ASSERTION_LIFETIME"]["value"] == DEFAULTS["assertionLifetime"]


def test_join_token_value_and_existing_secret_are_exclusive():
    with pytest.raises(subprocess.CalledProcessError):
        agent_env({"agent": {"joinToken": {"value": JOIN_TOKEN, "existingSecret": "enrollment"}}})
