import runpy

import pytest
from test_control_plane import API_VALUES
from test_platform_contracts import ROOT, TEST_PROXY, render_chart


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
    assert policy["spec"] == {
        "targetRefs": [
            {
                "group": "gateway.networking.k8s.io",
                "kind": "HTTPRoute",
                "name": name,
                "sectionName": "api",
            }
            for name in sorted(expected_routes)
        ],
        "retry": {
            "numRetries": 1,
            "retryOn": {
                "triggers": ["connect-failure", "reset-before-request", "refused-stream"]
            },
        },
    }
    for route in routes.values():
        for rule in route["spec"]["rules"]:
            if rule["backendRefs"][0]["name"] == "loco-control-plane-api":
                assert rule["name"] == "api"
                assert rule["timeouts"] == {"request": "0s"}
            else:
                assert rule.get("name") != "api"


def test_retry_stats_distinguish_dashboard_host_from_embedded_api_host():
    driver = runpy.run_path(str(ROOT / "e2e/api-failover.py"))
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
