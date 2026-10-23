import base64
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


def _exporter_token():
    obs = chart_values("loco-obs")
    token = obs["global"]["exporter"]["bearerToken"]
    node = obs["otel-col-daemon"]["namespaceOverride"]
    return {
        "secretName": token["secretName"],
        "secretKey": token["secretKey"],
        "nodeNamespace": node,
    }


def test_agent_writes_the_ingest_token_to_each_collector_namespace():
    token = _exporter_token()
    overrides = {"global": {"exporter": {"bearerToken": token}}}
    (deployment,) = [
        document
        for document in render_documents(
            CHART,
            "agent-deployment.yaml",
            {"agent": TAG, **overrides},
            includes=("env-config-secret.yml",),
        )
        if document["kind"] == "Deployment"
    ]
    (container,) = deployment["spec"]["template"]["spec"]["containers"]
    env = {variable["name"]: variable for variable in container["env"]}
    namespaces = [chart_values(CHART)["global"]["observability"]["namespace"], token["nodeNamespace"]]
    assert env["LOCO_INGEST_TOKEN_SECRET"]["value"] == token["secretName"]
    assert env["LOCO_INGEST_TOKEN_SECRET_KEY"]["value"] == token["secretKey"]
    assert env["LOCO_INGEST_TOKEN_NAMESPACES"]["value"] == ",".join(namespaces)
    documents = render_documents(CHART, "agent-ingest-token.yaml", overrides)
    by_namespace = {}
    for document in documents:
        by_namespace.setdefault(document["metadata"]["namespace"], {})[document["kind"]] = document
    assert sorted(by_namespace) == sorted(namespaces)
    for kinds in by_namespace.values():
        assert set(kinds) == {"Secret", "Role", "RoleBinding"}
        assert kinds["Secret"]["metadata"]["name"] == token["secretName"]
        pending = base64.b64encode(DEFAULTS["pendingIngestToken"].encode()).decode()
        assert kinds["Secret"]["data"] == {token["secretKey"]: pending}
        (rule,) = kinds["Role"]["rules"]
        assert rule == {
            "apiGroups": [""],
            "resources": ["secrets"],
            "resourceNames": [token["secretName"]],
            "verbs": ["get", "update"],
        }
        (subject,) = kinds["RoleBinding"]["subjects"]
        assert subject["name"].endswith("-agent")


def test_the_ingest_token_secret_is_off_without_a_name():
    env = _agent_env({})
    assert not {name for name in env if name.startswith("LOCO_INGEST_TOKEN")}
    assert render_documents(CHART, "agent-ingest-token.yaml", {}) == []
