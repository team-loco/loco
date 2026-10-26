import json
import re
import shutil
import subprocess
import tempfile
from pathlib import Path
from types import SimpleNamespace

import pytest

ROOT = Path(__file__).resolve().parents[2]
COLLECTOR_CONFIGMAPS = ("otel-col-daemon-agent", "otel-col-deploy", "otel-col-cluster")
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
TEST_PROXY = {
    "image": {"tag": "test"},
    "query": {
        "clickhouse": {
            "existingSecret": {"name": "proxy-reader", "key": "url"},
            "migratorExistingSecret": {"name": "proxy-migrator", "key": "url"},
        },
    },
}
TEST_REGION = "test-region-1"
TEST_EXPORTER_ENDPOINT = "https://ingest.test.local"
CONTROL_PLANE_BASE_VALUES = {"certManager": {"issuer": {"email": "ops@region.test"}}}
TEST_IMAGE = {"tag": "test"}
WORKER_BASE_VALUES = {
    "global": {"region": TEST_REGION, "exporter": {"endpoint": TEST_EXPORTER_ENDPOINT}},
    "certManager": {"issuer": {"email": "ops@worker.test"}},
    "agent": {"image": TEST_IMAGE},
    "controller": {"image": TEST_IMAGE},
    "builds": {"builderImage": TEST_IMAGE},
}


def render(chart, template, overrides):
    documents = render_documents(chart, template, overrides)
    return documents[0] if documents else None


def chart_values(chart):
    return json.loads(
        subprocess.check_output(
            ["yq", "-o=json", ".", str(ROOT / "charts" / chart / "values.yaml")],
            text=True,
        )
    )


def render_documents(chart, template, overrides, includes=()):
    source = ROOT / "charts" / chart
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        (root / "templates").mkdir()
        metadata = subprocess.check_output(
            ["yq", "-o=json", ".", str(source / "Chart.yaml")], text=True
        )
        chart_values = json.loads(metadata)
        chart_values.pop("dependencies", None)
        (root / "Chart.yaml").write_text(json.dumps(chart_values))
        shutil.copyfile(source / "values.yaml", root / "values.yaml")
        target = root / "templates" / template
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source / "templates" / template, target)
        for included in includes:
            shutil.copyfile(source / "templates" / included, root / "templates" / included)
        for helper in (source / "templates").glob("*.tpl"):
            shutil.copyfile(helper, root / "templates" / helper.name)
        (root / "overrides.json").write_text(json.dumps(overrides))
        rendered = subprocess.check_output(
            [
                "helm",
                "template",
                chart,
                str(root),
                "-n",
                "platform-test",
                "-f",
                str(root / "overrides.json"),
            ],
            text=True,
            stderr=subprocess.STDOUT,
        )
        documents = subprocess.check_output(
            ["yq", "-o=json", "-I=0", ".", "-"], input=rendered, text=True
        )
        return [
            json.loads(line)
            for line in documents.splitlines()
            if line.strip() not in ("", "null")
        ]


def render_chart(chart, overrides, check=True):
    with tempfile.TemporaryDirectory() as directory:
        base = Path(directory) / "base.json"
        base_values = {"loco-worker": WORKER_BASE_VALUES, "loco-control-plane": CONTROL_PLANE_BASE_VALUES}
        base.write_text(json.dumps(base_values.get(chart, {})))
        values = Path(directory) / "overrides.json"
        values.write_text(json.dumps(overrides))
        command = [
            "helm",
            "template",
            chart,
            str(ROOT / "charts" / chart),
            "-n",
            "platform-test",
            "-f",
            str(base),
            "-f",
            str(values),
        ]
        result = subprocess.run(command, capture_output=True, text=True)
        if not check:
            return result
        if result.returncode != 0:
            raise subprocess.CalledProcessError(
                result.returncode, command, result.stdout, result.stderr
            )
        rendered = result.stdout
    documents = subprocess.check_output(
        ["yq", "-o=json", "-I=0", ".", "-"], input=rendered, text=True
    )
    return [
        json.loads(line)
        for line in documents.splitlines()
        if line.strip() not in ("", "null")
    ]


QUANTITY_SUFFIXES = {"m": 0.001, "Mi": 2**20, "Gi": 2**30}


def quantity(value):
    for suffix, scale in QUANTITY_SUFFIXES.items():
        if value.endswith(suffix):
            return float(value.removesuffix(suffix)) * scale
    return float(value)


def collector_documents(documents):
    return [
        document
        for document in documents
        if document["metadata"].get("labels", {}).get("app.kubernetes.io/part-of") == "opentelemetry-collector"
    ]


def collector_relays(documents):
    configs = {}
    for document in documents:
        if (
            document["kind"] == "ConfigMap"
            and document["metadata"]["name"] in COLLECTOR_CONFIGMAPS
        ):
            relay = subprocess.check_output(
                ["yq", "-o=json", "-I=0", ".", "-"],
                input=document["data"]["relay"],
                text=True,
            )
            configs[document["metadata"]["name"]] = json.loads(relay)
    return configs


def test_regional_certificate_names():
    certificate = render(
        "loco-worker",
        "cm-cert.yaml",
        {
            "global": {
                "domain": {
                    "apps": ["apps.example.test"],
                    "platform": "platform.example.test",
                }
            },
            "certManager": {
                "additionalDNSNames": [
                    "*.staging-east.apps.example.test",
                    "*.staging-west.apps.example.test",
                ]
            },
        },
    )
    assert certificate["spec"]["dnsNames"] == [
        "*.apps.example.test",
        "*.platform.example.test",
        "*.staging-east.apps.example.test",
        "*.staging-west.apps.example.test",
    ]


def test_api_route_uses_control_plane_origin():
    route = render(
        "loco-control-plane",
        "api-route.yaml",
        {"controlPlaneHostname": "api.region.example.test"},
    )
    assert route["spec"]["hostnames"] == ["api.region.example.test"]
    (rule,) = route["spec"]["rules"]
    assert rule["matches"] == [{"path": {"type": "PathPrefix", "value": "/"}}]
    assert rule["backendRefs"][0]["name"] == "loco-control-plane-api"


def test_database_and_auth_secret_references():
    deployment = render(
        "loco-control-plane", "obs-proxy-query-deployment.yaml", {"obsProxy": TEST_PROXY}
    )
    env = {
        item["name"]: item
        for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"]
    }
    assert env["CLICKHOUSE_URL"]["valueFrom"]["secretKeyRef"] == {
        "name": "proxy-reader",
        "key": "url",
    }
    assert env["CLICKHOUSE_MIGRATOR_URL"]["valueFrom"]["secretKeyRef"] == {
        "name": "proxy-migrator",
        "key": "url",
    }


def test_missing_clickhouse_secrets_are_rejected(subtests):
    for secret in ("existingSecret", "migratorExistingSecret"):
        with subtests.test(secret=secret):
            clickhouse = {
                **TEST_PROXY["query"]["clickhouse"],
                secret: {"name": "", "key": "url"},
            }
            with pytest.raises(subprocess.CalledProcessError) as caught:
                render(
                    "loco-control-plane",
                    "obs-proxy-query-deployment.yaml",
                    {"obsProxy": {**TEST_PROXY, "query": {"clickhouse": clickhouse}}},
                )
            assert (
                f"obsProxy.query.clickhouse.{secret}.name is required" in caught.value.output
            )


def test_missing_database_secret_key_is_rejected():
    with pytest.raises(subprocess.CalledProcessError):
        render(
            "loco-control-plane",
            "obs-proxy-query-deployment.yaml",
            {
                "obsProxy": {
                    **TEST_PROXY,
                    "query": {
                        "clickhouse": {
                            **TEST_PROXY["query"]["clickhouse"],
                            "existingSecret": {"name": "db", "key": ""},
                        },
                    },
                }
            },
        )


def test_permission_endpoint_uses_the_private_api_service():
    deployment = render(
        "loco-control-plane", "obs-proxy-query-deployment.yaml", {"obsProxy": TEST_PROXY}
    )
    env = {
        item["name"]: item
        for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"]
    }
    assert (
        env["CONTROL_PLANE_URL"]["value"]
        == "http://loco-control-plane-api.platform-test.svc.cluster.local"
    )


def _proxy_env(overrides=None):
    values = {"obsProxy": {**TEST_PROXY, "query": dict(TEST_PROXY["query"])}}
    for key, value in (overrides or {}).items():
        values["obsProxy"]["query"][key] = value
    deployment = render("loco-control-plane", "obs-proxy-query-deployment.yaml", values)
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    return (
        container,
        [item["name"] for item in container["env"]],
        {item["name"]: item for item in container["env"]},
    )


def test_proxy_cors_origins_are_explicit():
    _, _, env = _proxy_env(
        {
            "cors": {
                "allowedOrigins": ["https://app.example.test", "http://localhost:5173"]
            }
        }
    )
    assert (
        env["CORS_ALLOWED_ORIGINS"]["value"]
        == "https://app.example.test,http://localhost:5173"
    )
    _, _, env = _proxy_env()
    assert env["CORS_ALLOWED_ORIGINS"]["value"] == ""


def test_proxy_reads_its_database_from_the_one_chart_value():
    database = chart_values("loco-control-plane")["clickhouse"]["clickhouse"]["database"]
    _, _, env = _proxy_env()
    assert env["CLICKHOUSE_DB"] == {"name": "CLICKHOUSE_DB", "value": database}


def test_proxy_readiness_checks_readyz():
    container, _, _ = _proxy_env()
    assert container["readinessProbe"]["httpGet"]["path"] == "/readyz"


def exporter_settings():
    return chart_values("loco-worker")["global"]["exporter"]


def test_collectors_export_only_otlp_to_the_configured_endpoint():
    documents = render_chart("loco-worker", {})
    configs = collector_relays(documents)
    assert set(configs) == set(COLLECTOR_CONFIGMAPS)
    settings = exporter_settings()
    queue = {key: value for key, value in settings["queue"].items() if key != "directory"}
    for collector, config in configs.items():
        (exporter,) = config["exporters"].values()
        assert list(config["exporters"]) == ["otlp_http"], collector
        assert exporter["endpoint"] == TEST_EXPORTER_ENDPOINT, collector
        assert exporter["sending_queue"] == {
            "enabled": True, "storage": "file_storage/exporter", **queue
        }, collector
        assert exporter["retry_on_failure"] == {"enabled": True, **settings["retry"]}, collector
        assert "auth" not in exporter, collector
        assert config["extensions"]["file_storage/exporter"] == {
            "directory": settings["queue"]["directory"]
        }, collector
        assert "file_storage/exporter" in config["service"]["extensions"], collector
        for name, pipeline in config["service"]["pipelines"].items():
            assert pipeline["exporters"] == ["otlp_http"], (collector, name)


def test_collectors_require_an_exporter_endpoint():
    result = render_chart("loco-worker", {"global": {"exporter": {"endpoint": ""}}}, check=False)
    assert result.returncode != 0
    assert "global.exporter.endpoint is required" in result.stderr


def test_exporter_headers_and_bearer_token_come_from_values():
    headers = {"X-Loco-Cluster": "cluster-a"}
    documents = render_chart(
        "loco-worker",
        {"global": {"exporter": {"headers": headers, "bearerToken": {"enabled": True, "secretName": "cluster-token"}}}},
    )
    token = exporter_settings()["bearerToken"]
    filename = f"{token['directory']}/{token['secretKey']}"
    for collector, config in collector_relays(documents).items():
        exporter = config["exporters"]["otlp_http"]
        assert exporter["headers"] == headers, collector
        assert exporter["auth"] == {"authenticator": "bearertokenauth/exporter"}, collector
        assert config["extensions"]["bearertokenauth/exporter"] == {"filename": filename}, collector
        assert "bearertokenauth/exporter" in config["service"]["extensions"], collector
    workloads = [document for document in documents if document["kind"] in ("DaemonSet", "StatefulSet")]
    assert len(workloads) == len(COLLECTOR_CONFIGMAPS)
    for workload in workloads:
        pod = workload["spec"]["template"]["spec"]
        volumes = {volume["name"]: volume for volume in pod["volumes"]}
        mounts = {mount["name"]: mount for mount in pod["containers"][0]["volumeMounts"]}
        assert volumes["exporter-token"]["secret"] == {"secretName": "cluster-token", "optional": True}
        assert mounts["exporter-token"]["mountPath"] == token["directory"]
        assert mounts["exporter-token"]["readOnly"]
        assert mounts["exporter-queue"]["mountPath"] == exporter_settings()["queue"]["directory"]


def test_exporter_queues_survive_collector_pod_replacement():
    documents = render_chart("loco-worker", {})
    directory = exporter_settings()["queue"]["directory"]
    workloads = {document["metadata"]["name"]: document for document in documents if document["kind"] in ("DaemonSet", "StatefulSet")}
    assert {name: workload["kind"] for name, workload in workloads.items()} == {
        "otel-col-daemon-agent": "DaemonSet",
        "otel-col-deploy": "StatefulSet",
        "otel-col-cluster": "StatefulSet",
    }
    daemon = workloads["otel-col-daemon-agent"]["spec"]["template"]["spec"]
    collector = daemon["containers"][0]
    queue = next(volume for volume in daemon["volumes"] if volume["name"] == "exporter-queue")
    assert queue["hostPath"]["path"].startswith(f"{directory}/")
    assert queue["hostPath"]["type"] == "DirectoryOrCreate"
    init = next(container for container in daemon["initContainers"] if container["name"] == "state-permissions")
    user = collector["securityContext"]["runAsUser"]
    group = collector["securityContext"]["runAsGroup"]
    assert init["command"][:3] == ["chown", "-R", f"{user}:{group}"]
    assert directory in init["command"][3:]
    assert {"name": "exporter-queue", "mountPath": directory} in init["volumeMounts"]
    for name in ("otel-col-deploy", "otel-col-cluster"):
        spec = workloads[name]["spec"]
        (claim,) = spec["volumeClaimTemplates"]
        assert claim["metadata"]["name"] == "exporter-queue", name
        assert claim["spec"]["accessModes"] == ["ReadWriteOnce"], name
        assert quantity(claim["spec"]["resources"]["requests"]["storage"]) > 0, name
        pod = spec["template"]["spec"]
        assert "exporter-queue" not in {volume["name"] for volume in pod["volumes"]}, name
        assert {"name": "exporter-queue", "mountPath": directory} in pod["containers"][0]["volumeMounts"], name
        assert pod["securityContext"]["fsGroup"] == user, name


@pytest.fixture(scope="module")
def collector_tenancy():
    collector_tenancy = SimpleNamespace()
    collector_tenancy.documents = render_chart("loco-worker", {})
    collector_tenancy.configs = collector_relays(collector_tenancy.documents)
    return collector_tenancy


def _pipelines(collector_tenancy):
    for collector, config in collector_tenancy.configs.items():
        for name, pipeline in config["service"]["pipelines"].items():
            yield (collector, config, name, pipeline)


def _push_pipelines(collector_tenancy):
    for collector, config, name, pipeline in _pipelines(collector_tenancy):
        if PUSH_RECEIVERS & set(pipeline["receivers"]):
            yield (collector, config, name, pipeline)


def _k8s_attributes(collector_tenancy, pipeline):
    return [
        processor
        for processor in pipeline.get("processors", [])
        if processor.split("/")[0] == "k8s_attributes"
    ]


def test_node_log_offsets_survive_collector_pod_replacement(collector_tenancy):
    config = collector_tenancy.configs["otel-col-daemon-agent"]
    assert config["receivers"]["file_log"]["storage"] == "file_storage"
    assert "file_storage" in config["service"]["extensions"]
    directory = config["extensions"]["file_storage"]["directory"]
    daemon = next(
        document for document in collector_tenancy.documents
        if document["kind"] == "DaemonSet"
    )
    pod = daemon["spec"]["template"]["spec"]
    collector = pod["containers"][0]
    user = collector["securityContext"]["runAsUser"]
    group = collector["securityContext"]["runAsGroup"]
    assert pod["securityContext"]["runAsNonRoot"]
    mount = next(
        mount for mount in collector["volumeMounts"]
        if mount["mountPath"] == directory
    )
    volume = next(
        volume for volume in pod["volumes"] if volume["name"] == mount["name"]
    )
    assert volume["hostPath"] == {"path": directory, "type": "DirectoryOrCreate"}
    init = next(
        container for container in pod["initContainers"]
        if container["name"] == "state-permissions"
    )
    assert init["command"][:4] == ["chown", "-R", f"{user}:{group}", directory]
    init_mount = next(mount for mount in init["volumeMounts"] if mount["mountPath"] == directory)
    init_volume = next(
        volume for volume in pod["volumes"]
        if volume["name"] == init_mount["name"]
    )
    assert init_volume["hostPath"] == volume["hostPath"]
    assert init["securityContext"]["capabilities"] == {
        "drop": ["ALL"], "add": ["CHOWN"]
    }
    assert not init["securityContext"]["allowPrivilegeEscalation"]


def test_disabling_log_checkpoints_keeps_volume_references_valid():
    documents = render_chart(
        "loco-worker",
        {"otel-col-daemon": {"presets": {"logsCollection": {"storeCheckpoints": False}}}},
    )
    config = collector_relays(documents)["otel-col-daemon-agent"]
    assert "storage" not in config["receivers"]["file_log"]
    daemon = next(document for document in documents if document["kind"] == "DaemonSet")
    pod = daemon["spec"]["template"]["spec"]
    volume_names = {volume["name"] for volume in pod["volumes"]}
    for container in pod["containers"] + pod.get("initContainers", []):
        for mount in container.get("volumeMounts", []):
            assert mount["name"] in volume_names


def test_application_metrics_use_node_local_otel_pulling(collector_tenancy):
    config = collector_tenancy.configs["otel-col-daemon-agent"]
    (scrape,) = config["receivers"]["prometheus/applications"]["config"][
        "scrape_configs"
    ]
    (discovery,) = scrape["kubernetes_sd_configs"]
    assert discovery["selectors"] == [
        {
            "role": "pod",
            "field": "spec.nodeName=${env:OTEL_K8S_NODE_NAME}",
            "label": "loco.io/metrics-port",
        }
    ]
    assert scrape["sample_limit"] > 0
    assert scrape["body_size_limit"]
    assert not scrape["honor_labels"]
    assert not scrape["follow_redirects"]
    pipeline = config["service"]["pipelines"]["metrics/applications"]
    assert pipeline["receivers"] == ["prometheus/applications"]
    processors = pipeline["processors"]
    enrichment = processors.index("k8s_attributes/pod_uid")
    assert processors.index("resource/strip_tenancy") < enrichment
    assert processors.index("transform/scrape_identity") < enrichment


def test_application_scrape_addresses_support_ipv4_and_ipv6(
    collector_tenancy, subtests
):
    config = collector_tenancy.configs["otel-col-daemon-agent"]
    (scrape,) = config["receivers"]["prometheus/applications"]["config"][
        "scrape_configs"
    ]
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


def test_all_collectors_render(collector_tenancy):
    assert set(collector_tenancy.configs) == set(COLLECTOR_CONFIGMAPS)


def test_every_pipeline_stamps_the_region_last(collector_tenancy, subtests):
    for collector, config, name, pipeline in _pipelines(collector_tenancy):
        with subtests.test(collector=collector, pipeline=name):
            assert pipeline["processors"][-2:] == ["resource/region", "batch"]
            assert config["processors"]["resource/region"] == {
                "attributes": [
                    {"key": "loco.region", "value": TEST_REGION, "action": "upsert"}
                ]
            }


def test_chart_requires_a_region(collector_tenancy):
    result = render_chart("loco-worker", {"global": {"region": ""}}, check=False)
    assert result.returncode != 0
    assert "global.region is required" in result.stderr


def test_memory_limiter_runs_first_and_batch_last(collector_tenancy):
    for collector, _, name, pipeline in _pipelines(collector_tenancy):
        processors = pipeline.get("processors", [])
        assert len(processors) >= 2, f"{collector} {name}"
        assert processors[0] == "memory_limiter", f"{collector} {name}"
        assert processors[-1] == "batch", f"{collector} {name}"


def test_push_receivers_have_their_own_pipelines(collector_tenancy):
    for collector, _, name, pipeline in _push_pipelines(collector_tenancy):
        assert set(pipeline["receivers"]) <= PUSH_RECEIVERS, f"{collector} {name}"


def test_client_tenancy_is_removed_before_kubernetes_attributes(collector_tenancy):
    stripped = (
        set(TENANT_LABELS.values())
        | set(BUILD_LABELS.values())
        | set(GATEWAY_LABELS.values())
        | set(CLIENT_POD_IDENTITY)
    )
    for collector, config, name, pipeline in _push_pipelines(collector_tenancy):
        processors = pipeline["processors"]
        enrichers = _k8s_attributes(collector_tenancy, pipeline)
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


def test_push_pipelines_associate_pods_by_connection_only(collector_tenancy):
    for collector, config, name, pipeline in _push_pipelines(collector_tenancy):
        for enricher in _k8s_attributes(collector_tenancy, pipeline):
            assert config["processors"][enricher]["pod_association"] == [
                {"sources": [{"from": "connection"}]}
            ], f"{collector} {name}"


def test_file_logs_associate_pods_by_uid(collector_tenancy):
    config = collector_tenancy.configs["otel-col-daemon-agent"]
    pipelines = [
        pipeline
        for pipeline in config["service"]["pipelines"].values()
        if "file_log" in pipeline["receivers"]
    ]
    assert len(pipelines) == 1
    enrichers = _k8s_attributes(collector_tenancy, pipelines[0])
    assert len(enrichers) == 1
    assert config["processors"][enrichers[0]]["pod_association"] == [
        {"sources": [{"from": "resource_attribute", "name": "k8s.pod.uid"}]}
    ]


def test_collectors_can_read_the_pod_metadata_they_extract(collector_tenancy):
    needed = {("", "pods"), ("", "namespaces"), ("apps", "replicasets")}
    roles = {
        document["metadata"]["name"]: document
        for document in collector_tenancy.documents
        if document["kind"] == "ClusterRole"
    }
    for collector in ("otel-col-daemon", "otel-col-deploy"):
        granted = {
            (group, resource)
            for rule in roles[collector]["rules"]
            if {"get", "list", "watch"} <= set(rule["verbs"])
            for group in rule["apiGroups"]
            for resource in rule["resources"]
        }
        assert needed <= granted, collector


def test_build_logs_keep_their_build_id(collector_tenancy):
    config = collector_tenancy.configs["otel-col-daemon-agent"]
    pipeline = next(
        (
            pipeline
            for pipeline in config["service"]["pipelines"].values()
            if "file_log" in pipeline["receivers"]
        )
    )
    extract = config["processors"][_k8s_attributes(collector_tenancy, pipeline)[0]][
        "extract"
    ]
    assert {
        "from": "pod",
        "key": "loco.io/build-id",
        "tag_name": "loco.io/build-id",
    } in extract["labels"]


def test_tenancy_comes_from_explicit_pod_labels(collector_tenancy):
    for collector, config, name, pipeline in _pipelines(collector_tenancy):
        for enricher in _k8s_attributes(collector_tenancy, pipeline):
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


@pytest.fixture(scope="module")
def cluster_collector():
    cluster_collector = SimpleNamespace()
    cluster_collector.documents = render_chart("loco-worker", {})
    cluster_collector.configs = collector_relays(cluster_collector.documents)
    return cluster_collector


def test_only_the_singleton_collects_cluster_metrics_and_events(cluster_collector):
    for receiver in ("k8s_cluster", "k8sobjects"):
        collectors = {
            name
            for name, config in cluster_collector.configs.items()
            if receiver in config["receivers"]
        }
        assert collectors == {"otel-col-cluster"}, receiver
    deployment = next(
        (
            document
            for document in cluster_collector.documents
            if document["kind"] == "StatefulSet"
            and document["metadata"]["name"] == "otel-col-cluster"
        )
    )
    assert deployment["spec"]["replicas"] == 1
    assert deployment["spec"]["updateStrategy"]["type"] == "RollingUpdate"
    assert not any(
        (
            document["kind"] == "HorizontalPodAutoscaler"
            and document["spec"]["scaleTargetRef"]["name"] == "otel-col-cluster"
            for document in cluster_collector.documents
        )
    )


def test_ingest_keeps_only_push_pipelines(cluster_collector):
    config = cluster_collector.configs["otel-col-deploy"]
    for pipeline in config["service"]["pipelines"].values():
        assert set(pipeline["receivers"]) <= PUSH_RECEIVERS


def test_ingest_has_cpu_requests_and_an_hpa():
    documents = render_chart("loco-worker", {})
    values = chart_values("loco-worker")["otel-col-deploy"]
    hpa = next(
        (
            document
            for document in documents
            if document["kind"] == "HorizontalPodAutoscaler"
            and document["spec"]["scaleTargetRef"]["name"] == "otel-col-deploy"
        )
    )
    assert hpa["spec"]["minReplicas"] == values["autoscaling"]["minReplicas"]
    assert hpa["spec"]["maxReplicas"] == values["autoscaling"]["maxReplicas"]
    assert hpa["spec"]["metrics"] == [
        {
            "type": "Resource",
            "resource": {
                "name": "cpu",
                "target": {
                    "type": "Utilization",
                    "averageUtilization": values["autoscaling"][
                        "targetCPUUtilizationPercentage"
                    ],
                },
            },
        }
    ]
    deployment = next(
        (
            document
            for document in documents
            if document["kind"] == "StatefulSet"
            and document["metadata"]["name"] == "otel-col-deploy"
        )
    )
    assert "replicas" not in deployment["spec"]
    resources = deployment["spec"]["template"]["spec"]["containers"][0]["resources"]
    for resource in ("cpu", "memory"):
        assert quantity(resources["requests"][resource]) > 0
        assert quantity(resources["requests"][resource]) <= quantity(
            resources["limits"][resource]
        )


def test_disabling_autoscaling_restores_the_explicit_replica_count():
    documents = render_chart(
        "loco-worker",
        {"otel-col-deploy": {"autoscaling": {"enabled": False}, "replicaCount": 2}},
    )
    assert not any(
        (document["kind"] == "HorizontalPodAutoscaler" for document in documents)
    )
    deployment = next(
        (
            document
            for document in documents
            if document["kind"] == "StatefulSet"
            and document["metadata"]["name"] == "otel-col-deploy"
        )
    )
    assert deployment["spec"]["replicas"] == 2


@pytest.fixture(scope="module")
def envoy_telemetry():
    envoy_telemetry = SimpleNamespace()
    documents = render_documents("loco-worker", "gateway.yaml", {})
    envoy_telemetry.proxy = next(
        (document for document in documents if document["kind"] == "EnvoyProxy")
    )
    envoy_telemetry.core = chart_values("loco-worker")
    envoy_telemetry.collector = collector_relays(render_chart("loco-worker", {}))[
        "otel-col-deploy"
    ]
    return envoy_telemetry


def _collector_address(envoy_telemetry):
    deploy = envoy_telemetry.core["otel-col-deploy"]
    return (
        f"{deploy['fullnameOverride']}.{deploy['namespaceOverride']}.svc.cluster.local",
        deploy["ports"]["otlp"]["servicePort"],
    )


def test_envoy_sends_every_signal_to_the_deployment_collector(envoy_telemetry):
    host, port = _collector_address(envoy_telemetry)
    assert (
        envoy_telemetry.collector["receivers"]["otlp"]["protocols"]["grpc"]["endpoint"]
        == f"0.0.0.0:{port}"
    )
    telemetry = envoy_telemetry.proxy["spec"]["telemetry"]
    sinks = [sink["openTelemetry"] for sink in telemetry["metrics"]["sinks"]]
    sinks += [
        sink["openTelemetry"]
        for setting in telemetry["accessLog"]["settings"]
        for sink in setting["sinks"]
    ]
    sinks.append(telemetry["tracing"]["provider"])
    for sink in sinks:
        assert (sink["host"], sink["port"]) == (host, port)
    assert telemetry["tracing"]["provider"]["type"] == "OpenTelemetry"
    assert all(
        (
            sink["type"] == "OpenTelemetry"
            for setting in telemetry["accessLog"]["settings"]
            for sink in setting["sinks"]
        )
    )


def test_gateway_service_settings_are_left_to_envoy_by_default(envoy_telemetry):
    assert "provider" not in envoy_telemetry.proxy["spec"]


def test_gateway_service_accepts_native_nodeport_configuration(envoy_telemetry):
    service = {
        "type": "NodePort",
        "externalTrafficPolicy": "Cluster",
        "patch": {
            "type": "StrategicMerge",
            "value": {"spec": {"ports": [{"port": 80, "nodePort": 30080}]}},
        },
    }
    documents = render_documents(
        "loco-worker", "gateway.yaml", {"envoyProxy": {"service": service}}
    )
    proxy = next(
        (document for document in documents if document["kind"] == "EnvoyProxy")
    )
    assert proxy["spec"]["provider"] == {
        "type": "Kubernetes",
        "kubernetes": {"envoyService": service},
    }


def test_tracing_samples_at_the_configured_rate(envoy_telemetry):
    tracing = envoy_telemetry.proxy["spec"]["telemetry"]["tracing"]
    assert (
        tracing["samplingRate"]
        == envoy_telemetry.core["envoyProxy"]["tracing"]["samplingRate"]
    )


def test_access_logs_carry_the_request_and_its_upstream(envoy_telemetry):
    settings = envoy_telemetry.proxy["spec"]["telemetry"]["accessLog"]["settings"]
    assert len(settings) == 1
    log_format = settings[0]["format"]
    assert log_format["type"] == "JSON"
    assert (
        log_format["json"]
        == envoy_telemetry.core["envoyProxy"]["accessLog"]["attributes"]
    )
    operators = set(log_format["json"].values())
    for operator in (
        "%UPSTREAM_CLUSTER%",
        "%RESPONSE_CODE%",
        "%DURATION%",
        "%REQ(:METHOD)%",
        "%TRACE_ID%",
    ):
        assert operator in operators
    assert any(("PATH" in operator for operator in operators))
    assert log_format["json"]["upstream_cluster"] == "%UPSTREAM_CLUSTER%"


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


def test_controller_reads_the_collector_from_values():
    deployment = render(
        "loco-worker",
        "controller/deployment.yaml",
        {
            "controller": {"image": {"tag": "test"}},
            "otel-col-deploy": {
                "namespaceOverride": "telemetry",
                "fullnameOverride": "collector",
                "ports": {"otlp": {"servicePort": 14317}, "otlp-http": {"servicePort": 14318}},
            },
        },
    )
    env = {
        item["name"]: item
        for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"]
    }
    assert env["LOCO_OBSERVABILITY_NAMESPACE"]["value"] == "telemetry"
    assert env["LOCO_OTEL_COLLECTOR_SERVICE"]["value"] == "collector"
    assert env["LOCO_OTEL_COLLECTOR_GRPC_PORT"]["value"] == "14317"
    assert env["LOCO_OTEL_COLLECTOR_HTTP_PORT"]["value"] == "14318"


def test_collector_service_ports_reach_the_receivers():
    deploy = chart_values("loco-worker")["otel-col-deploy"]
    protocols = deploy["config"]["receivers"]["otlp"]["protocols"]
    assert protocols["grpc"]["endpoint"].rsplit(":", 1)[1] == str(deploy["ports"]["otlp"]["servicePort"])
    assert protocols["http"]["endpoint"].rsplit(":", 1)[1] == str(deploy["ports"]["otlp-http"]["servicePort"])


def _deployment():
    return render(
        "loco-control-plane", "obs-proxy-query-deployment.yaml", {"obsProxy": TEST_PROXY}
    )


def test_proxy_has_no_kubernetes_api_credentials():
    pod = _deployment()["spec"]["template"]["spec"]
    assert "serviceAccountName" not in pod
    assert pod["automountServiceAccountToken"] is False


def test_chart_creates_build_namespace_by_default():
    builds = chart_values("loco-worker")["builds"]
    namespace = render("loco-worker", "builds/namespace.yaml", {})
    assert namespace["kind"] == "Namespace"
    assert namespace["metadata"]["name"] == builds["namespace"]
    assert (
        namespace["metadata"]["labels"]["pod-security.kubernetes.io/enforce"]
        == builds["podSecurity"]["level"]
    )


def test_gitops_can_own_build_namespace():
    namespace = render(
        "loco-worker", "builds/namespace.yaml", {"builds": {"createNamespace": False}}
    )
    assert namespace is None


def test_file_logs_extract_application_severity(collector_tenancy):
    for _, config, _, pipeline in _pipelines(collector_tenancy):
        if "file_log" not in pipeline["receivers"]:
            continue
        processors = pipeline["processors"]
        assert "transform/log_severity" in processors
        assert processors.index("transform/log_severity") > processors.index("memory_limiter")
        assert processors.index("transform/log_severity") < processors.index("batch")
        assert "log_statements" in config["processors"]["transform/log_severity"]
