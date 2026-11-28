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
from test_file_logs import file_log_pipeline, write_pod_log

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
    for name in ("node-collector-agent", "cluster-collector"):
        config = configured_relays[name]
        pipeline = config["service"]["pipelines"]["metrics/platform"]
        assert pipeline["exporters"] == ["otlp_http"]
        assert config["service"]["pipelines"]["metrics"]["exporters"] == ["otlp_http"]
        assert "filter/platform" in pipeline["processors"]
        assert "resource/platform" in pipeline["processors"]
    assert (
        "metrics/platform"
        not in configured_relays["workload-collector"]["service"]["pipelines"]
    )


def test_platform_only_keeps_only_platform_pipelines():
    values = configured(platformOnly=True)
    values.update({"workloadCollector": {"enabled": False}})
    configs = collector_relays(render_chart("loco-worker", values))
    for config in configs.values():
        assert set(config["service"]["pipelines"]) <= {
            "metrics/platform",
            "logs/platform",
            "traces/platform",
        }
        assert set(config["exporters"]) == {"otlp_http"}


def test_application_pipelines_keep_their_configuration(
    worker_relays, configured_relays
):
    for name, config in worker_relays.items():
        for pipeline, settings in config["service"]["pipelines"].items():
            expected = dict(settings)
            if name == "node-collector-agent" and pipeline == "logs":
                expected["processors"] = [
                    *settings["processors"],
                    "filter/workload_logs",
                ]
            assert configured_relays[name]["service"]["pipelines"][pipeline] == expected


def test_platform_log_opt_out_retains_workloads_metrics_events_and_traces():
    configs = collector_relays(
        render_chart("loco-worker", configured(logs={"enabled": False}))
    )
    node = configs["node-collector-agent"]["service"]["pipelines"]
    cluster = configs["cluster-collector"]["service"]["pipelines"]
    assert "logs/platform" not in node
    assert {"logs", "metrics", "metrics/platform"} <= node.keys()
    assert {"metrics/platform", "logs/platform", "traces/platform"} <= cluster.keys()
    assert {"logs", "metrics", "traces"} <= configs["workload-collector"]["service"][
        "pipelines"
    ].keys()


def test_platform_identity_is_exported(configured_relays):
    for name in ("node-collector-agent", "cluster-collector"):
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
    receiver = configured_relays["cluster-collector"]["receivers"]["k8s_cluster"]
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
            in ("node-collector-agent", "cluster-collector")
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
            ("DaemonSet", "node-collector-agent"),
            ("StatefulSet", "cluster-collector"),
        }
    assert all(checksums[0][name] != checksums[1][name] for name in checksums[0])


def test_disabling_an_individual_collector_still_renders():
    overrides = configured()
    overrides["nodeCollector"] = {"enabled": False}
    configs = collector_relays(render_chart("loco-worker", overrides))
    assert "node-collector-agent" not in configs
    assert "metrics/platform" in configs["cluster-collector"]["service"]["pipelines"]


def test_platform_allowlist_covers_the_release_and_collector_namespaces(
    configured_relays,
):
    (condition, *_) = configured_relays["cluster-collector"]["processors"][
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
            platform = pipeline.endswith("/platform")
            want = "otlp_http/platform" if platform else "otlp_http"
            assert settings["exporters"] == [want], (name, pipeline)
    for name in ("node-collector-agent", "cluster-collector"):
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


@pytest.mark.parametrize(
    "enabled,namespace,pipeline",
    [
        (False, "separate-control-plane", "logs"),
        (True, "separate-control-plane", "logs"),
        (True, RELEASE_NAMESPACE, "logs/platform"),
    ],
)
def test_worker_skips_only_logs_owned_by_the_control_plane(
    tmp_path, enabled, namespace, pipeline
):
    relays = collector_relays(render_chart("loco-worker", configured(enabled=enabled)))
    daemon = relays["node-collector-agent"]
    for route in daemon["service"]["pipelines"].values():
        if "file_log" in route["receivers"]:
            assert "filter/control_plane_logs" in route["processors"]
    config = platform_pipeline_config(daemon, pipeline)
    # Synthetic pod metadata replaces Kubernetes enrichment in this processor fixture.
    processors = config["service"]["pipelines"]["logs"]["processors"]
    processors[:] = [
        name
        for name in processors
        if not name.startswith(("memory_limiter", "k8s_attributes"))
    ]
    config["processors"] = {name: config["processors"][name] for name in processors}
    resources = []
    for owner in (None, "control-plane"):
        attributes = [
            {"key": "k8s.namespace.name", "value": {"stringValue": namespace}}
        ]
        if owner:
            attributes.append(
                {"key": "loco.platform.log.owner", "value": {"stringValue": owner}}
            )
        resources.append(
            {
                "resource": {"attributes": attributes},
                "scopeLogs": [
                    {
                        "logRecords": [
                            {"body": {"stringValue": "owned" if owner else "unowned"}}
                        ]
                    }
                ],
            }
        )
    with run_collector(tmp_path, config, "node-collector-agent") as collector:
        post_otlp(collector, "logs", {"resourceLogs": resources})
        records = read_output(
            collector,
            lambda document: [
                record
                for resource in document["resourceLogs"]
                for scope in resource["scopeLogs"]
                for record in scope["logRecords"]
            ],
            1,
        )
    assert [record["body"]["stringValue"] for record in records] == ["unowned"]


def test_controller_runtime_metrics_pass_the_platform_metrics_pipeline(
    configured_relays, tmp_path
):
    cluster = configured_relays["cluster-collector"]
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
    with run_collector(tmp_path, config, "cluster-collector") as collector:
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
    cluster = configured_relays["cluster-collector"]
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
    with run_collector(tmp_path, config, "cluster-collector") as collector:
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


def platform_file_logs(relays, directory):
    (directory / "checkpoints").mkdir(exist_ok=True)
    daemon = relays["node-collector-agent"]
    config = file_log_pipeline(relays)
    pipeline = daemon["service"]["pipelines"]["logs/platform"]
    processors = [
        name
        for name in pipeline["processors"]
        if not name.startswith(("memory_limiter", "k8s_attributes"))
    ]
    config["processors"].update(
        {name: daemon["processors"][name] for name in processors}
    )
    config["service"]["pipelines"]["logs/platform"] = {
        "receivers": ["file_log"],
        "processors": processors,
        "exporters": ["file"],
    }
    config["receivers"]["file_log"]["start_at"] = daemon["receivers"]["file_log"][
        "start_at"
    ]
    config["receivers"]["file_log"]["storage"] = "file_storage"
    config["extensions"] = {"file_storage": {"directory": "/data/checkpoints"}}
    config["service"]["extensions"] = ["file_storage"]
    return config


def test_node_logs_keep_correlation_types_and_separate_workloads(
    configured_relays, tmp_path
):
    config = platform_file_logs(configured_relays, tmp_path)
    records = {
        "loco-controller": {
            "msg": "application rollout changed",
            "level": "INFO",
            "time": "2026-10-19T12:00:00.123456789Z",
            "trace_id": "0af7651916cd43dd8448eb211c80319c",
            "span_id": "b7ad6b7169203331",
            "resource": "synthetic-unready-service",
            "detail": "readiness probe: connection refused",
        },
        "loco-api": {
            "msg": "request failed",
            "level": "ERROR",
            "request": {"requestId": "synthetic-request", "entity": None},
            "literal": '{"must":"stay a string"}',
        },
        "loco-obs-proxy": {"msg": "otlp listening", "level": "INFO", "http": ":4318"},
    }
    for service, record in records.items():
        write_pod_log(
            tmp_path,
            RELEASE_NAMESPACE,
            [json.dumps(record)],
            pod=service,
            container=service,
        )
    write_pod_log(
        tmp_path,
        RELEASE_NAMESPACE,
        [
            json.dumps(
                {
                    "msg": "OTLP export failed",
                    "level": "ERROR",
                    "loco.telemetry.internal": True,
                }
            )
        ],
        pod="loco-obs-proxy-feedback",
        container="obs-proxy-ingest",
    )
    workload = json.dumps(
        {"msg": "customer JSON must remain a body", "trace_id": "user-string"}
    )
    write_pod_log(tmp_path, "loco-ws-tenant", [workload])
    for collector, namespace in [
        ("node-collector-agent", "loco-obs-node"),
        ("cluster-collector", "loco-obs"),
        ("workload-collector", "loco-obs"),
    ]:
        write_pod_log(
            tmp_path, namespace, ["collector logs must not loop"], pod=collector + "-0"
        )
    with run_collector(tmp_path, config, "node-collector-agent") as collector:
        received = read_output(
            collector,
            lambda document: [
                (resource.get("resource", {}).get("attributes", []), record)
                for resource in document["resourceLogs"]
                for scope in resource["scopeLogs"]
                for record in scope["logRecords"]
            ],
            4,
        )
    assert len(received) == 4, received
    by_body = {
        record["body"]["stringValue"]: (attrs, record) for attrs, record in received
    }
    assert set(by_body) == {record["msg"] for record in records.values()} | {workload}
    for expected in records.values():
        attrs, actual = by_body[expected["msg"]]
        attrs = {item["key"]: item["value"] for item in attrs}
        assert attrs["loco.telemetry.scope"] == {"stringValue": "platform"}
        assert attrs["k8s.cluster.name"] == {"stringValue": "test-cluster"}
        assert actual["severityNumber"] == (17 if expected["level"] == "ERROR" else 9)
    _, controller = by_body[records["loco-controller"]["msg"]]
    assert controller["traceId"] == records["loco-controller"]["trace_id"]
    assert controller["spanId"] == records["loco-controller"]["span_id"]
    assert controller["timeUnixNano"] == "1792411200123456789"
    _, api = by_body[records["loco-api"]["msg"]]
    attributes = {item["key"]: item["value"] for item in api["attributes"]}
    assert "kvlistValue" in attributes["request"]
    assert attributes["literal"] == {"stringValue": records["loco-api"]["literal"]}
    attrs, actual = by_body[workload]
    assert not any(item["key"] == "loco.telemetry.scope" for item in attrs)
    assert "traceId" not in actual
    print(
        "synthetic CRI files: controller/API/proxy collected once as platform logs; workload kept separate; collector logs excluded"
    )
    print(json.dumps(controller, sort_keys=True))
    print(json.dumps(api, sort_keys=True))


def test_platform_log_backlog_resumes_without_replay(configured_relays, tmp_path):
    config = platform_file_logs(configured_relays, tmp_path)
    write_pod_log(
        tmp_path,
        RELEASE_NAMESPACE,
        ['{"msg":"before collector startup","level":"INFO"}'],
    )
    with run_collector(tmp_path, config, "node-collector-agent") as collector:
        first = read_output(
            collector,
            lambda document: [
                record["body"]["stringValue"]
                for resource in document["resourceLogs"]
                for scope in resource["scopeLogs"]
                for record in scope["logRecords"]
            ],
            1,
        )
    assert first == ["before collector startup"]
    (tmp_path / "output.json").rename(tmp_path / "before-restart.json")
    path = next((tmp_path / "pods").glob("*/*/0.log"))
    with path.open("a") as file:
        file.write(
            '2026-10-19T12:00:01.000000000Z stdout F {"msg":"during collector restart","level":"INFO"}\n'
        )
    with run_collector(tmp_path, config, "node-collector-agent") as collector:
        resumed = read_output(
            collector,
            lambda document: [
                record["body"]["stringValue"]
                for resource in document["resourceLogs"]
                for scope in resource["scopeLogs"]
                for record in scope["logRecords"]
            ],
            1,
        )
    assert resumed == ["during collector restart"], resumed
    print(
        json.dumps(
            {"before_collector": first, "after_restart": resumed}, sort_keys=True
        )
    )
