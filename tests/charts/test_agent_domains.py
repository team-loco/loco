from test_platform_contracts import render_documents

APPS = {"global": {"domain": {"apps": ["apps.test", "other.test"]}}}
TAG = {"agent": {"image": {"tag": "test"}}}


def _agent_env(overrides):
    (deployment,) = [
        document
        for document in render_documents(
            "loco-worker",
            "agent-deployment.yaml",
            {**TAG, **APPS, **overrides},
            includes=("env-config-secret.yml",),
        )
        if document["kind"] == "Deployment"
    ]
    (container,) = deployment["spec"]["template"]["spec"]["containers"]
    return {variable["name"]: variable.get("value") for variable in container["env"]}


def _agent_role(overrides):
    (role,) = [
        document
        for document in render_documents("loco-worker", "agent-rbac.yaml", {**APPS, **overrides})
        if document["kind"] == "Role"
    ]
    return role["rules"]


def test_agent_reads_the_gateway_and_certificate_the_chart_renders():
    env = _agent_env({})
    assert env["LOCO_APP_DOMAINS"] == "apps.test,other.test"
    (gateway,) = [
        document
        for document in render_documents("loco-worker", "gateway.yaml", APPS)
        if document["kind"] == "Gateway"
    ]
    assert env["LOCO_GATEWAY"] == gateway["metadata"]["name"]
    (certificate,) = render_documents("loco-worker", "cm-cert.yaml", APPS)
    assert env["LOCO_CERTIFICATE"] == certificate["metadata"]["name"]


def test_agent_reads_no_certificate_with_an_existing_tls_secret():
    existing = {"certManager": {"existingTLSSecret": "wildcard-tls"}}
    assert _agent_env(existing)["LOCO_CERTIFICATE"] == ""
    assert not any("cert-manager.io" in rule["apiGroups"] for rule in _agent_role(existing))


def test_agent_only_reads_its_gateway_and_certificate():
    rules = _agent_role({})
    env = _agent_env({})
    gateway = [rule for rule in rules if "gateway.networking.k8s.io" in rule["apiGroups"]]
    certificate = [rule for rule in rules if "cert-manager.io" in rule["apiGroups"]]
    assert gateway == [
        {"apiGroups": ["gateway.networking.k8s.io"], "resources": ["gateways"], "resourceNames": [env["LOCO_GATEWAY"]], "verbs": ["get"]}
    ]
    assert certificate == [
        {"apiGroups": ["cert-manager.io"], "resources": ["certificates"], "resourceNames": [env["LOCO_CERTIFICATE"]], "verbs": ["get"]}
    ]
