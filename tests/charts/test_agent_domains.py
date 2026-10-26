from helpers import agent_env, render_template

APPS = {"global": {"domain": {"apps": ["apps.test", "other.test"]}}}


def _agent_role(overrides):
    (role,) = [
        document
        for document in render_template("loco-worker", "agent-rbac.yaml", {**APPS, **overrides})
        if document["kind"] == "Role"
    ]
    return role["rules"]


def test_agent_reads_the_gateway_and_certificate_the_chart_renders():
    env = agent_env(APPS)
    assert env["LOCO_APP_DOMAINS"]["value"] == "apps.test,other.test"
    (gateway,) = [
        document
        for document in render_template("loco-worker", "gateway.yaml", APPS)
        if document["kind"] == "Gateway"
    ]
    assert env["LOCO_GATEWAY"]["value"] == gateway["metadata"]["name"]
    (certificate,) = render_template("loco-worker", "cm-cert.yaml", APPS)
    assert env["LOCO_CERTIFICATE"]["value"] == certificate["metadata"]["name"]


def test_agent_reads_no_certificate_with_an_existing_tls_secret():
    existing = {"certManager": {"existingTLSSecret": "wildcard-tls"}}
    assert agent_env({**APPS, **existing})["LOCO_CERTIFICATE"]["value"] == ""
    assert not any("cert-manager.io" in rule["apiGroups"] for rule in _agent_role(existing))


def test_agent_only_reads_its_gateway_and_certificate():
    rules = _agent_role({})
    env = agent_env(APPS)
    gateway = [rule for rule in rules if "gateway.networking.k8s.io" in rule["apiGroups"]]
    certificate = [rule for rule in rules if "cert-manager.io" in rule["apiGroups"]]
    assert gateway == [
        {"apiGroups": ["gateway.networking.k8s.io"], "resources": ["gateways"], "resourceNames": [env["LOCO_GATEWAY"]["value"]], "verbs": ["get"]}
    ]
    assert certificate == [
        {"apiGroups": ["cert-manager.io"], "resources": ["certificates"], "resourceNames": [env["LOCO_CERTIFICATE"]["value"]], "verbs": ["get"]}
    ]
