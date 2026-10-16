import subprocess

import pytest
from test_platform_contracts import TEST_PROXY, render, render_chart, render_documents

API_VALUES = {"image": {"tag": "test"}, "existingSecret": "regional-api-env"}


def test_chart_installs_api_and_proxy_without_migration_jobs():
    documents = render_chart(
        "loco-control-plane",
        {"api": API_VALUES, "obsProxy": TEST_PROXY, "ui": {"enabled": False}},
    )
    deployments = {
        document["metadata"]["name"]
        for document in documents
        if document["kind"] == "Deployment"
    }
    assert deployments == {"loco-control-plane-api", "loco-control-plane-obs-proxy"}
    assert not any((document["kind"] == "Job" for document in documents))
    routes = [document for document in documents if document["kind"] == "HTTPRoute"]
    assert len(routes) == 1
    assert (
        routes[0]["spec"]["rules"][0]["backendRefs"][0]["name"]
        == "loco-control-plane-api"
    )


def test_api_uses_existing_secret_and_private_proxy():
    deployment = render(
        "loco-control-plane",
        "api-deployment.yaml",
        {"api": {**API_VALUES, "port": 9000, "env": {"APP_ENV": "production"}}},
    )
    pod = deployment["spec"]["template"]["spec"]
    assert pod["automountServiceAccountToken"] is False
    assert "initContainers" not in pod
    (api,) = pod["containers"]
    assert api["envFrom"] == [{"secretRef": {"name": "regional-api-env"}}]
    env = {item["name"]: item["value"] for item in api["env"]}
    assert env["APP_PORT"] == "0.0.0.0:9000"
    assert env["APP_ENV"] == "production"
    assert (
        env["OBSERVABILITY_PROXY_URL"]
        == "http://loco-control-plane-obs-proxy.platform-test.svc.cluster.local"
    )
    assert api["startupProbe"] == {
        "httpGet": {"path": "/health", "port": "http"},
        "failureThreshold": 60,
        "periodSeconds": 5,
    }


def test_api_requires_an_operator_secret():
    with pytest.raises(subprocess.CalledProcessError) as caught:
        render(
            "loco-control-plane",
            "api-deployment.yaml",
            {"api": {"image": {"tag": "test"}}},
        )
    assert "api.existingSecret is required" in caught.value.output


def test_chart_owns_port_and_proxy_wiring():
    deployment = render(
        "loco-control-plane",
        "api-deployment.yaml",
        {
            "api": {
                **API_VALUES,
                "env": {"APP_PORT": "wrong", "OBSERVABILITY_PROXY_URL": "wrong"},
            }
        },
    )
    env = deployment["spec"]["template"]["spec"]["containers"][0]["env"]
    assert [entry["name"] for entry in env] == ["APP_PORT", "OBSERVABILITY_PROXY_URL"]
    assert "wrong" not in [entry["value"] for entry in env]


def test_only_api_can_reach_private_proxy():
    policy = render("loco-control-plane", "obs-proxy-network-policy.yaml", {})
    (rule,) = policy["spec"]["ingress"]
    assert rule["from"] == [
        {"podSelector": {"matchLabels": {"app": "loco-control-plane-api"}}}
    ]
    assert rule["ports"] == [{"protocol": "TCP", "port": 8080}]


def test_gateway_and_proxy_can_reach_api():
    policy = render(
        "loco-control-plane", "api-network-policy.yaml", {"gatewayNamespace": "gateway"}
    )
    (rule,) = policy["spec"]["ingress"]
    assert rule["from"][0]["namespaceSelector"]["matchLabels"] == {
        "kubernetes.io/metadata.name": "gateway"
    }
    assert rule["from"][1] == {
        "podSelector": {"matchLabels": {"app": "loco-control-plane-obs-proxy"}}
    }


def test_regional_proxy_can_reach_clickhouse_in_the_obs_namespace():
    policies = render_documents(
        "loco-obs", "network-policies.yaml", {"controlPlaneNamespace": "regional-cp"}
    )
    clickhouse = next(
        (
            policy
            for policy in policies
            if policy["metadata"]["name"] == "allow-clickhouse-ingress"
        )
    )
    assert {
        "from": [
            {
                "namespaceSelector": {
                    "matchLabels": {"kubernetes.io/metadata.name": "regional-cp"}
                },
                "podSelector": {
                    "matchLabels": {
                        "app.kubernetes.io/component": "observability-proxy"
                    }
                },
            }
        ],
        "ports": [{"protocol": "TCP", "port": 9000}],
    } in clickhouse["spec"]["ingress"]


def test_dashboard_uses_the_installation_origin():
    documents = render_documents(
        "loco-control-plane",
        "ui.yaml",
        {"ui": {"image": {"tag": "test"}, "hostname": "app.region.example.test"}},
    )
    config = next((item for item in documents if item["kind"] == "ConfigMap"))
    assert (
        config["data"]["runtime-config.js"]
        == 'window.locoConfig = {"apiURL":"/api"};\n'
    )
    route = next((item for item in documents if item["kind"] == "HTTPRoute"))
    assert route["spec"]["hostnames"] == ["app.region.example.test"]
    api, ui = route["spec"]["rules"]
    assert api["matches"][0]["path"]["value"] == "/api"
    assert api["filters"][0]["urlRewrite"]["path"] == {
        "type": "ReplacePrefixMatch",
        "replacePrefixMatch": "/",
    }
    assert api["backendRefs"][0]["name"] == "loco-control-plane-api"
    assert api["timeouts"] == {"request": "0s"}
    assert ui["backendRefs"][0]["name"] == "loco-control-plane-ui"
    deployment = next((item for item in documents if item["kind"] == "Deployment"))
    (mount,) = deployment["spec"]["template"]["spec"]["containers"][0]["volumeMounts"]
    assert mount["mountPath"] == "/public/runtime-config.js"


def test_same_hostname_does_not_publish_competing_root_routes():
    routes = render_documents(
        "loco-control-plane",
        "api-route.yaml",
        {
            "controlPlaneHostname": "loco.example.test",
            "ui": {"hostname": "loco.example.test"},
        },
    )
    assert routes == []
