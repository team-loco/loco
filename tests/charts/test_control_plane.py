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
    assert deployments == {"loco-control-plane-api", "loco-control-plane-obs-proxy-query"}
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
        == "http://loco-control-plane-obs-proxy-query.platform-test.svc.cluster.local"
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


def test_api_reads_bootstrap_token_from_existing_secret():
    deployment = render(
        "loco-control-plane",
        "api-deployment.yaml",
        {
            "api": {
                **API_VALUES,
                "bootstrapToken": {
                    "existingSecret": {"name": "loco-bootstrap", "key": "value"}
                },
            }
        },
    )
    env = deployment["spec"]["template"]["spec"]["containers"][0]["env"]
    (bootstrap,) = [entry for entry in env if entry["name"] == "LOCO_BOOTSTRAP_TOKEN"]
    assert bootstrap == {
        "name": "LOCO_BOOTSTRAP_TOKEN",
        "valueFrom": {"secretKeyRef": {"name": "loco-bootstrap", "key": "value"}},
    }
    default = render("loco-control-plane", "api-deployment.yaml", {"api": API_VALUES})
    default_env = default["spec"]["template"]["spec"]["containers"][0]["env"]
    assert "LOCO_BOOTSTRAP_TOKEN" not in [entry["name"] for entry in default_env]


def test_only_api_can_reach_private_proxy():
    policy = render("loco-control-plane", "obs-proxy-query-network-policy.yaml", {})
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
        "podSelector": {"matchLabels": {"app": "loco-control-plane-obs-proxy-query"}}
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


def test_disabled_proxy_renders_nothing_and_leaves_the_api_unwired():
    documents = render_chart(
        "loco-control-plane",
        {"api": API_VALUES, "obsProxy": {"enabled": False}, "ui": {"enabled": False}},
    )
    names = {document["metadata"]["name"] for document in documents}
    assert not any("obs-proxy" in name for name in names)
    (api,) = [
        document
        for document in documents
        if document["metadata"]["name"] == "loco-control-plane-api"
        and document["kind"] == "Deployment"
    ]
    env = api["spec"]["template"]["spec"]["containers"][0]["env"]
    assert [entry["name"] for entry in env] == ["APP_PORT"]
    (policy,) = [
        document
        for document in documents
        if document["kind"] == "NetworkPolicy"
        and document["metadata"]["name"] == "loco-control-plane-api"
    ]
    (rule,) = policy["spec"]["ingress"]
    assert len(rule["from"]) == 1


INGEST = {
    "enabled": True,
    "clickhouse": {"existingSecret": {"name": "proxy-ingest", "key": "url"}},
}


def test_ingest_runs_as_its_own_deployment_without_migrator_rights():
    documents = render_chart(
        "loco-control-plane",
        {
            "api": API_VALUES,
            "obsProxy": {**TEST_PROXY, "ingest": INGEST},
            "ui": {"enabled": False},
        },
    )
    deployments = {
        document["metadata"]["name"]: document
        for document in documents
        if document["kind"] == "Deployment"
    }
    assert set(deployments) == {
        "loco-control-plane-api",
        "loco-control-plane-obs-proxy-query",
        "loco-control-plane-obs-proxy-ingest",
    }
    pod = deployments["loco-control-plane-obs-proxy-ingest"]["spec"]["template"]
    assert pod["metadata"]["labels"] == {
        "app": "loco-control-plane-obs-proxy-ingest",
        "app.kubernetes.io/component": "observability-proxy",
    }
    (container,) = pod["spec"]["containers"]
    env = {item["name"]: item for item in container["env"]}
    assert env["QUERY_ENABLED"]["value"] == "false"
    assert env["INGEST_ENABLED"]["value"] == "true"
    assert env["CLICKHOUSE_INGEST_URL"]["valueFrom"]["secretKeyRef"] == {
        "name": "proxy-ingest",
        "key": "url",
    }
    assert not {"CLICKHOUSE_URL", "CLICKHOUSE_MIGRATOR_URL", "CONTROL_PLANE_URL"} & set(env)
    ports = {port["name"]: port["containerPort"] for port in container["ports"]}
    assert ports == {"health": 8080, "otlp-grpc": 4317, "otlp-http": 4318}
    assert env["PORT"]["value"] == "8080"
    assert env["OTLP_GRPC_PORT"]["value"] == "4317"
    assert env["OTLP_HTTP_PORT"]["value"] == "4318"
    assert container["readinessProbe"]["httpGet"] == {"path": "/readyz", "port": "health"}

    services = {
        document["metadata"]["name"]: document
        for document in documents
        if document["kind"] == "Service"
    }
    ingest = services["loco-control-plane-obs-proxy-ingest"]["spec"]
    assert ingest["selector"] == {"app": "loco-control-plane-obs-proxy-ingest"}
    assert [(port["port"], port["targetPort"]) for port in ingest["ports"]] == [
        (4317, "otlp-grpc"),
        (4318, "otlp-http"),
    ]
    assert services["loco-control-plane-obs-proxy-query"]["spec"]["selector"] == {
        "app": "loco-control-plane-obs-proxy-query"
    }

    (policy,) = [
        document
        for document in documents
        if document["kind"] == "NetworkPolicy"
        and document["metadata"]["name"] == "loco-control-plane-obs-proxy-ingest"
    ]
    (rule,) = policy["spec"]["ingress"]
    assert rule["from"] == [{"podSelector": {}}]
    assert [port["port"] for port in rule["ports"]] == [4317, 4318]


def test_ingest_is_off_by_default_and_requires_its_secret():
    documents = render_documents(
        "loco-control-plane", "obs-proxy-ingest-deployment.yaml", {"obsProxy": TEST_PROXY}
    )
    assert documents == []
    with pytest.raises(subprocess.CalledProcessError) as caught:
        render(
            "loco-control-plane",
            "obs-proxy-ingest-deployment.yaml",
            {"obsProxy": {**TEST_PROXY, "ingest": {"enabled": True}}},
        )
    assert "obsProxy.ingest.clickhouse.existingSecret.name is required" in caught.value.output
