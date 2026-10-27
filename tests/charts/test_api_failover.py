import runpy

import pytest

from helpers import API_VALUES, ROOT, TEST_PROXY, render_chart

PRE_REQUEST_FAILURES = {"connect-failure", "reset-before-request", "refused-stream"}


@pytest.mark.parametrize(
    ("enabled", "hostname", "expected_routes"),
    [
        (False, "dashboard.test", {"loco-control-plane-api"}),
        (True, "api.test", {"loco-control-plane-ui"}),
        (True, "dashboard.test", {"loco-control-plane-api", "loco-control-plane-ui"}),
    ],
)
def test_only_api_rules_retry_connections(enabled, hostname, expected_routes):
    documents = render_chart(
        "loco-control-plane",
        {
            "controlPlaneHostname": "api.test",
            "api": API_VALUES,
            "obsProxy": TEST_PROXY,
            "ui": {"enabled": enabled, "hostname": hostname, "image": {"tag": "test"}},
        },
    )
    routes = {
        document["metadata"]["name"]: document
        for document in documents
        if document["kind"] == "HTTPRoute"
    }
    assert set(routes) == expected_routes
    (policy,) = [
        document for document in documents if document["kind"] == "BackendTrafficPolicy"
    ]
    assert policy["spec"]["targetRefs"] == [
        {
            "group": "gateway.networking.k8s.io",
            "kind": "HTTPRoute",
            "name": name,
            "sectionName": "api",
        }
        for name in sorted(expected_routes)
    ]
    retry_on = policy["spec"]["retry"]["retryOn"]
    assert retry_on.keys() == {"triggers"}
    assert retry_on["triggers"]
    assert set(retry_on["triggers"]) <= PRE_REQUEST_FAILURES
    for route in routes.values():
        for rule in route["spec"]["rules"]:
            if rule["backendRefs"][0]["name"] == "loco-control-plane-api":
                assert rule["name"] == "api"
                assert rule["timeouts"] == {"request": "0s"}
            else:
                assert rule.get("name") != "api"


def test_retry_stats_distinguish_dashboard_host_from_embedded_api_host():
    driver = runpy.run_path(str(ROOT / "tests/e2e/api-failover.py"))
    hosts = ["api.test.local", "ui-api.test.local"]
    configs = {
        host: {
            "name": "httproute/namespace/route/rule/0/match/0/" + host.replace(".", "_"),
            "route": {"cluster": host},
            "metadata": {"filter_metadata": {"envoy-gateway": {"resources": [{"sectionName": "api"}]}}},
        }
        for host in reversed(hosts)
    }
    assert driver["api_clusters"](configs, hosts) == {host: host for host in hosts}
