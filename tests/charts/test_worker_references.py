import pytest

from test_platform_contracts import collector_documents, render_chart

RELEASE_NAMESPACE = "platform-test"


@pytest.fixture(scope="module")
def worker():
    documents = render_chart("loco-worker", {})
    return {(document["kind"], document["metadata"]["name"], document["metadata"].get("namespace")): document for document in documents}


def _one(worker, kind, name):
    (document,) = [document for (k, n, _), document in worker.items() if k == kind and n == name]
    return document


def _env(deployment):
    return {item["name"]: item.get("value") for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"]}


def test_collector_namespaces_carry_their_pod_security_level(worker):
    levels = {"loco-obs": "restricted", "loco-obs-node": "privileged"}
    for name, level in levels.items():
        namespace = _one(worker, "Namespace", name)
        assert namespace["metadata"]["annotations"]["helm.sh/resource-policy"] == "keep"
        for mode in ("enforce", "audit", "warn"):
            assert namespace["metadata"]["labels"][f"pod-security.kubernetes.io/{mode}"] == level
    namespaced = [document for document in collector_documents(list(worker.values())) if "namespace" in document["metadata"]]
    assert namespaced
    assert {document["metadata"]["namespace"] for document in namespaced} == set(levels)


def test_agent_references_resolve_to_rendered_objects(worker):
    agent = _one(worker, "Deployment", "loco-worker-agent")
    env = _env(agent)
    service_account = agent["spec"]["template"]["spec"]["serviceAccountName"]
    assert ("ServiceAccount", service_account, RELEASE_NAMESPACE) in worker
    assert ("Deployment", env["LOCO_CONTROLLER_DEPLOYMENT"], RELEASE_NAMESPACE) in worker
    assert ("Namespace", env["LOCO_BUILD_NAMESPACE"], None) in worker
    binding = _one(worker, "RoleBinding", "loco-agent-builds")
    assert binding["subjects"] == [{"kind": "ServiceAccount", "name": service_account, "namespace": RELEASE_NAMESPACE}]


def test_collector_references_resolve_to_rendered_objects(worker):
    env = _env(_one(worker, "Deployment", "loco-controller"))
    assert ("Service", env["LOCO_OTEL_COLLECTOR_SERVICE"], env["LOCO_OBSERVABILITY_NAMESPACE"]) in worker
    assert ("DaemonSet", env["LOCO_METRICS_COLLECTOR_SERVICE"] + "-agent", env["LOCO_METRICS_COLLECTOR_NAMESPACE"]) in worker
    proxy = _one(worker, "EnvoyProxy", "envoy-proxy")
    host = proxy["spec"]["telemetry"]["tracing"]["provider"]["host"]
    service, namespace = host.split(".")[:2]
    assert ("Service", service, namespace) in worker
    gateway = _one(worker, "Gateway", "eg")
    for listener in gateway["spec"]["listeners"]:
        if listener["name"].endswith("-platform"):
            (expression,) = listener["allowedRoutes"]["namespaces"]["selector"]["matchExpressions"]
            assert expression["values"] == [RELEASE_NAMESPACE, namespace]
    ingress = _one(worker, "NetworkPolicy", "allow-otlp-ingress")
    assert ingress["metadata"]["namespace"] == namespace
    sources = [source["namespaceSelector"]["matchLabels"] for source in ingress["spec"]["ingress"][0]["from"]]
    assert {"kubernetes.io/metadata.name": RELEASE_NAMESPACE} in sources
