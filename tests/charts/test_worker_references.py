from helpers import collector_relays, render_chart


def test_the_collector_parses_routes_in_the_controllers_workspace_namespaces():
    prefix = "tenant-"
    documents = render_chart("loco-worker", {"global": {"workspaceNamespacePrefix": prefix}})
    statements = collector_relays(documents)["otel-col-deploy"]["processors"]["transform/envoy_tenancy"]["trace_statements"][1]["statements"]
    (parse,) = [statement for statement in statements if "ExtractPatterns" in statement]
    assert f'"^httproute/{prefix}(?P<workspace>' in parse
