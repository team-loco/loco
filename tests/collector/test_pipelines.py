import json
import os
import subprocess
from pathlib import Path

import pytest
from helpers import (
    CI_VALUES,
    COLLECTOR_CONFIGMAPS,
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
CLIENT_POD_IDENTITY = (
    "k8s.pod.ip",
    "k8s.pod.uid",
    "k8s.pod.name",
    "k8s.namespace.name",
)
ROUTE_TENANCY = ("loco.workspace.id", "loco.resource.id")
EXPORTER_HEADERS = {"X-Loco-Cluster": "cluster-a"}
PLATFORM_TELEMETRY = {
    "enabled": True,
    "installation": "test",
    "cluster": "test",
    "environment": "staging",
}


def exporter_settings():
    return chart_values("loco-worker")["global"]["exporter"]


@pytest.fixture(
    scope="module",
    params=[None, False, True],
    ids=["default", "platform-telemetry", "platform-only"],
)
def variant_relays(request):
    overrides = {}
    if request.param is not None:
        overrides = {
            "global": {
                "platformTelemetry": {
                    **PLATFORM_TELEMETRY,
                    "platformOnly": request.param,
                }
            },
            "otel-col-deploy": {"enabled": not request.param},
        }
    return collector_relays(render_chart("loco-worker", overrides))


def test_collectors_export_only_otlp_http(worker_relays):
    for collector, config in worker_relays.items():
        assert list(config["exporters"]) == ["otlp_http"], collector
        for name, pipeline in config["service"]["pipelines"].items():
            assert pipeline["exporters"] == ["otlp_http"], (collector, name)


def test_collectors_export_to_the_configured_endpoint(worker_relays):
    for collector, config in worker_relays.items():
        (exporter,) = config["exporters"].values()
        assert (
            exporter["endpoint"]
            == chart_values("loco-worker", CI_VALUES)["global"]["exporter"]["endpoint"]
        ), collector


def test_exporters_queue_on_file_storage_with_the_configured_settings(variant_relays):
    settings = exporter_settings()
    queue = {
        key: value for key, value in settings["queue"].items() if key != "directory"
    }
    for collector, config in variant_relays.items():
        (exporter,) = config["exporters"].values()
        assert exporter["sending_queue"] == {
            "enabled": True,
            "storage": "file_storage/exporter",
            **queue,
        }, collector
        assert config["extensions"]["file_storage/exporter"] == {
            "directory": settings["queue"]["directory"]
        }, collector
        assert "file_storage/exporter" in config["service"]["extensions"], collector


def test_exporters_retry_with_the_configured_settings(worker_relays):
    settings = exporter_settings()
    for collector, config in worker_relays.items():
        (exporter,) = config["exporters"].values()
        assert exporter["retry_on_failure"] == {"enabled": True, **settings["retry"]}, (
            collector
        )


def test_exporters_send_no_auth_by_default(worker_relays):
    for collector, config in worker_relays.items():
        (exporter,) = config["exporters"].values()
        assert "auth" not in exporter, collector


@pytest.fixture(scope="module")
def authenticated_exporter():
    return render_chart(
        "loco-worker",
        {
            "global": {
                "exporter": {
                    "headers": EXPORTER_HEADERS,
                    "ingestToken": {"enabled": True, "secretName": "cluster-token"},
                }
            }
        },
    )


def test_exporter_headers_come_from_values(authenticated_exporter):
    for collector, config in collector_relays(authenticated_exporter).items():
        assert config["exporters"]["otlp_http"]["headers"] == EXPORTER_HEADERS, (
            collector
        )


def test_exporter_authenticates_with_the_mounted_bearer_token(authenticated_exporter):
    token = exporter_settings()["ingestToken"]
    filename = f"{token['directory']}/{token['secretKey']}"
    for collector, config in collector_relays(authenticated_exporter).items():
        exporter = config["exporters"]["otlp_http"]
        assert exporter["auth"] == {"authenticator": "bearertokenauth/exporter"}, (
            collector
        )
        assert config["extensions"]["bearertokenauth/exporter"] == {
            "filename": filename
        }, collector
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


def test_file_log_receiver_checkpoints_to_file_storage(worker_relays):
    config = worker_relays["otel-col-daemon-agent"]
    assert config["receivers"]["file_log"]["storage"] == "file_storage"
    assert "file_storage" in config["service"]["extensions"]


def test_log_checkpoints_live_in_the_host_path_the_daemonset_mounts(
    worker_documents, worker_relays
):
    directory = worker_relays["otel-col-daemon-agent"]["extensions"]["file_storage"][
        "directory"
    ]
    daemonset = next(
        document
        for document in worker_documents
        if document["kind"] == "DaemonSet"
        and document["metadata"]["name"] == "otel-col-daemon-agent"
    )
    host_paths = [
        volume["hostPath"]["path"]
        for volume in daemonset["spec"]["template"]["spec"]["volumes"]
        if "hostPath" in volume
    ]
    assert directory in host_paths, host_paths


def test_disabling_log_checkpoints_drops_the_file_log_storage():
    documents = render_chart(
        "loco-worker",
        {
            "otel-col-daemon": {
                "presets": {"logsCollection": {"storeCheckpoints": False}}
            }
        },
    )
    config = collector_relays(documents)["otel-col-daemon-agent"]
    assert "storage" not in config["receivers"]["file_log"]


def test_all_collectors_render(worker_relays):
    assert set(worker_relays) == set(COLLECTOR_CONFIGMAPS)


def test_every_pipeline_stamps_the_region_last(worker_relays, subtests):
    for collector, _config, name, pipeline in _pipelines(worker_relays):
        with subtests.test(collector=collector, pipeline=name):
            assert pipeline["processors"][-1] == "resource/region"
    for collector, config in worker_relays.items():
        assert config["processors"]["resource/region"] == {
            "attributes": [
                {
                    "key": "loco.region",
                    "value": chart_values("loco-worker", CI_VALUES)["global"]["region"],
                    "action": "upsert",
                }
            ]
        }, collector


def test_memory_limiter_runs_first(worker_relays):
    for collector, _, name, pipeline in _pipelines(worker_relays):
        processors = pipeline.get("processors", [])
        assert len(processors) >= 2, f"{collector} {name}"
        assert processors[0] == "memory_limiter", f"{collector} {name}"


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
        assert enrichers[0] == "k8s_attributes/connection", f"{collector} {name}"
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
            if enricher == "k8s_attributes/route_namespace":
                continue
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
        pipeline
        for pipeline in config["service"]["pipelines"].values()
        if "file_log" in pipeline["receivers"]
    )
    extract = config["processors"][_k8s_attributes(pipeline)[0]]["extract"]
    assert {
        "from": "pod",
        "key": "loco.io/build-id",
        "tag_name": "loco.io/build-id",
    } in extract["labels"]


def _tenancy_extracts(worker_relays):
    for collector, config, _name, pipeline in _pipelines(worker_relays):
        for enricher in _k8s_attributes(pipeline):
            if enricher != "k8s_attributes/route_namespace":
                yield (
                    f"{collector} {enricher}",
                    config["processors"][enricher]["extract"],
                )


def test_tenancy_comes_from_pod_resource_labels(worker_relays):
    for label, extract in _tenancy_extracts(worker_relays):
        labels = {
            rule.get("key"): rule.get("tag_name")
            for rule in extract["labels"]
            if rule["from"] == "pod"
        }
        expected = TENANT_LABELS | BUILD_LABELS | GATEWAY_LABELS
        if label == "otel-col-daemon-agent k8s_attributes/pod_uid":
            expected |= {"loco.io/platform-log-owner": "loco.platform.log.owner"}
        assert labels == expected, label


def test_tenancy_comes_from_namespace_ownership_labels(worker_relays):
    for label, extract in _tenancy_extracts(worker_relays):
        labels = {
            rule["key"]: rule["tag_name"]
            for rule in extract["labels"]
            if rule["from"] == "namespace"
        }
        assert labels == {
            key: value
            for key, value in TENANT_LABELS.items()
            if key != "loco.io/resource-id"
        }, label


def test_tenancy_labels_match_exact_keys(worker_relays):
    for label, extract in _tenancy_extracts(worker_relays):
        assert all("key_regex" not in rule for rule in extract["labels"]), label


def test_tenancy_ignores_pod_annotations(worker_relays):
    for label, extract in _tenancy_extracts(worker_relays):
        assert "annotations" not in extract, label
        assert not extract.get("otel_annotations", False), label


def test_enrichment_identifies_the_workload(worker_relays):
    for label, extract in _tenancy_extracts(worker_relays):
        for key in (
            "k8s.namespace.name",
            "k8s.pod.name",
            "k8s.pod.uid",
            "k8s.deployment.name",
        ):
            assert key in extract["metadata"], (label, key)


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
        document
        for document in worker_documents
        if document["kind"] == "StatefulSet"
        and document["metadata"]["name"] == "otel-col-deploy"
    )


def test_ingest_requests_resources_within_its_limits(worker_documents):
    resources = _ingest_collector(worker_documents)["spec"]["template"]["spec"][
        "containers"
    ][0]["resources"]
    for resource in ("cpu", "memory"):
        assert quantity(resources["requests"][resource]) > 0
        assert quantity(resources["requests"][resource]) <= quantity(
            resources["limits"][resource]
        )


def test_route_tenancy_runs_after_kubernetes_attributes(worker_relays):
    collector = worker_relays["otel-col-deploy"]
    for name, pipeline in collector["service"]["pipelines"].items():
        if "otlp" not in pipeline["receivers"]:
            continue
        processors = pipeline["processors"]
        order = [
            processors.index(step)
            for step in (
                "k8s_attributes/connection",
                "transform/envoy_tenancy",
                "groupbyattrs/tenancy",
                "resource/region",
            )
        ]
        assert order == sorted(order), name
    keys = collector["processors"]["groupbyattrs/tenancy"]["keys"]
    assert {*ROUTE_TENANCY, "k8s.namespace.name"} <= set(keys), keys


def test_collector_service_ports_reach_the_receivers():
    deploy = chart_values("loco-worker")["otel-col-deploy"]
    protocols = deploy["config"]["receivers"]["otlp"]["protocols"]
    assert protocols["grpc"]["endpoint"].rsplit(":", 1)[1] == str(
        deploy["ports"]["otlp"]["servicePort"]
    )
    assert protocols["http"]["endpoint"].rsplit(":", 1)[1] == str(
        deploy["ports"]["otlp-http"]["servicePort"]
    )


def test_file_logs_extract_application_severity(worker_relays):
    for _, config, _, pipeline in _pipelines(worker_relays):
        if "file_log" not in pipeline["receivers"]:
            continue
        processors = pipeline["processors"]
        assert "transform/log_severity" in processors
        assert processors.index("transform/log_severity") > processors.index(
            "memory_limiter"
        )
        assert processors.index("transform/log_severity") < processors.index(
            "resource/region"
        )
        assert "log_statements" in config["processors"]["transform/log_severity"]


def test_the_collector_parses_routes_in_the_controllers_environment_namespaces():
    prefix = "tenant-"
    documents = render_chart(
        "loco-worker", {"global": {"workspaceNamespacePrefix": prefix}}
    )
    groups = collector_relays(documents)["otel-col-deploy"]["processors"][
        "transform/envoy_tenancy"
    ]["trace_statements"]
    (parse,) = [
        statement
        for group in groups
        for statement in group["statements"]
        if "ExtractPatterns" in statement
    ]
    assert f'"^httproute/(?P<namespace>{prefix}' in parse


def test_exporter_queue_blocks_on_overflow():
    assert exporter_settings()["queue"]["block_on_overflow"] is True


def test_exporter_batches_count_items():
    assert exporter_settings()["queue"]["batch"]["sizer"] == "items"


def test_exporter_batches_wait_for_a_minimum_size():
    assert exporter_settings()["queue"]["batch"]["min_size"] > 0


def test_pipelines_batch_only_after_persistent_enqueue(variant_relays):
    for collector, config in variant_relays.items():
        assert "batch" not in config["processors"], collector
        for name, pipeline in config["service"]["pipelines"].items():
            assert all(
                step.split("/")[0] not in {"batch", "queuebatch"}
                for step in pipeline["processors"]
            ), (collector, name)


def test_the_collector_accepts_the_rendered_config(variant_relays, tmp_path):
    image = chart_values("loco-worker")["otel-col-daemon"]["image"]
    service_account = tmp_path / "serviceaccount"
    service_account.mkdir()
    (service_account / "token").write_text("test-token")
    (service_account / "namespace").write_text("default")
    ca = Path(__file__).parent / "fixtures/ca.crt"
    (service_account / "ca.crt").write_bytes(ca.read_bytes())
    for collector, config in variant_relays.items():
        config_path = tmp_path / f"{collector}.json"
        config_path.write_text(json.dumps(config))
        storage_mounts = [
            argument
            for name, extension in config["extensions"].items()
            if name.split("/")[0] == "file_storage"
            for argument in ("--tmpfs", extension["directory"])
        ]
        result = subprocess.run(
            [
                "docker",
                "run",
                "--rm",
                "--user",
                str(os.getuid()),
                *storage_mounts,
                "--env",
                "MY_POD_IP=127.0.0.1",
                "--env",
                "OTEL_K8S_NODE_NAME=test-node",
                "--env",
                "OTEL_K8S_NODE_IP=127.0.0.1",
                "--env",
                "KUBERNETES_SERVICE_HOST=127.0.0.1",
                "--env",
                "KUBERNETES_SERVICE_PORT=443",
                "--volume",
                f"{service_account}:/var/run/secrets/kubernetes.io/serviceaccount:ro",
                "--volume",
                f"{tmp_path}:/conf:ro",
                f"{image['repository']}:{image['tag']}",
                "validate",
                f"--config=/conf/{config_path.name}",
            ],
            capture_output=True,
            text=True,
        )
        assert result.returncode == 0, f"{collector}: {result.stdout}\n{result.stderr}"


def test_gateway_tenancy_comes_from_the_route_namespace(worker_relays):
    config = worker_relays["otel-col-deploy"]
    processor = config["processors"]["k8s_attributes/route_namespace"]
    assert {
        rule["key"]: rule["tag_name"]
        for rule in processor["extract"]["labels"]
        if rule["from"] == "namespace"
    } == {
        key: value
        for key, value in TENANT_LABELS.items()
        if key != "loco.io/resource-id"
    }
    for pipeline in config["service"]["pipelines"].values():
        order = pipeline["processors"]
        assert (
            order.index("groupbyattrs/tenancy")
            < order.index("k8s_attributes/route_namespace")
            < order.index("resource/region")
        )
