import pytest
from helpers import chart_values, post_otlp, read_output, run_collector

GATEWAY_LABEL = "gateway.envoyproxy.io/owning-gateway-name"
FORGED = {
    "loco.organization.id": "forged-organization",
    "loco.workspace.id": "forged-workspace",
    "loco.environment.id": "forged-environment",
    "loco.resource.id": "forged-resource",
}
ROUTE_TENANCY = ("loco.resource.id", "k8s.namespace.name")
TIME = "1700000000000000000"


def namespace():
    return chart_values("loco-worker")["global"]["workspaceNamespacePrefix"] + "env-a"


def attributes(values):
    return [
        {"key": key, "value": {"stringValue": value}} for key, value in values.items()
    ]


def flatten(values):
    return {item["key"]: item["value"].get("stringValue") for item in values}


# Each signal: the attribute Envoy names the upstream cluster in, the payload wrapping
# one record per resource, and the records the file exporter wrote, with their resource.
SIGNALS = {
    "traces": (
        "upstream_cluster",
        lambda resource, record: {
            "resourceSpans": [
                {
                    "resource": resource,
                    "scopeSpans": [
                        {
                            "spans": [
                                {
                                    "traceId": "0123456789abcdef0123456789abcdef",
                                    "spanId": "0123456789abcdef",
                                    "name": "route",
                                    "startTimeUnixNano": TIME,
                                    "endTimeUnixNano": TIME,
                                    "attributes": record,
                                }
                            ]
                        }
                    ],
                }
            ]
        },
        lambda document: [
            (resource["resource"], span)
            for resource in document["resourceSpans"]
            for scope in resource["scopeSpans"]
            for span in scope["spans"]
        ],
    ),
    "logs": (
        "upstream_cluster",
        lambda resource, record: {
            "resourceLogs": [
                {
                    "resource": resource,
                    "scopeLogs": [
                        {
                            "logRecords": [
                                {"body": {"stringValue": "route"}, "attributes": record}
                            ]
                        }
                    ],
                }
            ]
        },
        lambda document: [
            (resource["resource"], log)
            for resource in document["resourceLogs"]
            for scope in resource["scopeLogs"]
            for log in scope["logRecords"]
        ],
    ),
    "metrics": (
        "envoy.cluster_name",
        lambda resource, record: {
            "resourceMetrics": [
                {
                    "resource": resource,
                    "scopeMetrics": [
                        {
                            "metrics": [
                                {
                                    "name": "route",
                                    "gauge": {
                                        "dataPoints": [
                                            {
                                                "asInt": "1",
                                                "timeUnixNano": TIME,
                                                "attributes": record,
                                            }
                                        ]
                                    },
                                }
                            ]
                        }
                    ],
                }
            ]
        },
        lambda document: [
            (resource["resource"], point)
            for resource in document["resourceMetrics"]
            for scope in resource["scopeMetrics"]
            for metric in scope["metrics"]
            for point in metric["gauge"]["dataPoints"]
        ],
    ),
}


@pytest.fixture(scope="module", params=list(SIGNALS))
def route_records(request, worker_relays, tmp_path_factory):
    """Send one record from a gateway and one from another pod, both naming a route and forging tenancy."""
    signal = request.param
    cluster, payload, records = SIGNALS[signal]
    transform = worker_relays["workload-collector"]["processors"][
        "transform/envoy_tenancy"
    ]
    config = {
        "receivers": {"otlp": {"protocols": {"http": {"endpoint": "0.0.0.0:4318"}}}},
        "processors": {"transform/envoy_tenancy": transform},
        "exporters": {"file": {"path": "/data/output.json", "flush_interval": "100ms"}},
        "service": {
            "pipelines": {
                signal: {
                    "receivers": ["otlp"],
                    "processors": ["transform/envoy_tenancy"],
                    "exporters": ["file"],
                }
            }
        },
    }
    record = attributes(
        {**FORGED, cluster: f"httproute/{namespace()}/resource-resource-a-route/rule/0"}
    )
    directory = tmp_path_factory.mktemp(signal)
    with run_collector(directory, config, "workload-collector") as collector:
        for resource in ({GATEWAY_LABEL: "loco"}, {"service.name": "dependency"}):
            post_otlp(
                collector, signal, payload({"attributes": attributes(resource)}, record)
            )
        received = read_output(collector, records, 2)
    return {
        "gateway" if GATEWAY_LABEL in flatten(resource["attributes"]) else "other": (
            flatten(item["attributes"])
        )
        for resource, item in received
    }


def test_client_sent_tenancy_keys_are_deleted(route_records):
    for source, record in route_records.items():
        assert not FORGED.items() & record.items(), (source, record)


def test_gateway_telemetry_gets_the_route_resource_and_namespace(route_records):
    assert (
        route_records["gateway"].items()
        >= {"loco.resource.id": "resource-a", "k8s.namespace.name": namespace()}.items()
    ), route_records["gateway"]


def test_other_telemetry_gets_no_route_tenancy(route_records):
    assert not set(ROUTE_TENANCY) & route_records["other"].keys(), route_records[
        "other"
    ]
