import runpy

from helpers import ROOT


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
