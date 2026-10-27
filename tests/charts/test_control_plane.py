import json
import re
import subprocess

import pytest

from helpers import (
    API_VALUES,
    TEST_PROXY,
    by_kind,
    chart_values,
    container_env,
    kinds,
    quantity,
    render_chart,
    render_template,
    render_template_object,
)

BOOTSTRAP_TOKEN_ENV = "LOCO_BOOTSTRAP_TOKEN"


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
    assert {"loco-control-plane-api", "loco-control-plane-obs-proxy-query"} <= deployments
    routes = [document for document in documents if document["kind"] == "HTTPRoute"]
    assert any(
        route["spec"]["rules"][0]["backendRefs"][0]["name"] == "loco-control-plane-api"
        for route in routes
    )


@pytest.fixture(scope="module")
def api_pod():
    deployment = render_template_object(
        "loco-control-plane",
        "api-deployment.yaml",
        {"api": {**API_VALUES, "port": 9000, "env": {"APP_ENV": "production"}}},
    )
    return deployment["spec"]["template"]["spec"]


def _api_container(api_pod):
    (api,) = api_pod["containers"]
    return api


def _api_env(api_pod):
    return {item["name"]: item["value"] for item in _api_container(api_pod)["env"]}


def test_api_gets_no_service_account_token(api_pod):
    assert api_pod["automountServiceAccountToken"] is False


def test_api_reads_its_environment_from_the_existing_secret(api_pod):
    assert _api_container(api_pod)["envFrom"] == [{"secretRef": {"name": "regional-api-env"}}]


def test_api_listens_on_the_configured_port(api_pod):
    assert _api_env(api_pod)["APP_PORT"] == "0.0.0.0:9000"


def test_api_passes_values_env_through(api_pod):
    assert _api_env(api_pod)["APP_ENV"] == "production"


def test_api_reaches_the_private_proxy(api_pod):
    assert (
        _api_env(api_pod)["OBSERVABILITY_PROXY_URL"]
        == "http://loco-control-plane-obs-proxy-query.platform-test.svc.cluster.local"
    )


def test_api_startup_probe_follows_values(api_pod):
    assert _api_container(api_pod)["startupProbe"] == {
        "httpGet": {"path": "/health", "port": "http"},
        **chart_values("loco-control-plane")["api"]["startupProbe"],
    }


def test_api_requires_an_operator_secret():
    result = render_chart("loco-control-plane", {"api": {"image": {"tag": "test"}}, "obsProxy": TEST_PROXY}, check=False)
    assert result.returncode != 0
    assert "at '/api/existingSecret'" in result.stderr


def test_chart_owns_port_and_proxy_wiring():
    deployment = render_template_object(
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
    for name in ("APP_PORT", "OBSERVABILITY_PROXY_URL"):
        (entry,) = [entry for entry in env if entry["name"] == name]
        assert entry["value"] != "wrong", name


def test_api_reads_bootstrap_token_from_existing_secret():
    deployment = render_template_object(
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
    default = render_template_object("loco-control-plane", "api-deployment.yaml", {"api": API_VALUES})
    default_env = default["spec"]["template"]["spec"]["containers"][0]["env"]
    assert BOOTSTRAP_TOKEN_ENV not in [entry["name"] for entry in default_env]


def test_only_api_can_reach_private_proxy():
    policy = render_template_object("loco-control-plane", "obs-proxy-query-network-policy.yaml", {})
    (rule,) = policy["spec"]["ingress"]
    assert rule["from"] == [
        {"podSelector": {"matchLabels": {"app": "loco-control-plane-api"}}}
    ]
    assert rule["ports"] == [{"protocol": "TCP", "port": 8080}]


def test_gateway_and_proxy_can_reach_api():
    policy = render_template_object(
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
    documents = render_template(
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
    routes = render_template(
        "loco-control-plane",
        "api-route.yaml",
        {
            "controlPlaneHostname": "loco.example.test",
            "ui": {"hostname": "loco.example.test"},
        },
    )
    assert routes == []


@pytest.fixture(scope="module")
def proxyless():
    return render_chart(
        "loco-control-plane",
        {"api": API_VALUES, "obsProxy": {"enabled": False}, "ui": {"enabled": False}},
    )


def test_disabled_proxy_renders_no_proxy_objects(proxyless):
    names = {document["metadata"]["name"] for document in proxyless}
    assert not any("obs-proxy" in name for name in names)


def test_disabled_proxy_leaves_the_api_without_a_proxy_url(proxyless):
    (api,) = [
        document
        for document in proxyless
        if document["metadata"]["name"] == "loco-control-plane-api"
        and document["kind"] == "Deployment"
    ]
    env = api["spec"]["template"]["spec"]["containers"][0]["env"]
    assert "OBSERVABILITY_PROXY_URL" not in [entry["name"] for entry in env]


def test_disabled_proxy_leaves_the_api_policy_without_proxy_sources(proxyless):
    (policy,) = [
        document
        for document in proxyless
        if document["kind"] == "NetworkPolicy"
        and document["metadata"]["name"] == "loco-control-plane-api"
    ]
    (rule,) = policy["spec"]["ingress"]
    assert "obs-proxy" not in json.dumps(rule["from"])


def test_disabled_proxy_renders_no_clickhouse(proxyless):
    assert "clickhouse" not in json.dumps(proxyless).lower()


INGEST = {
    "enabled": True,
    "clickhouse": {"existingSecret": {"name": "proxy-ingest", "key": "url"}},
}


@pytest.fixture(scope="module")
def ingest_chart():
    return render_chart(
        "loco-control-plane",
        {
            "api": API_VALUES,
            "obsProxy": {**TEST_PROXY, "ingest": INGEST},
            "ui": {"enabled": False},
        },
    )


def _ingest_container(ingest_chart):
    deployment = by_kind(ingest_chart, "Deployment")["loco-control-plane-obs-proxy-ingest"]
    (container,) = deployment["spec"]["template"]["spec"]["containers"]
    return container


def _ingest_env(ingest_chart):
    return {item["name"]: item for item in _ingest_container(ingest_chart)["env"]}


def _ingest_ports(ingest_chart):
    return {port["name"]: port["containerPort"] for port in _ingest_container(ingest_chart)["ports"]}


def test_ingest_deployment_runs_only_the_ingest_role(ingest_chart):
    env = _ingest_env(ingest_chart)
    assert env["QUERY_ENABLED"]["value"] == "false"
    assert env["INGEST_ENABLED"]["value"] == "true"


def test_ingest_reads_only_its_own_clickhouse_secret(ingest_chart):
    env = _ingest_env(ingest_chart)
    assert env["CLICKHOUSE_INGEST_URL"]["valueFrom"]["secretKeyRef"] == {
        "name": "proxy-ingest",
        "key": "url",
    }
    assert not {"CLICKHOUSE_URL", "CLICKHOUSE_MIGRATOR_URL", "CONTROL_PLANE_URL"} & set(env)


def test_ingest_listens_on_its_container_ports(ingest_chart):
    env = _ingest_env(ingest_chart)
    ports = _ingest_ports(ingest_chart)
    assert env["PORT"]["value"] == str(ports["health"])
    assert env["OTLP_GRPC_PORT"]["value"] == str(ports["otlp-grpc"])
    assert env["OTLP_HTTP_PORT"]["value"] == str(ports["otlp-http"])


def test_ingest_readiness_checks_readyz_on_the_health_port(ingest_chart):
    assert _ingest_container(ingest_chart)["readinessProbe"]["httpGet"] == {"path": "/readyz", "port": "health"}


def test_each_proxy_service_selects_only_its_own_deployment(ingest_chart):
    deployments = by_kind(ingest_chart, "Deployment")
    services = by_kind(ingest_chart, "Service")
    for name in ("loco-control-plane-obs-proxy-ingest", "loco-control-plane-obs-proxy-query"):
        selector = services[name]["spec"]["selector"].items()
        selected = [other for other, deployment in deployments.items() if selector <= deployment["spec"]["template"]["metadata"]["labels"].items()]
        assert selected == [name]


def test_ingest_service_exposes_only_otlp(ingest_chart):
    ingest = by_kind(ingest_chart, "Service")["loco-control-plane-obs-proxy-ingest"]["spec"]
    assert {port["targetPort"] for port in ingest["ports"]} == {"otlp-grpc", "otlp-http"}


def test_ingest_policy_admits_otlp_from_any_pod(ingest_chart):
    ports = _ingest_ports(ingest_chart)
    (policy,) = [
        document
        for document in ingest_chart
        if document["kind"] == "NetworkPolicy"
        and document["metadata"]["name"] == "loco-control-plane-obs-proxy-ingest"
    ]
    (rule,) = policy["spec"]["ingress"]
    assert rule["from"] == [{"podSelector": {}}]
    assert {port["port"] for port in rule["ports"]} == {ports["otlp-grpc"], ports["otlp-http"]}


def test_ingest_is_off_by_default():
    documents = render_template(
        "loco-control-plane", "obs-proxy-ingest-deployment.yaml", {"obsProxy": TEST_PROXY}
    )
    assert documents == []


def test_ingest_requires_its_secret():
    with pytest.raises(subprocess.CalledProcessError) as caught:
        render_template_object(
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


def secret_ref(deployment, name):
    return container_env(deployment)[name]["valueFrom"]["secretKeyRef"]


def _clickhouse_users():
    return {user["name"]: user for user in chart_values("loco-control-plane")["clickhouse"]["clickhouse"]["users"]}


@pytest.fixture(scope="module")
def in_cluster():
    return render_chart("loco-control-plane", IN_CLUSTER)


def test_in_cluster_clickhouse_renders_an_installation(in_cluster):
    assert "ClickHouseInstallation" in kinds(in_cluster)


def test_in_cluster_clickhouse_renders_a_dsn_secret_for_each_user(in_cluster):
    secrets = by_kind(in_cluster, "Secret")
    host = "clickhouse-loco-control-plane-clickhouse.platform-test.svc.cluster.local:9000"
    for name, user in _clickhouse_users().items():
        secret = secrets[user["password_secret_name"]]["stringData"]
        assert secret == {
            "password": CLICKHOUSE_PASSWORDS[name],
            "url": f"clickhouse://{name}:{CLICKHOUSE_PASSWORDS[name]}@{host}",
        }


def test_in_cluster_clickhouse_wires_each_proxy_role_to_its_user(in_cluster):
    users = _clickhouse_users()
    deployments = by_kind(in_cluster, "Deployment")
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


@pytest.fixture(scope="module")
def external_clickhouse():
    return render_chart(
        "loco-control-plane",
        {"api": API_VALUES, "ui": {"enabled": False}, "obsProxy": {**TEST_PROXY, "ingest": INGEST}, "certManager": {"enabled": False}},
    )


def test_external_clickhouse_renders_no_server(external_clickhouse):
    assert not {"ClickHouseInstallation", "Secret"} & kinds(external_clickhouse)
    assert "loco-control-plane-clickhouse" not in by_kind(external_clickhouse, "NetworkPolicy")


def test_external_clickhouse_proxies_read_the_existing_secrets(external_clickhouse):
    deployments = by_kind(external_clickhouse, "Deployment")
    assert secret_ref(deployments["loco-control-plane-obs-proxy-query"], "CLICKHOUSE_URL") == {"name": "proxy-reader", "key": "url"}
    assert secret_ref(deployments["loco-control-plane-obs-proxy-ingest"], "CLICKHOUSE_INGEST_URL") == {"name": "proxy-ingest", "key": "url"}


def test_in_cluster_clickhouse_requires_the_proxy():
    result = render_chart("loco-control-plane", {**IN_CLUSTER, "obsProxy": {"enabled": False}}, check=False)
    assert result.returncode != 0
    assert "clickhouse.enabled requires obsProxy.enabled" in result.stderr


def test_in_cluster_clickhouse_requires_dsn_safe_passwords(subtests):
    for passwords, message in (
        ({**CLICKHOUSE_PASSWORDS, "loco_reader": ""}, "clickhouseUserPasswords.loco_reader is required"),
        ({**CLICKHOUSE_PASSWORDS, "loco_reader": "a b@c"}, "may contain only letters"),
    ):
        with subtests.test(message=message):
            result = render_chart("loco-control-plane", {**IN_CLUSTER, "clickhouseUserPasswords": passwords}, check=False)
            assert result.returncode != 0
            assert message in result.stderr


def test_only_proxy_roles_and_the_operator_reach_clickhouse(in_cluster):
    policy = by_kind(in_cluster, "NetworkPolicy")["loco-control-plane-clickhouse"]
    proxy, internal = policy["spec"]["ingress"]
    assert proxy == {
        "from": [{"podSelector": {"matchLabels": {"app.kubernetes.io/component": "observability-proxy"}}}],
        "ports": [{"protocol": "TCP", "port": 9000}],
    }
    deployments = by_kind(in_cluster, "Deployment")
    for name in ("loco-control-plane-obs-proxy-query", "loco-control-plane-obs-proxy-ingest"):
        labels = deployments[name]["spec"]["template"]["metadata"]["labels"]
        assert labels["app.kubernetes.io/component"] == "observability-proxy", name
    operator = next(document for document in in_cluster if document["kind"] == "Deployment" and "operator" in document["metadata"]["name"])
    operator_labels = operator["spec"]["template"]["metadata"]["labels"]
    selectors = [source["podSelector"] for source in internal["from"]]
    assert any(
        set(selector.get("matchLabels", {}).items()) <= set(operator_labels.items()) and selector.get("matchLabels")
        for selector in selectors
    )
    assert "loco-control-plane-api" not in json.dumps(policy)


def _installation(in_cluster):
    return by_kind(in_cluster, "ClickHouseInstallation")["loco-control-plane-clickhouse"]


def test_in_cluster_clickhouse_has_bounded_resources(in_cluster):
    containers = _installation(in_cluster)["spec"]["templates"]["podTemplates"][0]["spec"]["containers"]
    resources = next(container["resources"] for container in containers if container["name"] == "clickhouse")
    for kind in ("requests", "limits"):
        assert set(resources[kind]) == {"cpu", "memory"}, kind
    assert quantity(resources["requests"]["memory"]) == quantity(resources["limits"]["memory"])


def test_in_cluster_clickhouse_runs_a_single_node_keeper(in_cluster):
    configuration = _installation(in_cluster)["spec"]["configuration"]
    config = configuration["files"]["config.d/extra_config.xml"]
    assert "<keeper_server>" in config
    assert "<keeper_map_path_prefix>" in config
    assert configuration["clusters"][0]["layout"]["replicasCount"] == 1


def test_in_cluster_clickhouse_default_user_is_local_only(in_cluster):
    assert _installation(in_cluster)["spec"]["configuration"]["users"]["default/networks/ip"] == "127.0.0.1/32"


GATEWAY_HOSTS = {"controlPlaneHostname": "api.region.test", "ui": {"image": {"tag": "test"}, "hostname": "app.region.test"}}


def gateway_listeners(overrides):
    documents = render_chart("loco-control-plane", {"api": API_VALUES, "obsProxy": TEST_PROXY, **GATEWAY_HOSTS, **overrides})
    gateways = by_kind(documents, "Gateway")
    return documents, gateways


PLAIN_HTTP = {"certManager": {"enabled": False}}
GATEWAY_SERVICE = {"type": "NodePort", "externalTrafficPolicy": "Cluster"}


def test_configured_solvers_render_verbatim_without_a_cloudflare_secret():
    solvers = [{"dns01": {"route53": {"region": "us-east-1"}}}]
    documents, _ = gateway_listeners({"certManager": {"issuer": {"solvers": solvers}}})
    (issuer,) = by_kind(documents, "ClusterIssuer").values()
    assert issuer["spec"]["acme"]["solvers"] == solvers
    assert "cloudflare-api-token-secret" not in by_kind(documents, "Secret")


@pytest.fixture(scope="module")
def own_gateway():
    return gateway_listeners({"gatewayNamespace": "edge", **PLAIN_HTTP})


def test_chart_creates_its_gateway_in_the_gateway_namespace(own_gateway):
    documents, gateways = own_gateway
    gateway = gateways["loco-control-plane"]
    assert gateway["metadata"]["namespace"] == "edge"
    assert gateway["spec"]["gatewayClassName"] == "loco-control-plane"
    assert by_kind(documents, "GatewayClass")["loco-control-plane"]["spec"]["controllerName"] == "gateway.envoyproxy.io/gatewayclass-controller"


def test_gateway_listens_on_each_hostname(own_gateway):
    _, gateways = own_gateway
    assert {(listener["hostname"], listener["protocol"], listener["port"]) for listener in gateways["loco-control-plane"]["spec"]["listeners"]} == {
        ("app.region.test", "HTTP", 80),
        ("api.region.test", "HTTP", 80),
    }


def test_gateway_admits_routes_only_from_the_release_namespace(own_gateway):
    _, gateways = own_gateway
    for listener in gateways["loco-control-plane"]["spec"]["listeners"]:
        assert listener["allowedRoutes"]["namespaces"]["selector"]["matchLabels"] == {"kubernetes.io/metadata.name": "platform-test"}


def test_routes_attach_to_the_chart_gateway(own_gateway):
    documents, _ = own_gateway
    for route in by_kind(documents, "HTTPRoute").values():
        assert route["spec"]["parentRefs"] == [{"name": "loco-control-plane", "namespace": "edge"}]


def test_gateway_runs_on_the_chart_envoy_proxy_with_default_service_settings(own_gateway):
    documents, gateways = own_gateway
    proxy = by_kind(documents, "EnvoyProxy")["loco-control-plane"]
    assert gateways["loco-control-plane"]["spec"]["infrastructure"]["parametersRef"] == {"group": "gateway.envoyproxy.io", "kind": "EnvoyProxy", "name": "loco-control-plane"}
    assert "provider" not in proxy["spec"]


def test_plain_http_gateway_renders_no_certificates(own_gateway):
    documents, _ = own_gateway
    assert not {"Certificate", "ClusterIssuer", "Secret"} & kinds(documents)


@pytest.fixture(scope="module")
def serviced_gateway():
    return gateway_listeners({"gateway": {"service": GATEWAY_SERVICE, "tlsSecretName": "regional-tls"}})


def test_gateway_takes_envoy_service_settings(serviced_gateway):
    documents, _ = serviced_gateway
    proxy = by_kind(documents, "EnvoyProxy")["loco-control-plane"]
    assert proxy["spec"]["provider"] == {"type": "Kubernetes", "kubernetes": {"envoyService": GATEWAY_SERVICE}}


def test_gateway_serves_https_from_the_tls_secret(serviced_gateway):
    _, gateways = serviced_gateway
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


@pytest.fixture(scope="module")
def default_certificates():
    return gateway_listeners({"gatewayNamespace": "edge", "secrets": {"cloudflareToken": "token"}})


def _one_of_kind(documents, kind):
    (document,) = by_kind(documents, kind).values()
    return document


def test_default_issuer_is_acme_with_the_operator_email(default_certificates):
    documents, _ = default_certificates
    assert _one_of_kind(documents, "ClusterIssuer")["spec"]["acme"]["email"] == "ops@region.test"


def test_default_issuer_reads_the_rendered_cloudflare_token(default_certificates):
    documents, _ = default_certificates
    issuer = _one_of_kind(documents, "ClusterIssuer")
    reference = issuer["spec"]["acme"]["solvers"][0]["dns01"]["cloudflare"]["apiTokenSecretRef"]
    token = _one_of_kind(documents, "Secret")
    assert reference["name"] == token["metadata"]["name"]
    assert token["metadata"]["namespace"] == "cert-manager"
    assert token["stringData"][reference["key"]] == "token"


def test_default_certificate_lives_in_the_gateway_namespace(default_certificates):
    documents, _ = default_certificates
    assert _one_of_kind(documents, "Certificate")["metadata"]["namespace"] == "edge"


def test_default_certificate_names_the_rendered_issuer(default_certificates):
    documents, _ = default_certificates
    issuer = _one_of_kind(documents, "ClusterIssuer")
    certificate = _one_of_kind(documents, "Certificate")
    assert certificate["spec"]["issuerRef"] == {"name": issuer["metadata"]["name"], "kind": issuer["kind"]}


def test_default_certificate_covers_every_listener_hostname(default_certificates):
    documents, gateways = default_certificates
    certificate = _one_of_kind(documents, "Certificate")
    assert certificate["spec"]["dnsNames"] == ["app.region.test", "api.region.test"]
    assert [listener["hostname"] for listener in https_listeners(gateways)] == certificate["spec"]["dnsNames"]


def test_default_listeners_serve_https_from_the_issued_secret(default_certificates):
    documents, gateways = default_certificates
    secret = _one_of_kind(documents, "Certificate")["spec"]["secretName"]
    for listener in https_listeners(gateways):
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
