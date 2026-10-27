import re
from types import SimpleNamespace

import pytest

from helpers import (
    COLLECTOR_CONFIGMAPS,
    TEST_EXPORTER_ENDPOINT,
    TEST_REGION,
    chart_values,
    collector_relays,
    quantity,
    render_chart,
)

PUSH_RECEIVERS = {"otlp", "jaeger", "zipkin"}
TENANT_LABELS = {
    "loco.io/organization-id": "loco.organization.id",
    "loco.io/workspace-id": "loco.workspace.id",
    "loco.io/environment-id": "loco.environment.id",
    "loco.io/resource-id": "loco.resource.id",
}
BUILD_LABELS = {"loco.io/build-id": "loco.io/build-id"}
GATEWAY_LABELS = {
    "gateway.envoyproxy.io/owning-gateway-name": "gateway.envoyproxy.io/owning-gateway-name",
    "gateway.envoyproxy.io/owning-gateway-namespace": "gateway.envoyproxy.io/owning-gateway-namespace",
}
CLIENT_POD_IDENTITY = ("k8s.pod.ip", "k8s.pod.uid")
ENVOY_CLUSTER_ATTRIBUTES = {
    "trace_statements": "upstream_cluster",
    "log_statements": "upstream_cluster",
    "metric_statements": "envoy.cluster_name",
}
ROUTE_TENANCY = ("loco.workspace.id", "loco.resource.id")
EXPORTER_HEADERS = {"X-Loco-Cluster": "cluster-a"}


def exporter_settings():
    return chart_values("loco-worker")["global"]["exporter"]


def test_collectors_export_only_otlp_http(worker_relays):
    for collector, config in worker_relays.items():
        assert list(config["exporters"]) == ["otlp_http"], collector
        for name, pipeline in config["service"]["pipelines"].items():
            assert pipeline["exporters"] == ["otlp_http"], (collector, name)


def test_collectors_export_to_the_configured_endpoint(worker_relays):
    for collector, config in worker_relays.items():
        (exporter,) = config["exporters"].values()
        assert exporter["endpoint"] == TEST_EXPORTER_ENDPOINT, collector


def test_exporters_queue_on_file_storage_with_the_configured_settings(worker_relays):
    settings = exporter_settings()
    queue = {key: value for key, value in settings["queue"].items() if key != "directory"}
    for collector, config in worker_relays.items():
        (exporter,) = config["exporters"].values()
        assert exporter["sending_queue"] == {
            "enabled": True, "storage": "file_storage/exporter", **queue
        }, collector
        assert config["extensions"]["file_storage/exporter"] == {
            "directory": settings["queue"]["directory"]
        }, collector
        assert "file_storage/exporter" in config["service"]["extensions"], collector


def test_exporters_retry_with_the_configured_settings(worker_relays):
    settings = exporter_settings()
    for collector, config in worker_relays.items():
        (exporter,) = config["exporters"].values()
        assert exporter["retry_on_failure"] == {"enabled": True, **settings["retry"]}, collector


def test_exporters_send_no_auth_by_default(worker_relays):
    for collector, config in worker_relays.items():
        (exporter,) = config["exporters"].values()
        assert "auth" not in exporter, collector


@pytest.fixture(scope="module")
def authenticated_exporter():
    return render_chart(
        "loco-worker",
        {"global": {"exporter": {"headers": EXPORTER_HEADERS, "ingestToken": {"enabled": True, "secretName": "cluster-token"}}}},
    )


def test_exporter_headers_come_from_values(authenticated_exporter):
    for collector, config in collector_relays(authenticated_exporter).items():
        assert config["exporters"]["otlp_http"]["headers"] == EXPORTER_HEADERS, collector


def test_exporter_authenticates_with_the_mounted_bearer_token(authenticated_exporter):
    token = exporter_settings()["ingestToken"]
    filename = f"{token['directory']}/{token['secretKey']}"
    for collector, config in collector_relays(authenticated_exporter).items():
        exporter = config["exporters"]["otlp_http"]
        assert exporter["auth"] == {"authenticator": "bearertokenauth/exporter"}, collector
        assert config["extensions"]["bearertokenauth/exporter"] == {"filename": filename}, collector
        assert "bearertokenauth/exporter" in config["service"]["extensions"], collector


def _pipelines(worker_relays):
    for collector, config in worker_relays.items():
        for name, pipeline in config["service"]["pipelines"].items():
            yield (collector, config, name, pipeline)


def _push_pipelines(worker_relays):
    for collector, config, name, pipeline in _pipelines(worker_relays):
        if PUSH_RECEIVERS & set(pipeline["receivers"]):
            yield (collector, config, name, pipeline)


def _k8s_attributes(pipeline):
    return [
        processor
        for processor in pipeline.get("processors", [])
        if processor.split("/")[0] == "k8s_attributes"
    ]


def _checkpoint_directory(worker_relays):
    return worker_relays["otel-col-daemon-agent"]["extensions"]["file_storage"]["directory"]


def test_file_log_receiver_checkpoints_to_file_storage(worker_relays):
    config = worker_relays["otel-col-daemon-agent"]
    assert config["receivers"]["file_log"]["storage"] == "file_storage"
    assert "file_storage" in config["service"]["extensions"]


def test_log_checkpoints_live_in_the_host_path_the_daemonset_mounts(worker_relays):
    assert _checkpoint_directory(worker_relays) == "/var/lib/otelcol"


def test_disabling_log_checkpoints_drops_the_file_log_storage():
    documents = render_chart(
        "loco-worker",
        {"otel-col-daemon": {"presets": {"logsCollection": {"storeCheckpoints": False}}}},
    )
    config = collector_relays(documents)["otel-col-daemon-agent"]
    assert "storage" not in config["receivers"]["file_log"]


def _application_scrape(worker_relays):
    config = worker_relays["otel-col-daemon-agent"]
    (scrape,) = config["receivers"]["prometheus/applications"]["config"][
        "scrape_configs"
    ]
    return scrape


def test_application_scrapes_discover_only_pods_on_their_node(worker_relays):
    (discovery,) = _application_scrape(worker_relays)["kubernetes_sd_configs"]
    assert discovery["selectors"] == [
        {
            "role": "pod",
            "field": "spec.nodeName=${env:OTEL_K8S_NODE_NAME}",
            "label": "loco.io/metrics-port",
        }
    ]


def test_application_scrapes_use_the_configured_limits(worker_relays):
    scrape = _application_scrape(worker_relays)
    limits = chart_values("loco-worker")["otel-col-daemon"]["config"]["receivers"]["prometheus/applications"]["config"]["scrape_configs"][0]
    assert scrape["sample_limit"] == limits["sample_limit"]
    assert scrape["body_size_limit"] == limits["body_size_limit"]


def test_application_scrapes_ignore_target_labels_and_redirects(worker_relays):
    scrape = _application_scrape(worker_relays)
    assert not scrape["honor_labels"]
    assert not scrape["follow_redirects"]


def test_application_metrics_pipeline_reads_only_the_application_scrape(worker_relays):
    pipeline = worker_relays["otel-col-daemon-agent"]["service"]["pipelines"]["metrics/applications"]
    assert pipeline["receivers"] == ["prometheus/applications"]


def test_application_metrics_drop_scraped_tenancy_before_enrichment(worker_relays):
    processors = worker_relays["otel-col-daemon-agent"]["service"]["pipelines"]["metrics/applications"]["processors"]
    enrichment = processors.index("k8s_attributes/pod_uid")
    assert processors.index("resource/strip_tenancy") < enrichment
    assert processors.index("transform/scrape_identity") < enrichment


def test_application_scrape_addresses_support_ipv4_and_ipv6(
    worker_relays, subtests
):
    scrape = _application_scrape(worker_relays)
    rules = [
        rule
        for rule in scrape["relabel_configs"]
        if rule.get("target_label") == "__address__"
    ]
    for pod_ip, expected in [
        ("10.244.0.5", "10.244.0.5:9090"),
        ("fd00::5", "[fd00::5]:9090"),
    ]:
        with subtests.test(pod_ip=pod_ip):
            address = None
            for rule in rules:
                match = re.fullmatch(rule["regex"], f"{pod_ip};9090")
                if match:
                    replacement = rule["replacement"].replace("$$", "$")
                    address = re.sub(
                        "\\$\\{(\\d+)\\}",
                        lambda capture: match.group(int(capture[1])),
                        replacement,
                    )
            assert address == expected


def test_all_collectors_render(worker_relays):
    assert set(worker_relays) == set(COLLECTOR_CONFIGMAPS)


def test_every_pipeline_stamps_the_region_last(worker_relays, subtests):
    for collector, config, name, pipeline in _pipelines(worker_relays):
        with subtests.test(collector=collector, pipeline=name):
            assert pipeline["processors"][-2:] == ["resource/region", "batch"]
            assert config["processors"]["resource/region"] == {
                "attributes": [
                    {"key": "loco.region", "value": TEST_REGION, "action": "upsert"}
                ]
            }


def test_memory_limiter_runs_first_and_batch_last(worker_relays):
    for collector, _, name, pipeline in _pipelines(worker_relays):
        processors = pipeline.get("processors", [])
        assert len(processors) >= 2, f"{collector} {name}"
        assert processors[0] == "memory_limiter", f"{collector} {name}"
        assert processors[-1] == "batch", f"{collector} {name}"


def test_push_receivers_have_their_own_pipelines(worker_relays):
    for collector, _, name, pipeline in _push_pipelines(worker_relays):
        assert set(pipeline["receivers"]) <= PUSH_RECEIVERS, f"{collector} {name}"


def test_client_tenancy_is_removed_before_kubernetes_attributes(worker_relays):
    stripped = (
        set(TENANT_LABELS.values())
        | set(BUILD_LABELS.values())
        | set(GATEWAY_LABELS.values())
        | set(CLIENT_POD_IDENTITY)
    )
    for collector, config, name, pipeline in _push_pipelines(worker_relays):
        processors = pipeline["processors"]
        enrichers = _k8s_attributes(pipeline)
        assert len(enrichers) == 1, f"{collector} {name}"
        strips = [
            index
            for index, processor in enumerate(processors)
            if processor.split("/")[0] == "resource"
            and stripped
            <= {
                action["key"]
                for action in config["processors"][processor]["attributes"]
                if action["action"] == "delete"
            }
        ]
        assert strips, f"{collector} {name} does not delete client tenancy attributes"
        assert strips[0] < processors.index(enrichers[0]), f"{collector} {name}"


def test_push_pipelines_associate_pods_by_connection_only(worker_relays):
    for collector, config, name, pipeline in _push_pipelines(worker_relays):
        for enricher in _k8s_attributes(pipeline):
            assert config["processors"][enricher]["pod_association"] == [
                {"sources": [{"from": "connection"}]}
            ], f"{collector} {name}"


def test_file_logs_associate_pods_by_uid(worker_relays):
    config = worker_relays["otel-col-daemon-agent"]
    pipelines = [
        pipeline
        for pipeline in config["service"]["pipelines"].values()
        if "file_log" in pipeline["receivers"]
    ]
    assert len(pipelines) == 1
    enrichers = _k8s_attributes(pipelines[0])
    assert len(enrichers) == 1
    assert config["processors"][enrichers[0]]["pod_association"] == [
        {"sources": [{"from": "resource_attribute", "name": "k8s.pod.uid"}]}
    ]


def test_build_logs_keep_their_build_id(worker_relays):
    config = worker_relays["otel-col-daemon-agent"]
    pipeline = next(
        (
            pipeline
            for pipeline in config["service"]["pipelines"].values()
            if "file_log" in pipeline["receivers"]
        )
    )
    extract = config["processors"][_k8s_attributes(pipeline)[0]][
        "extract"
    ]
    assert {
        "from": "pod",
        "key": "loco.io/build-id",
        "tag_name": "loco.io/build-id",
    } in extract["labels"]


def test_tenancy_comes_from_explicit_pod_labels(worker_relays):
    for collector, config, name, pipeline in _pipelines(worker_relays):
        for enricher in _k8s_attributes(pipeline):
            extract = config["processors"][enricher]["extract"]
            labels = {
                rule.get("key"): rule.get("tag_name")
                for rule in extract["labels"]
                if rule["from"] == "pod"
            }
            assert labels == TENANT_LABELS | BUILD_LABELS | GATEWAY_LABELS, (
                f"{collector} {enricher}"
            )
            assert all(("key_regex" not in rule for rule in extract["labels"])), (
                f"{collector} {enricher}"
            )
            assert "annotations" not in extract, f"{collector} {enricher}"
            assert not extract.get("otel_annotations", False), f"{collector} {enricher}"
            for key in (
                "k8s.namespace.name",
                "k8s.pod.name",
                "k8s.pod.uid",
                "k8s.deployment.name",
                "service.name",
            ):
                assert key in extract["metadata"], f"{collector} {enricher}"


def test_only_the_singleton_collects_cluster_metrics_and_events(worker_relays):
    for receiver in ("k8s_cluster", "k8sobjects"):
        collectors = {
            name
            for name, config in worker_relays.items()
            if receiver in config["receivers"]
        }
        assert collectors == {"otel-col-cluster"}, receiver


def test_ingest_keeps_only_push_pipelines(worker_relays):
    config = worker_relays["otel-col-deploy"]
    for pipeline in config["service"]["pipelines"].values():
        assert set(pipeline["receivers"]) <= PUSH_RECEIVERS


def _ingest_collector(worker_documents):
    return next(
        (
            document
            for document in worker_documents
            if document["kind"] == "StatefulSet"
            and document["metadata"]["name"] == "otel-col-deploy"
        )
    )


def test_ingest_requests_resources_within_its_limits(worker_documents):
    resources = _ingest_collector(worker_documents)["spec"]["template"]["spec"]["containers"][0]["resources"]
    for resource in ("cpu", "memory"):
        assert quantity(resources["requests"][resource]) > 0
        assert quantity(resources["requests"][resource]) <= quantity(
            resources["limits"][resource]
        )


@pytest.fixture(scope="module")
def envoy_telemetry(worker_relays):
    envoy_telemetry = SimpleNamespace()
    envoy_telemetry.core = chart_values("loco-worker")
    envoy_telemetry.collector = worker_relays["otel-col-deploy"]
    return envoy_telemetry


def test_the_deployment_collector_receives_otlp_on_its_service_port(envoy_telemetry):
    port = envoy_telemetry.core["otel-col-deploy"]["ports"]["otlp"]["servicePort"]
    assert (
        envoy_telemetry.collector["receivers"]["otlp"]["protocols"]["grpc"]["endpoint"]
        == f"0.0.0.0:{port}"
    )


def test_route_tenancy_runs_after_kubernetes_attributes(envoy_telemetry):
    for name, pipeline in envoy_telemetry.collector["service"]["pipelines"].items():
        if "otlp" not in pipeline["receivers"]:
            continue
        processors = pipeline["processors"]
        order = [
            processors.index(step)
            for step in (
                "k8s_attributes/connection",
                "transform/envoy_tenancy",
                "groupbyattrs/tenancy",
                "batch",
            )
        ]
        assert order == sorted(order), name
    assert envoy_telemetry.collector["processors"]["groupbyattrs/tenancy"][
        "keys"
    ] == list(ROUTE_TENANCY)


def test_only_envoy_telemetry_gets_route_tenancy(envoy_telemetry):
    strip = {
        action["key"]
        for action in envoy_telemetry.collector["processors"]["resource/strip_tenancy"][
            "attributes"
        ]
        if action["action"] == "delete"
    }
    transform = envoy_telemetry.collector["processors"]["transform/envoy_tenancy"]
    for statements, attribute in ENVOY_CLUSTER_ATTRIBUTES.items():
        groups = transform[statements]
        unconditional = [group for group in groups if not group.get("conditions")]
        assert len(unconditional) == 1, statements
        for key in TENANT_LABELS.values():
            assert (
                f'delete_key(attributes, "{key}")' in unconditional[0]["statements"]
            ), statements
        gated = [group for group in groups if group.get("conditions")]
        assert len(gated) == 1, statements
        for condition in gated[0]["conditions"]:
            assert (
                'resource.attributes["gateway.envoyproxy.io/owning-gateway-name"] != nil'
                in condition
            ), statements
        assert "gateway.envoyproxy.io/owning-gateway-name" in strip
        assert (
            gated[0]["statements"][0]
            == f'set(cache["cluster"], attributes["{attribute}"])'
        ), statements
        for key in ROUTE_TENANCY:
            setters = [
                statement
                for statement in gated[0]["statements"]
                if statement.startswith(f'set(attributes["{key}"], cache[')
            ]
            assert len(setters) == 1, f"{statements} {key}"


def test_collector_service_ports_reach_the_receivers():
    deploy = chart_values("loco-worker")["otel-col-deploy"]
    protocols = deploy["config"]["receivers"]["otlp"]["protocols"]
    assert protocols["grpc"]["endpoint"].rsplit(":", 1)[1] == str(deploy["ports"]["otlp"]["servicePort"])
    assert protocols["http"]["endpoint"].rsplit(":", 1)[1] == str(deploy["ports"]["otlp-http"]["servicePort"])


def test_file_logs_extract_application_severity(worker_relays):
    for _, config, _, pipeline in _pipelines(worker_relays):
        if "file_log" not in pipeline["receivers"]:
            continue
        processors = pipeline["processors"]
        assert "transform/log_severity" in processors
        assert processors.index("transform/log_severity") > processors.index("memory_limiter")
        assert processors.index("transform/log_severity") < processors.index("batch")
        assert "log_statements" in config["processors"]["transform/log_severity"]


def test_the_collector_parses_routes_in_the_controllers_workspace_namespaces():
    prefix = "tenant-"
    documents = render_chart("loco-worker", {"global": {"workspaceNamespacePrefix": prefix}})
    statements = collector_relays(documents)["otel-col-deploy"]["processors"]["transform/envoy_tenancy"]["trace_statements"][1]["statements"]
    (parse,) = [statement for statement in statements if "ExtractPatterns" in statement]
    assert f'"^httproute/{prefix}(?P<workspace>' in parse
