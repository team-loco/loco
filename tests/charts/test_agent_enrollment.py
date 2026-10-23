import subprocess

import pytest
from test_platform_contracts import render_documents

TAG = {"image": {"tag": "test"}}


def _agent_env(agent):
    (deployment,) = [
        document
        for document in render_documents(
            "loco-core",
            "agent-deployment.yaml",
            {"agent": {**TAG, **agent}},
            includes=("env-config-secret.yml",),
        )
        if document["kind"] == "Deployment"
    ]
    (container,) = deployment["spec"]["template"]["spec"]["containers"]
    return {variable["name"]: variable for variable in container["env"]}


def test_agent_reads_a_chart_rendered_join_token():
    env = _agent_env({"joinToken": {"value": "loco_j_token"}})
    assert env["LOCO_AGENT_KEY_SECRET"]["value"] == "loco-core-agent-key"
    assert env["LOCO_JOIN_TOKEN"]["valueFrom"]["secretKeyRef"] == {
        "name": "loco-core-agent-join-token",
        "key": "join-token",
        "optional": True,
    }
    (secret,) = render_documents(
        "loco-core", "agent-join-token-secret.yaml", {"agent": {"joinToken": {"value": "loco_j_token"}}}
    )
    assert secret["metadata"]["name"] == "loco-core-agent-join-token"
    assert secret["stringData"] == {"join-token": "loco_j_token"}


def test_agent_reads_an_existing_join_token_secret():
    env = _agent_env({"joinToken": {"existingSecret": "enrollment", "secretKey": "token"}})
    assert env["LOCO_JOIN_TOKEN"]["valueFrom"]["secretKeyRef"]["name"] == "enrollment"
    assert env["LOCO_JOIN_TOKEN"]["valueFrom"]["secretKeyRef"]["key"] == "token"
    assert render_documents("loco-core", "agent-join-token-secret.yaml", {"agent": {"joinToken": {"existingSecret": "enrollment"}}}) == []


def test_enrolled_agent_needs_no_join_token():
    env = _agent_env({})
    assert "LOCO_JOIN_TOKEN" not in env
    assert "AGENT_TOKEN" not in env
    assert env["LOCO_ASSERTION_LIFETIME"]["value"] == "1m"


def test_join_token_value_and_existing_secret_are_exclusive():
    with pytest.raises(subprocess.CalledProcessError):
        _agent_env({"joinToken": {"value": "loco_j_token", "existingSecret": "enrollment"}})
