import subprocess

import pytest
from test_platform_contracts import chart_values, render_documents

CHART = "loco-core"
DEFAULTS = chart_values(CHART)["agent"]
TAG = {"image": {"tag": "test"}}
JOIN_TOKEN = "join-token-value"


def _agent_env(agent):
    (deployment,) = [
        document
        for document in render_documents(
            CHART,
            "agent-deployment.yaml",
            {"agent": {**TAG, **agent}},
            includes=("env-config-secret.yml",),
        )
        if document["kind"] == "Deployment"
    ]
    (container,) = deployment["spec"]["template"]["spec"]["containers"]
    return {variable["name"]: variable for variable in container["env"]}


def test_agent_reads_a_chart_rendered_join_token():
    env = _agent_env({"joinToken": {"value": JOIN_TOKEN}})
    (secret,) = render_documents(
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
    env = _agent_env({"joinToken": joined})
    reference = env["LOCO_JOIN_TOKEN"]["valueFrom"]["secretKeyRef"]
    assert (reference["name"], reference["key"]) == (joined["existingSecret"], joined["secretKey"])
    rendered = render_documents(CHART, "agent-join-token-secret.yaml", {"agent": {"joinToken": joined}})
    assert rendered == []


def test_enrolled_agent_needs_no_join_token():
    env = _agent_env({})
    assert "LOCO_JOIN_TOKEN" not in env
    assert env["LOCO_AGENT_KEY_SECRET"]["value"].startswith(CHART)
    assert env["LOCO_ASSERTION_LIFETIME"]["value"] == DEFAULTS["assertionLifetime"]


def test_join_token_value_and_existing_secret_are_exclusive():
    with pytest.raises(subprocess.CalledProcessError):
        _agent_env({"joinToken": {"value": JOIN_TOKEN, "existingSecret": "enrollment"}})
