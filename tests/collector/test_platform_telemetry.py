import json
import re

import pytest
from helpers import (
    CI_VALUES,
    RELEASE_NAMESPACE,
    chart_values,
    collector_relays,
    post_otlp,
    read_output,
    render_chart,
    run_collector,
)

IDENTITY = {
    "enabled": True,
    "installation": "test-installation",
    "cluster": "test-cluster",
    "environment": "test",
}


def configured(**settings):
    return {"global": {"platformTelemetry": {**IDENTITY, **settings}}}


@pytest.fixture(scope="module")
def configured_relays():
    return collector_relays(render_chart("loco-worker", configured()))


def test_platform_collection_is_opt_in(worker_relays):
    for config in worker_relays.values():
        assert "metrics/platform" not in config["service"]["pipelines"]


def test_platform_metrics_share_the_cluster_exporter(configured_relays):
    for name in ("otel-col-daemon-agent", "otel-col-cluster"):
        config = configured_relays[name]
        pipeline = config["service"]["pipelines"]["metrics/platform"]
        assert pipeline["exporters"] == ["otlp_http"]
        assert config["service"]["pipelines"]["metrics"]["exporters"] == ["otlp_http"]
        assert "filter/platform" in pipeline["processors"]
        assert "resource/platform" in pipeline["processors"]
    assert (
        "metrics/platform"
        not in configured_relays["otel-col-deploy"]["service"]["pipelines"]
    )


def test_platform_only_keeps_only_platform_pipelines():
    values = configured(platformOnly=True)
    values.update({"otel-col-deploy": {"enabled": False}})
    configs = collector_relays(render_chart("loco-worker", values))
    for config in configs.values():
        assert set(config["service"]["pipelines"]) <= {
            "metrics/platform",
            "metrics/capacity",
            "logs/platform",
            "traces/platform",
        }
        assert set(config["exporters"]) == {"otlp_http"}


def test_application_pipelines_keep_their_configuration(
    worker_relays, configured_relays
):
    for name, config in worker_relays.items():
        for pipeline, settings in config["service"]["pipelines"].items():
            assert configured_relays[name]["service"]["pipelines"][pipeline] == settings


def test_platform_identity_is_exported(configured_relays):
    for name in ("otel-col-daemon-agent", "otel-col-cluster"):
        attributes = {
            item["key"]: item["value"]
            for item in configured_relays[name]["processors"]["resource/platform"][
                "attributes"
            ]
        }
        assert (
            attributes.items()
            >= {
                "loco.installation.id": "test-installation",
                "k8s.cluster.name": "test-cluster",
                "deployment.environment.name": "test",
                "loco.region": chart_values("loco-worker", CI_VALUES)["global"][
                    "region"
                ],
                "loco.telemetry.scope": "platform",
            }.items()
        )


def test_platform_capacity_follows_the_configured_reports(configured_relays):
    settings = chart_values("loco-worker")["global"]["platformTelemetry"]
    receiver = configured_relays["otel-col-cluster"]["receivers"]["k8s_cluster"]
    assert receiver["node_conditions_to_report"] == settings["nodeConditions"]
    assert receiver["allocatable_types_to_report"] == settings["allocatableTypes"]


def test_changing_platform_settings_restarts_the_collectors():
    checksums = []
    for interval in ("30s", "60s"):
        workloads = [
            document
            for document in render_chart(
                "loco-worker", configured(collectionInterval=interval)
            )
            if document["kind"] in ("DaemonSet", "StatefulSet")
            and document["metadata"]["name"]
            in ("otel-col-daemon-agent", "otel-col-cluster")
        ]
        checksums.append(
            {
                document["metadata"]["name"]: document["spec"]["template"]["metadata"][
                    "annotations"
                ]["checksum/platform-telemetry"]
                for document in workloads
            }
        )
        assert {
            (document["kind"], document["metadata"]["name"]) for document in workloads
        } == {
            ("DaemonSet", "otel-col-daemon-agent"),
            ("StatefulSet", "otel-col-cluster"),
        }
    assert all(checksums[0][name] != checksums[1][name] for name in checksums[0])


def test_disabling_an_individual_collector_still_renders():
    overrides = configured()
    overrides["otel-col-daemon"] = {"enabled": False}
    configs = collector_relays(render_chart("loco-worker", overrides))
    assert "otel-col-daemon-agent" not in configs
    assert "metrics/platform" in configs["otel-col-cluster"]["service"]["pipelines"]


def test_platform_allowlist_covers_the_release_and_collector_namespaces(
    configured_relays,
):
    (condition, *_) = configured_relays["otel-col-cluster"]["processors"][
        "filter/platform"
    ]["metric_conditions"]
    pattern = json.loads(condition.rsplit(", ", 1)[1].removesuffix(")"))
    for namespace in (
        "kube-system",
        "cert-manager",
        RELEASE_NAMESPACE,
        "loco-obs",
        "loco-obs-node",
    ):
        assert re.search(pattern, namespace), namespace
        for excluded in (f"prefix-{namespace}", f"{namespace}-suffix", f"{namespace}x"):
            assert re.search(pattern, excluded) is None, excluded


def test_platform_pipelines_use_the_operators_endpoint_when_set():
    endpoint = "https://otel.example.com"
    configs = collector_relays(
        render_chart(
            "loco-worker",
            configured(exporter={"endpoint": endpoint, "headers": {"x-key": "abc"}}),
        )
    )
    for name, config in configs.items():
        for pipeline, settings in config["service"]["pipelines"].items():
            platform = pipeline.endswith("/platform") or pipeline == "metrics/capacity"
            want = "otlp_http/platform" if platform else "otlp_http"
            assert settings["exporters"] == [want], (name, pipeline)
    for name in ("otel-col-daemon-agent", "otel-col-cluster"):
        own = configs[name]["exporters"]["otlp_http/platform"]
        assert own["endpoint"] == endpoint
        assert own["headers"] == {"x-key": "abc"}
        assert "auth" not in own


def test_the_operators_endpoint_needs_platform_telemetry():
    values = configured(
        enabled=False, exporter={"endpoint": "https://otel.example.com"}
    )
    for config in collector_relays(render_chart("loco-worker", values)).values():
        assert set(config["exporters"]) == {"otlp_http"}


def platform_pipeline_config(cluster, name):
    """The cluster collector's name pipeline alone, fed over OTLP/HTTP and written to a file."""
    pipeline = cluster["service"]["pipelines"][name]
    return {
        "receivers": {
            "otlp/platform": {"protocols": {"http": {"endpoint": "0.0.0.0:4318"}}}
        },
        "processors": {
            name: cluster["processors"][name] for name in pipeline["processors"]
        },
        "exporters": {"file": {"path": "/data/output.json", "flush_interval": "100ms"}},
        "service": {
            "pipelines": {
                name.split("/")[0]: {
                    "receivers": ["otlp/platform"],
                    "processors": pipeline["processors"],
                    "exporters": ["file"],
                }
            }
        },
    }


def test_controller_runtime_metrics_pass_the_platform_metrics_pipeline(
    configured_relays, tmp_path
):
    cluster = configured_relays["otel-col-cluster"]
    config = platform_pipeline_config(cluster, "metrics/platform")
    point = {"timeUnixNano": "1", "asDouble": 1}
    controller = {"key": "controller", "value": {"stringValue": "application"}}

    def resource(service, namespace=None):
        attributes = [{"key": "service.name", "value": {"stringValue": service}}]
        if namespace:
            attributes.append(
                {"key": "k8s.namespace.name", "value": {"stringValue": namespace}}
            )
        return {"attributes": attributes}

    metrics = [
        {
            "name": "controller_runtime_reconcile_total",
            "sum": {
                "aggregationTemporality": 2,
                "isMonotonic": True,
                "dataPoints": [{**point, "attributes": [controller]}],
            },
        },
        {"name": "workqueue_depth", "gauge": {"dataPoints": [point]}},
    ]
    payload = {
        "resourceMetrics": [
            {
                "resource": resource(service),
                "scopeMetrics": [{"metrics": metrics}],
            }
            for service in ("loco-controller", "loco-build-controller")
        ]
        + [
            {
                "resource": resource("workload", "loco-ws-tenant"),
                "scopeMetrics": [{"metrics": metrics}],
            }
        ]
    }
    with run_collector(tmp_path, config, "otel-col-cluster") as collector:
        post_otlp(collector, "metrics", payload)
        received = read_output(
            collector,
            lambda document: [
                (
                    {
                        item["key"]: item["value"]["stringValue"]
                        for item in metrics["resource"]["attributes"]
                    },
                    metric["name"],
                )
                for metrics in document["resourceMetrics"]
                for scope in metrics["scopeMetrics"]
                for metric in scope["metrics"]
            ],
            4,
        )
    assert {(attrs["service.name"], name) for attrs, name in received} == {
        (service, name)
        for service in ("loco-controller", "loco-build-controller")
        for name in ("controller_runtime_reconcile_total", "workqueue_depth")
    }
    assert all(attrs["loco.telemetry.scope"] == "platform" for attrs, _ in received)


def test_controller_telemetry_enters_the_cluster_platform_pipelines(
    configured_relays, tmp_path
):
    cluster = configured_relays["otel-col-cluster"]
    assert (
        "otlp/platform"
        in cluster["service"]["pipelines"]["metrics/platform"]["receivers"]
    )
    config = platform_pipeline_config(cluster, "traces/platform")
    span = {
        "traceId": "0af7651916cd43dd8448eb211c80319c",
        "spanId": "b7ad6b7169203331",
        "name": "reconcile",
        "startTimeUnixNano": "1",
        "endTimeUnixNano": "2",
    }
    payload = {
        "resourceSpans": [
            {
                "resource": {
                    "attributes": [
                        {
                            "key": "service.name",
                            "value": {"stringValue": "loco-controller"},
                        }
                    ]
                },
                "scopeSpans": [{"spans": [span]}],
            }
        ]
    }
    with run_collector(tmp_path, config, "otel-col-cluster") as collector:
        post_otlp(collector, "traces", payload)
        (resource,) = read_output(
            collector,
            lambda document: [
                {
                    item["key"]: item["value"]["stringValue"]
                    for item in spans["resource"]["attributes"]
                }
                for spans in document["resourceSpans"]
            ],
            1,
        )
    assert resource["loco.telemetry.scope"] == "platform"
    assert resource["k8s.cluster.name"] == "test-cluster"


def test_capacity_collection_uses_the_selected_controller(configured_relays):
    cluster = configured_relays["otel-col-cluster"]
    pipeline = cluster["service"]["pipelines"]["metrics/capacity"]
    assert pipeline["receivers"] == ["prometheus/capacity"]
    assert pipeline["processors"] == ["memory_limiter", "resource/platform"]
    scrape = cluster["receivers"]["prometheus/capacity"]["config"]["scrape_configs"][0]
    assert scrape["metrics_path"] == "/capacity"
    assert scrape["honor_timestamps"] is True
    assert scrape["kubernetes_sd_configs"][0]["namespaces"]["names"] == [
        RELEASE_NAMESPACE
    ]
    assert scrape["relabel_configs"][0]["regex"] == "loco-controller"
    configs = collector_relays(
        render_chart("loco-worker", {**configured(), "cluster": {"role": "builds"}})
    )
    build_scrape = configs["otel-col-cluster"]["receivers"]["prometheus/capacity"][
        "config"
    ]["scrape_configs"][0]
    assert build_scrape["relabel_configs"][0]["regex"] == "loco-build-controller"


def test_capacity_scrape_preserves_membership_and_timestamps(
    configured_relays, tmp_path
):
    import http.server
    import threading
    import time

    captured = int(time.time() * 1000)
    source = "00000000-0000-0000-0000-000000000002"
    identity = f'source_id="{source}",node="node-a",pod_uid="pod-a",workspace_id="workspace-a",environment_id="environment-a",resource_id="resource-a"'
    text = (
        "# TYPE loco_pod_cpu_request_cores gauge\n"
        f"loco_pod_cpu_request_cores{{{identity}}} 0.9 {captured}\n"
        "# TYPE loco_pod_memory_request_bytes gauge\n"
        f"loco_pod_memory_request_bytes{{{identity}}} 335544320 {captured}\n"
        "# TYPE loco_capacity_sample_pods gauge\n"
        f'loco_capacity_sample_pods{{source_id="{source}"}} 1 {captured}\n'
    ).encode()

    class Snapshot(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.send_header("Content-Type", "text/plain; version=0.0.4")
            self.end_headers()
            self.wfile.write(text)

        def log_message(self, *_):
            pass

    server = http.server.ThreadingHTTPServer(("0.0.0.0", 0), Snapshot)
    thread = threading.Thread(target=server.serve_forever)
    thread.start()
    cluster = configured_relays["otel-col-cluster"]
    pipeline = cluster["service"]["pipelines"]["metrics/capacity"]
    config = {
        "receivers": {
            "prometheus/capacity": {
                "config": {
                    "scrape_configs": [
                        {
                            "job_name": "loco-capacity",
                            "scrape_interval": "100ms",
                            "scrape_timeout": "100ms",
                            "metrics_path": "/capacity",
                            "honor_timestamps": True,
                            "static_configs": [
                                {
                                    "targets": [
                                        f"host.docker.internal:{server.server_port}"
                                    ]
                                }
                            ],
                            "metric_relabel_configs": cluster["receivers"][
                                "prometheus/capacity"
                            ]["config"]["scrape_configs"][0]["metric_relabel_configs"],
                        }
                    ]
                }
            }
        },
        "processors": {
            name: cluster["processors"][name] for name in pipeline["processors"]
        },
        "exporters": {"file": {"path": "/data/output.json", "flush_interval": "100ms"}},
        "service": {"pipelines": {"metrics": {**pipeline, "exporters": ["file"]}}},
    }

    def points(document):
        return [
            (resource["resource"]["attributes"], metric["name"], point)
            for resource in document["resourceMetrics"]
            for scope in resource["scopeMetrics"]
            for metric in scope["metrics"]
            if metric["name"].startswith("loco_")
            for point in metric.get("gauge", {}).get("dataPoints", [])
        ]

    try:
        with run_collector(
            tmp_path,
            config,
            "otel-col-cluster",
            "--add-host",
            "host.docker.internal:host-gateway",
        ) as collector:
            received = read_output(collector, points, 3)
        names = {name for _, name, _ in received}
        assert names == {
            "loco_pod_cpu_request_cores",
            "loco_pod_memory_request_bytes",
            "loco_capacity_sample_pods",
        }
        for attributes, name, point in received:
            assert int(point["timeUnixNano"]) == captured * 1_000_000
            resource = {
                item["key"]: item["value"].get("stringValue") for item in attributes
            }
            assert resource["loco.telemetry.scope"] == "platform"
            assert "loco.workspace.id" not in resource
            labels = {
                item["key"]: item["value"].get("stringValue")
                for item in point["attributes"]
            }
            assert labels["source_id"] == source
            if name != "loco_capacity_sample_pods":
                assert labels["pod_uid"] == "pod-a"
                assert labels["workspace_id"] == "workspace-a"
    finally:
        server.shutdown()
        thread.join()
        server.server_close()
