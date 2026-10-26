import json
import re
import subprocess

import pytest
from test_platform_contracts import TEST_PROXY, chart_values, quantity, render, render_chart, render_documents

BOOTSTRAP_TOKEN_ENV = "LOCO_BOOTSTRAP_TOKEN"
API_VALUES = {"image": {"tag": "test"}, "existingSecret": "regional-api-env"}


def test_chart_installs_api_and_proxy():
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
    (bootstrap,) = [entry for entry in env if entry["name"] == BOOTSTRAP_TOKEN_ENV]
    assert bootstrap == {
        "name": BOOTSTRAP_TOKEN_ENV,
        "valueFrom": {"secretKeyRef": {"name": "loco-bootstrap", "key": "value"}},
    }
    default = render("loco-control-plane", "api-deployment.yaml", {"api": API_VALUES})
    default_env = default["spec"]["template"]["spec"]["containers"][0]["env"]
    assert BOOTSTRAP_TOKEN_ENV not in [entry["name"] for entry in default_env]


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


CLICKHOUSE_PASSWORDS = {"loco_migrator": "migrator-pw", "loco_ingest": "ingest-pw", "loco_reader": "reader-pw"}
IN_CLUSTER = {
    "api": API_VALUES,
    "ui": {"enabled": False},
    "obsProxy": {"image": {"tag": "test"}, "ingest": {"enabled": True}},
    "clickhouse": {"enabled": True},
    "clickhouseUserPasswords": CLICKHOUSE_PASSWORDS,
}


def kinds(documents):
    return {document["kind"] for document in documents}


def by_kind(documents, kind):
    return {document["metadata"]["name"]: document for document in documents if document["kind"] == kind}


def secret_ref(deployment, name):
    env = {item["name"]: item for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"]}
    return env[name]["valueFrom"]["secretKeyRef"]


def test_in_cluster_clickhouse_wires_each_proxy_role_to_its_user():
    documents = render_chart("loco-control-plane", IN_CLUSTER)
    assert "ClickHouseInstallation" in kinds(documents)
    users = {user["name"]: user for user in chart_values("loco-control-plane")["clickhouse"]["clickhouse"]["users"]}
    secrets = by_kind(documents, "Secret")
    host = "clickhouse-loco-control-plane-clickhouse.platform-test.svc.cluster.local:9000"
    for name, user in users.items():
        secret = secrets[user["password_secret_name"]]["stringData"]
        assert secret == {
            "password": CLICKHOUSE_PASSWORDS[name],
            "url": f"clickhouse://{name}:{CLICKHOUSE_PASSWORDS[name]}@{host}",
        }
    deployments = by_kind(documents, "Deployment")
    query = deployments["loco-control-plane-obs-proxy-query"]
    ingest = deployments["loco-control-plane-obs-proxy-ingest"]
    for deployment, variable, user in (
        (query, "CLICKHOUSE_URL", "loco_reader"),
        (query, "CLICKHOUSE_MIGRATOR_URL", "loco_migrator"),
        (ingest, "CLICKHOUSE_INGEST_URL", "loco_ingest"),
    ):
        assert secret_ref(deployment, variable) == {"name": users[user]["password_secret_name"], "key": "url"}


def test_grants_name_the_database_value():
    clickhouse = chart_values("loco-control-plane")["clickhouse"]["clickhouse"]
    for user in clickhouse["users"]:
        for grant in user["grants"]:
            assert re.search(rf" ON {re.escape(clickhouse['database'])}\.\*$", grant), (user["name"], grant)


def test_external_clickhouse_uses_existing_secrets_and_renders_no_server():
    documents = render_chart(
        "loco-control-plane",
        {"api": API_VALUES, "ui": {"enabled": False}, "obsProxy": {**TEST_PROXY, "ingest": INGEST}, "certManager": {"enabled": False}},
    )
    assert not {"ClickHouseInstallation", "Secret"} & kinds(documents)
    assert "loco-control-plane-clickhouse" not in by_kind(documents, "NetworkPolicy")
    deployments = by_kind(documents, "Deployment")
    assert secret_ref(deployments["loco-control-plane-obs-proxy-query"], "CLICKHOUSE_URL") == {"name": "proxy-reader", "key": "url"}
    assert secret_ref(deployments["loco-control-plane-obs-proxy-ingest"], "CLICKHOUSE_INGEST_URL") == {"name": "proxy-ingest", "key": "url"}


def test_in_cluster_clickhouse_requires_the_proxy():
    result = render_chart("loco-control-plane", {**IN_CLUSTER, "obsProxy": {"enabled": False}}, check=False)
    assert result.returncode != 0
    assert "clickhouse.enabled requires obsProxy.enabled" in result.stderr
    documents = render_chart("loco-control-plane", {"api": API_VALUES, "ui": {"enabled": False}, "obsProxy": {"enabled": False}})
    assert "clickhouse" not in json.dumps(documents).lower()


def test_in_cluster_clickhouse_requires_dsn_safe_passwords(subtests):
    for passwords, message in (
        ({**CLICKHOUSE_PASSWORDS, "loco_reader": ""}, "clickhouseUserPasswords.loco_reader is required"),
        ({**CLICKHOUSE_PASSWORDS, "loco_reader": "a b@c"}, "may contain only letters"),
    ):
        with subtests.test(message=message):
            result = render_chart("loco-control-plane", {**IN_CLUSTER, "clickhouseUserPasswords": passwords}, check=False)
            assert result.returncode != 0
            assert message in result.stderr


def test_only_proxy_roles_and_the_operator_reach_clickhouse():
    documents = render_chart("loco-control-plane", IN_CLUSTER)
    policy = by_kind(documents, "NetworkPolicy")["loco-control-plane-clickhouse"]
    proxy, internal = policy["spec"]["ingress"]
    assert proxy == {
        "from": [{"podSelector": {"matchLabels": {"app.kubernetes.io/component": "observability-proxy"}}}],
        "ports": [{"protocol": "TCP", "port": 9000}],
    }
    deployments = by_kind(documents, "Deployment")
    for name in ("loco-control-plane-obs-proxy-query", "loco-control-plane-obs-proxy-ingest"):
        labels = deployments[name]["spec"]["template"]["metadata"]["labels"]
        assert labels["app.kubernetes.io/component"] == "observability-proxy", name
    operator = next(document for document in documents if document["kind"] == "Deployment" and "operator" in document["metadata"]["name"])
    operator_labels = operator["spec"]["template"]["metadata"]["labels"]
    selectors = [source["podSelector"] for source in internal["from"]]
    assert any(
        set(selector.get("matchLabels", {}).items()) <= set(operator_labels.items()) and selector.get("matchLabels")
        for selector in selectors
    )
    assert "loco-control-plane-api" not in json.dumps(policy)


def test_in_cluster_clickhouse_has_bounded_resources_and_a_single_node_keeper():
    installation = by_kind(render_chart("loco-control-plane", IN_CLUSTER), "ClickHouseInstallation")["loco-control-plane-clickhouse"]
    containers = installation["spec"]["templates"]["podTemplates"][0]["spec"]["containers"]
    resources = next(container["resources"] for container in containers if container["name"] == "clickhouse")
    for kind in ("requests", "limits"):
        assert set(resources[kind]) == {"cpu", "memory"}, kind
    assert quantity(resources["requests"]["memory"]) == quantity(resources["limits"]["memory"])
    config = installation["spec"]["configuration"]["files"]["config.d/extra_config.xml"]
    assert "<keeper_server>" in config
    assert "<keeper_map_path_prefix>" in config
    assert installation["spec"]["configuration"]["clusters"][0]["layout"]["replicasCount"] == 1
    assert installation["spec"]["configuration"]["users"]["default/networks/ip"] == "127.0.0.1/32"


GATEWAY_HOSTS = {"controlPlaneHostname": "api.region.test", "ui": {"image": {"tag": "test"}, "hostname": "app.region.test"}}


def gateway_listeners(overrides):
    documents = render_chart("loco-control-plane", {"api": API_VALUES, "obsProxy": TEST_PROXY, **GATEWAY_HOSTS, **overrides})
    gateways = by_kind(documents, "Gateway")
    return documents, gateways


PLAIN_HTTP = {"certManager": {"enabled": False}}


def test_chart_creates_its_own_gateway_for_its_hostnames():
    documents, gateways = gateway_listeners({"gatewayNamespace": "edge", **PLAIN_HTTP})
    gateway = gateways["loco-control-plane"]
    assert gateway["metadata"]["namespace"] == "edge"
    assert gateway["spec"]["gatewayClassName"] == "loco-control-plane"
    assert by_kind(documents, "GatewayClass")["loco-control-plane"]["spec"]["controllerName"] == "gateway.envoyproxy.io/gatewayclass-controller"
    assert [(listener["name"], listener["hostname"], listener["protocol"], listener["port"]) for listener in gateway["spec"]["listeners"]] == [
        ("http-ui", "app.region.test", "HTTP", 80),
        ("http-api", "api.region.test", "HTTP", 80),
    ]
    for listener in gateway["spec"]["listeners"]:
        assert listener["allowedRoutes"]["namespaces"]["selector"]["matchLabels"] == {"kubernetes.io/metadata.name": "platform-test"}
    for route in by_kind(documents, "HTTPRoute").values():
        assert route["spec"]["parentRefs"] == [{"name": "loco-control-plane", "namespace": "edge"}]
    proxy = by_kind(documents, "EnvoyProxy")["loco-control-plane"]
    assert gateway["spec"]["infrastructure"]["parametersRef"] == {"group": "gateway.envoyproxy.io", "kind": "EnvoyProxy", "name": "loco-control-plane"}
    assert "provider" not in proxy["spec"]
    assert not {"Certificate", "ClusterIssuer", "Secret"} & kinds(documents)


def test_gateway_takes_envoy_service_settings_and_a_tls_secret():
    service = {"type": "NodePort", "externalTrafficPolicy": "Cluster"}
    documents, gateways = gateway_listeners({"gateway": {"service": service, "tlsSecretName": "regional-tls"}})
    proxy = by_kind(documents, "EnvoyProxy")["loco-control-plane"]
    assert proxy["spec"]["provider"] == {"type": "Kubernetes", "kubernetes": {"envoyService": service}}
    for listener in gateways["loco-control-plane"]["spec"]["listeners"]:
        assert listener["protocol"] == "HTTPS"
        assert listener["port"] == 443
        assert listener["tls"]["certificateRefs"] == [{"name": "regional-tls", "kind": "Secret", "group": ""}]


def test_shared_hostname_gets_one_listener():
    _, gateways = gateway_listeners({"controlPlaneHostname": "app.region.test", **PLAIN_HTTP})
    assert [listener["hostname"] for listener in gateways["loco-control-plane"]["spec"]["listeners"]] == ["app.region.test"]


def test_existing_gateway_is_referenced_without_creating_one():
    documents, gateways = gateway_listeners({"gateway": {"create": False, "name": "eg"}})
    assert gateways == {}
    assert not {"GatewayClass", "EnvoyProxy"} & kinds(documents)
    for route in by_kind(documents, "HTTPRoute").values():
        assert route["spec"]["parentRefs"] == [{"name": "eg", "namespace": "loco-system"}]


def test_ingest_route_needs_ingest_and_a_hostname(subtests):
    for ingest, routed in (
        ({**INGEST, "hostname": "ingest.region.test"}, True),
        (INGEST, False),
        ({**INGEST, "enabled": False, "hostname": "ingest.region.test"}, False),
    ):
        with subtests.test(ingest=ingest):
            documents, gateways = gateway_listeners({"obsProxy": {**TEST_PROXY, "ingest": ingest}})
            routes = by_kind(documents, "HTTPRoute")
            hostnames = [listener["hostname"] for listener in gateways["loco-control-plane"]["spec"]["listeners"]]
            assert ("ingest.region.test" in hostnames) is routed
            assert ("loco-control-plane-obs-proxy-ingest" in routes) is routed
            if not routed:
                continue
            route = routes["loco-control-plane-obs-proxy-ingest"]["spec"]
            assert route["hostnames"] == ["ingest.region.test"]
            (rule,) = route["rules"]
            assert rule["matches"] == [{"path": {"type": "PathPrefix", "value": "/v1/"}}]
            assert rule["backendRefs"] == [{"name": "loco-control-plane-obs-proxy-ingest", "port": 4318}]
            policy = by_kind(documents, "NetworkPolicy")["loco-control-plane-obs-proxy-ingest"]
            gateway_rule = policy["spec"]["ingress"][1]
            assert gateway_rule["from"][0]["namespaceSelector"]["matchLabels"] == {"kubernetes.io/metadata.name": "loco-system"}
            assert gateway_rule["ports"] == [{"protocol": "TCP", "port": 4318}]


def test_ingest_route_is_off_by_default():
    assert chart_values("loco-control-plane")["obsProxy"]["ingest"]["hostname"] == ""
    assert chart_values("loco-control-plane")["obsProxy"]["ingest"]["enabled"] is False


CERT_MANAGER_KINDS = {"Certificate", "ClusterIssuer"}


def https_listeners(gateways):
    return gateways["loco-control-plane"]["spec"]["listeners"]


def test_default_render_issues_one_certificate_for_every_served_hostname():
    documents, gateways = gateway_listeners({"gatewayNamespace": "edge", "secrets": {"cloudflareToken": "token"}})
    (issuer,) = by_kind(documents, "ClusterIssuer").values()
    assert issuer["metadata"]["name"] == "letsencrypt-prod"
    assert issuer["spec"]["acme"]["email"] == "ops@region.test"
    assert issuer["spec"]["acme"]["solvers"][0]["dns01"]["cloudflare"]["apiTokenSecretRef"] == {"name": "cloudflare-api-token-secret", "key": "api-token"}
    (token,) = by_kind(documents, "Secret").values()
    assert token["metadata"]["namespace"] == "cert-manager"
    assert token["stringData"] == {"api-token": "token"}
    (certificate,) = by_kind(documents, "Certificate").values()
    assert certificate["metadata"]["namespace"] == "edge"
    assert certificate["spec"]["issuerRef"] == {"name": "letsencrypt-prod", "kind": "ClusterIssuer"}
    assert certificate["spec"]["dnsNames"] == ["app.region.test", "api.region.test"]
    secret = certificate["spec"]["secretName"]
    listeners = https_listeners(gateways)
    assert [listener["hostname"] for listener in listeners] == certificate["spec"]["dnsNames"]
    for listener in listeners:
        assert listener["protocol"] == "HTTPS"
        assert listener["tls"]["certificateRefs"] == [{"name": secret, "kind": "Secret", "group": ""}]


def test_certificate_covers_ingest_only_when_published():
    for ingest, published in (({**INGEST, "hostname": "ingest.region.test"}, True), (INGEST, False)):
        documents, gateways = gateway_listeners({"obsProxy": {**TEST_PROXY, "ingest": ingest}})
        (certificate,) = by_kind(documents, "Certificate").values()
        assert ("ingest.region.test" in certificate["spec"]["dnsNames"]) is published
        assert [listener["hostname"] for listener in https_listeners(gateways)] == certificate["spec"]["dnsNames"]


def test_self_signed_issuer_needs_no_token_or_email():
    documents, _ = gateway_listeners({"certManager": {"issuer": {"type": "selfSigned", "email": "", "name": "local"}}})
    (issuer,) = by_kind(documents, "ClusterIssuer").values()
    assert issuer["spec"] == {"selfSigned": {}}
    (certificate,) = by_kind(documents, "Certificate").values()
    assert certificate["spec"]["issuerRef"]["name"] == "local"
    assert "Secret" not in kinds(documents)


def test_existing_issuer_renders_only_the_certificate():
    documents, _ = gateway_listeners({"certManager": {"issuer": {"create": False, "name": "shared", "kind": "Issuer"}}})
    assert kinds(documents) & CERT_MANAGER_KINDS == {"Certificate"}
    assert "Secret" not in kinds(documents)
    (certificate,) = by_kind(documents, "Certificate").values()
    assert certificate["spec"]["issuerRef"] == {"name": "shared", "kind": "Issuer"}


def test_acme_issuer_requires_an_email():
    result = render_chart("loco-control-plane", {"api": API_VALUES, "obsProxy": TEST_PROXY, **GATEWAY_HOSTS, "certManager": {"issuer": {"email": ""}}}, check=False)
    assert result.returncode != 0
    assert "certManager.issuer.email is required" in result.stderr


def test_existing_tls_secret_renders_no_cert_manager_objects():
    documents, gateways = gateway_listeners({"gateway": {"tlsSecretName": "regional-tls"}})
    assert not CERT_MANAGER_KINDS & kinds(documents)
    assert "Secret" not in kinds(documents)
    for listener in https_listeners(gateways):
        assert listener["tls"]["certificateRefs"][0]["name"] == "regional-tls"


def test_plain_http_renders_http_listeners_without_cert_manager_objects():
    documents, gateways = gateway_listeners(PLAIN_HTTP)
    assert not CERT_MANAGER_KINDS & kinds(documents)
    for listener in https_listeners(gateways):
        assert (listener["protocol"], listener["port"]) == ("HTTP", 80)
        assert "tls" not in listener


def test_existing_gateway_renders_no_certificate():
    documents, _ = gateway_listeners({"gateway": {"create": False, "name": "eg"}})
    assert not CERT_MANAGER_KINDS & kinds(documents)
