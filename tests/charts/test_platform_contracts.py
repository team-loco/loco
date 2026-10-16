import json
import os
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
    "clickhouse": {
        "existingSecret": {"name": "proxy-reader", "key": "url"},
        "migratorExistingSecret": {"name": "proxy-migrator", "key": "url"},
    },
}
TEST_REGION = "test-region-1"
OBS_BASE_VALUES = {"global": {"region": TEST_REGION}}
CLICKHOUSE_USER_PASSWORDS = {
    "CLICKHOUSE_MIGRATOR_PASSWORD": "test",
    "CLICKHOUSE_INGEST_PASSWORD": "test",
    "CLICKHOUSE_READER_PASSWORD": "test",
    "CLICKHOUSE_WORKLOAD_INGEST_PASSWORD": "test",
}


def environment_values(environment, variables, chart="loco-obs"):
    with tempfile.TemporaryDirectory() as directory:
        state = Path(directory) / "helmfile.yaml"
        config = json.dumps(
            {
                "releases": [
                    {
                        "name": chart,
                        "chart": str(ROOT / "charts" / chart),
                        "values": [
                            str(
                                ROOT
                                / "env"
                                / environment
                                / (chart.removeprefix("loco-") + "-chart.yaml.gotmpl")
                            )
                        ],
                    }
                ]
            }
        )
        state.write_text(
            subprocess.check_output(["yq", "-P", ".", "-"], input=config, text=True)
        )
        built = subprocess.check_output(
            ["helmfile", "-f", str(state), "build", "--embed-values"],
            env={**os.environ, **variables},
            text=True,
        )
        document = subprocess.check_output(
            ["yq", "-o=json", ".", "-"], input=built, text=True
        )
        return json.loads(document)["releases"][0]["values"][0]


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


def render_documents(chart, template, overrides):
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
        base.write_text(json.dumps(OBS_BASE_VALUES if chart == "loco-obs" else {}))
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


def clickhouse_installation(overrides):
    for document in render_chart("loco-obs", overrides):
        if document["kind"] == "ClickHouseInstallation":
            return document
    raise AssertionError("loco-obs renders no ClickHouseInstallation")


def clickhouse_resources(overrides):
    containers = clickhouse_installation(overrides)["spec"]["templates"][
        "podTemplates"
    ][0]["spec"]["containers"]
    return next(
        (
            container["resources"]
            for container in containers
            if container["name"] == "clickhouse"
        )
    )


def local_obs_values():
    return environment_values("local", {"APP_PORT": "8080"})


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
        "loco-core",
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
        "loco-control-plane", "obs-proxy-deployment.yaml", {"obsProxy": TEST_PROXY}
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
    assert "PROXY_AUTH_TOKEN" not in env


def test_missing_clickhouse_secrets_are_rejected(subtests):
    for secret in ("existingSecret", "migratorExistingSecret"):
        with subtests.test(secret=secret):
            clickhouse = {
                **TEST_PROXY["clickhouse"],
                secret: {"name": "", "key": "url"},
            }
            with pytest.raises(subprocess.CalledProcessError) as caught:
                render(
                    "loco-control-plane",
                    "obs-proxy-deployment.yaml",
                    {"obsProxy": {**TEST_PROXY, "clickhouse": clickhouse}},
                )
            assert (
                f"obsProxy.clickhouse.{secret}.name is required" in caught.value.output
            )


def test_obs_chart_runs_no_proxy():
    names = {document["metadata"]["name"] for document in render_chart("loco-obs", {})}
    assert not {name for name in names if "obs-proxy" in name}


def test_missing_database_secret_key_is_rejected():
    with pytest.raises(subprocess.CalledProcessError):
        render(
            "loco-control-plane",
            "obs-proxy-deployment.yaml",
            {
                "obsProxy": {
                    **TEST_PROXY,
                    "clickhouse": {
                        **TEST_PROXY["clickhouse"],
                        "existingSecret": {"name": "db", "key": ""},
                    },
                }
            },
        )


def test_permission_endpoint_uses_the_private_api_service():
    deployment = render(
        "loco-control-plane", "obs-proxy-deployment.yaml", {"obsProxy": TEST_PROXY}
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
    values = {"obsProxy": dict(TEST_PROXY)}
    for key, value in (overrides or {}).items():
        values["obsProxy"][key] = value
    deployment = render("loco-control-plane", "obs-proxy-deployment.yaml", values)
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


def test_proxy_uses_its_built_in_schema_settings():
    container, _, env = _proxy_env()
    built_in = {
        "CLICKHOUSE_DB",
        "CLICKHOUSE_LOGS_TTL",
        "CLICKHOUSE_TRACES_TTL",
        "CLICKHOUSE_METRICS_TTL",
        "CLICKHOUSE_EVENTS_TTL",
        "MIGRATION_RETRY_BUDGET",
        "MIGRATION_RETRY_INTERVAL",
        "MIGRATION_LOCK_TTL",
        "MIGRATION_LOCK_RETRY_PERIOD",
    }
    assert not built_in & set(env), "the chart sets settings the proxy defaults itself"
    assert "PERMISSION_CHECK_TIMEOUT" not in env
    assert container["readinessProbe"]["httpGet"]["path"] == "/readyz"
    assert "startupProbe" not in container


def test_user_secrets_follow_the_clickhouse_users():
    passwords = {
        "loco_migrator": "migrator-pw",
        "loco_ingest": "ingest-pw",
        "loco_workload_ingest": "workload-pw",
        "loco_reader": "reader-pw",
    }
    secrets = render_documents(
        "loco-obs", "clickhouse-users.yaml", {"clickhouseUserPasswords": passwords}
    )
    found = {
        (item["metadata"]["namespace"], item["metadata"]["name"]): item["stringData"][
            "password"
        ]
        for item in secrets
    }
    assert found == {
        ("platform-test", "loco-obs-clickhouse-migrator"): "migrator-pw",
        ("platform-test", "loco-obs-clickhouse-ingest"): "ingest-pw",
        ("platform-test", "loco-obs-clickhouse-workload-ingest"): "workload-pw",
        ("platform-test", "loco-obs-clickhouse-reader"): "reader-pw",
        ("observability-node", "loco-obs-clickhouse-ingest"): "ingest-pw",
    }


def test_user_secrets_are_left_to_the_cluster_without_passwords():
    assert render_documents("loco-obs", "clickhouse-users.yaml", {}) == []


def test_passwords_that_break_a_dsn_are_rejected():
    with pytest.raises(subprocess.CalledProcessError):
        render_documents(
            "loco-obs",
            "clickhouse-users.yaml",
            {"clickhouseUserPasswords": {"loco_reader": "a b@c"}},
        )


def test_clickhouse_has_requests_and_limits():
    resources = clickhouse_resources({})
    for kind in ("requests", "limits"):
        assert set(resources[kind]) == {"cpu", "memory"}, kind
    assert resources["requests"]["memory"] == resources["limits"]["memory"]


def test_local_clickhouse_is_smaller_than_the_default():
    default = clickhouse_resources({})
    local = clickhouse_resources(local_obs_values())
    for kind in ("requests", "limits"):
        for resource in ("cpu", "memory"):
            assert quantity(local[kind][resource]) < quantity(
                default[kind][resource]
            ), f"{kind}.{resource}"


def test_local_clickhouse_caps_its_caches_and_schedule_pool():
    capped = {
        "mark_cache_size",
        "index_mark_cache_size",
        "primary_index_cache_size",
        "uncompressed_cache_size",
        "background_schedule_pool_size",
        "max_concurrent_queries",
    }
    local = clickhouse_installation(local_obs_values())["spec"]["configuration"].get(
        "settings", {}
    )
    default = clickhouse_installation({})["spec"]["configuration"].get("settings", {})
    assert capped - set(local) == set()
    for setting in capped:
        assert re.search("^[0-9]+$", str(local[setting])), setting
    assert capped & set(default) == set()


def test_in_cluster_clickhouse_is_the_default_endpoint():
    documents = render_chart("loco-obs", {})
    host = "clickhouse-loco-obs-clickhouse.platform-test.svc.cluster.local:9000"
    for collector, config in collector_relays(documents).items():
        exporter = config["exporters"]["clickhouse"]
        assert exporter["endpoint"] == f"tcp://{host}", collector
        assert exporter["connection_params"]["secure"] == "false", collector
    assert "ClickHouseInstallation" in {document["kind"] for document in documents}


def test_external_clickhouse_replaces_the_in_cluster_server():
    overrides = {
        "clickhouse": {"enabled": False},
        "global": {
            "clickhouse": {
                "host": "abc.clickhouse.example.test",
                "port": 9440,
                "secure": True,
            }
        },
    }
    documents = render_chart("loco-obs", overrides)
    host = "abc.clickhouse.example.test:9440"
    for collector, config in collector_relays(documents).items():
        exporter = config["exporters"]["clickhouse"]
        assert exporter["endpoint"] == f"tcp://{host}", collector
        assert exporter["connection_params"]["secure"] == "true", collector
    names = {document["metadata"]["name"] for document in documents}
    kinds = {document["kind"] for document in documents}
    assert "ClickHouseInstallation" not in kinds
    assert "allow-clickhouse-ingress" not in names


def test_collectors_set_connection_options_as_exporter_fields():
    for collector, config in collector_relays(render_chart("loco-obs", {})).items():
        exporter = config["exporters"]["clickhouse"]
        assert "?" not in exporter["endpoint"], collector
        assert exporter["compress"] == "lz4", collector
        assert exporter["async_insert"], collector
        assert exporter["connection_params"] == {
            "dial_timeout": "10s",
            "log_queries": "0",
            "secure": "false",
        }, collector


def test_external_clickhouse_needs_a_host():
    with pytest.raises(subprocess.CalledProcessError):
        render_chart("loco-obs", {"clickhouse": {"enabled": False}})


def test_collectors_ingest_into_the_database_the_users_are_granted():
    values = chart_values("loco-obs")
    database = values["otel-col-daemon"]["config"]["exporters"]["clickhouse"][
        "database"
    ]
    users = {user["name"]: user for user in values["clickhouse"]["clickhouse"]["users"]}
    for user in users.values():
        assert all((f" ON {database}." in grant for grant in user["grants"])), (
            user["name"]
        )
    for collector in ("otel-col-daemon", "otel-col-deploy"):
        config = values[collector]["config"]
        exporter = config["exporters"]["clickhouse"]
        assert not exporter["create_schema"], collector
        assert exporter["database"] == database, collector
        assert "ttl" not in exporter, collector
        assert config["service"]["pipelines"]["traces"]["exporters"] == [
            "clickhouse"
        ], collector
        user = users[exporter["username"]]
        env = {item["name"]: item for item in values[collector]["extraEnvs"]}
        assert env["CLICKHOUSE_INGEST_PASSWORD"]["valueFrom"]["secretKeyRef"] == {
            "name": user["password_secret_name"],
            "key": "password",
        }, collector
        assert exporter["password"] == "${env:CLICKHOUSE_INGEST_PASSWORD}", collector
    for user in users.values():
        for grant in user["grants"]:
            assert f" ON {database}." in grant, user["name"]


@pytest.fixture(scope="module")
def collector_tenancy():
    collector_tenancy = SimpleNamespace()
    collector_tenancy.documents = render_chart("loco-obs", {})
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
    assert collector["securityContext"]["runAsUser"] == 10001
    assert collector["securityContext"]["runAsGroup"] == 0
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
        if container["name"] == "log-checkpoint-permissions"
    )
    assert init["command"] == ["chown", "-R", "10001:0", directory]
    (init_mount,) = init["volumeMounts"]
    assert init_mount["mountPath"] == directory
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
        "loco-obs",
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
    assert scrape["sample_limit"] == 10000
    assert not scrape["honor_labels"]
    assert not scrape["follow_redirects"]
    assert scrape["body_size_limit"] == "10MB"
    pipeline = config["service"]["pipelines"]["metrics/applications"]
    assert pipeline["receivers"] == ["prometheus/applications"]
    assert pipeline["processors"] == [
        "memory_limiter",
        "resource/strip_tenancy",
        "transform/scrape_identity",
        "k8s_attributes/pod_uid",
        "resource/region",
        "batch",
    ]
    assert pipeline["exporters"] == ["clickhouse"]


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
            if name.startswith("logs/workload_"):
                assert pipeline["processors"][-1] == "resource/region"
            else:
                assert pipeline["processors"][-2:] == ["resource/region", "batch"]
            assert config["processors"]["resource/region"] == {
                "attributes": [
                    {"key": "loco.region", "value": TEST_REGION, "action": "upsert"}
                ]
            }


def test_chart_requires_a_region(collector_tenancy):
    result = render_chart("loco-obs", {"global": {"region": ""}}, check=False)
    assert result.returncode != 0
    assert "global.region is required" in result.stderr


def test_every_environment_sets_a_region(collector_tenancy):
    local = environment_values("local", {"APP_PORT": "8080"})
    assert local["global"]["region"]
    prod = environment_values(
        "prod",
        {
            "GH_SHA": "test",
            "CLICKHOUSE_PASSWORD": "test",
            "CONTROL_PLANE_URL": "https://api.example.test",
            "LOCO_REGION": TEST_REGION,
            **CLICKHOUSE_USER_PASSWORDS,
        },
    )
    assert prod["global"]["region"] == TEST_REGION


def test_collectors_run_without_an_operator(collector_tenancy):
    names = {
        document["metadata"].get("labels", {}).get("app.kubernetes.io/name")
        for document in collector_tenancy.documents
    }
    kinds = {document["kind"] for document in collector_tenancy.documents}
    assert "opentelemetry-operator" not in names
    assert (
        not {"MutatingWebhookConfiguration", "ValidatingWebhookConfiguration"} & kinds
    )


def test_memory_limiter_runs_first_and_batch_last(collector_tenancy):
    for collector, _, name, pipeline in _pipelines(collector_tenancy):
        processors = pipeline.get("processors", [])
        assert len(processors) >= 2, f"{collector} {name}"
        assert processors[0] == "memory_limiter", f"{collector} {name}"
        if name.startswith("logs/workload_"):
            assert "batch" not in processors, f"{collector} {name}"
        else:
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
            if enricher == "k8s_attributes/observer":
                assert extract == {"metadata": ["k8s.cluster.uid"]}
                continue
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
    cluster_collector.documents = render_chart("loco-obs", {})
    cluster_collector.configs = collector_relays(cluster_collector.documents)
    return cluster_collector


def test_only_the_singleton_collects_cluster_metrics_and_events(cluster_collector):
    for receiver in ("k8s_cluster", "k8sobjects/workload_events", "k8sobjects/workload_owners", "k8sobjects/workload_owners_refresh"):
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
            if document["kind"] == "Deployment"
            and document["metadata"]["name"] == "otel-col-cluster"
        )
    )
    assert deployment["spec"]["replicas"] == 1
    assert deployment["spec"]["strategy"] == {"type": "Recreate"}
    assert not any(
        (
            document["kind"] == "HorizontalPodAutoscaler"
            and document["spec"]["scaleTargetRef"]["name"] == "otel-col-cluster"
            for document in cluster_collector.documents
        )
    )


def test_the_singleton_accepts_no_pushed_telemetry(cluster_collector):
    config = cluster_collector.configs["otel-col-cluster"]
    assert not PUSH_RECEIVERS & set(config["receivers"])
    for pipeline in config["service"]["pipelines"].values():
        assert not PUSH_RECEIVERS & set(pipeline["receivers"])
    services = [
        document
        for document in cluster_collector.documents
        if document["kind"] == "Service"
        and document["metadata"]["name"] == "otel-col-cluster"
    ]
    assert not {4317, 4318, 6831, 14250, 14268, 9411} & {
        port["port"]
        for service in services
        for port in service["spec"].get("ports", [])
    }


def test_ingest_keeps_only_push_pipelines(cluster_collector):
    config = cluster_collector.configs["otel-col-deploy"]
    for pipeline in config["service"]["pipelines"].values():
        assert set(pipeline["receivers"]) <= PUSH_RECEIVERS


def test_ingest_has_cpu_requests_and_an_hpa():
    documents = render_chart("loco-obs", {})
    values = chart_values("loco-obs")["otel-col-deploy"]
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
            if document["kind"] == "Deployment"
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
        "loco-obs",
        {"otel-col-deploy": {"autoscaling": {"enabled": False}, "replicaCount": 2}},
    )
    assert not any(
        (document["kind"] == "HorizontalPodAutoscaler" for document in documents)
    )
    deployment = next(
        (
            document
            for document in documents
            if document["kind"] == "Deployment"
            and document["metadata"]["name"] == "otel-col-deploy"
        )
    )
    assert deployment["spec"]["replicas"] == 2


@pytest.fixture(scope="module")
def envoy_telemetry():
    envoy_telemetry = SimpleNamespace()
    documents = render_documents("loco-core", "gateway.yaml", {})
    envoy_telemetry.proxy = next(
        (document for document in documents if document["kind"] == "EnvoyProxy")
    )
    envoy_telemetry.core = chart_values("loco-core")
    envoy_telemetry.collector = collector_relays(render_chart("loco-obs", {}))[
        "otel-col-deploy"
    ]
    return envoy_telemetry


def _collector_address(envoy_telemetry):
    observability = envoy_telemetry.core["global"]["observability"]
    return (
        f"{observability['otelCollectorDeployment']}.{observability['namespace']}.svc.cluster.local",
        observability["otelCollectorGrpcPort"],
    )


def test_envoy_sends_every_signal_to_the_deployment_collector(envoy_telemetry):
    obs = chart_values("loco-obs")
    host, port = _collector_address(envoy_telemetry)
    assert host.split(".")[0] == obs["otel-col-deploy"]["fullnameOverride"]
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
        "loco-core", "gateway.yaml", {"envoyProxy": {"service": service}}
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
        "loco-operator",
        "controller/deployment.yaml",
        {
            "controller": {"image": {"tag": "test"}},
            "observability": {
                "namespace": "telemetry",
                "collector": {
                    "service": "collector",
                    "grpcPort": 14317,
                    "httpPort": 14318,
                },
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


def test_operator_collector_matches_the_obs_chart():
    collector = chart_values("loco-operator")["observability"]["collector"]
    deploy = chart_values("loco-obs")["otel-col-deploy"]
    assert collector["service"] == deploy["fullnameOverride"]
    metrics = chart_values("loco-operator")["observability"]["metricsCollector"]
    daemon = chart_values("loco-obs")["otel-col-daemon"]
    assert metrics["namespace"] == daemon["namespaceOverride"]
    assert metrics["service"] == daemon["fullnameOverride"]
    protocols = deploy["config"]["receivers"]["otlp"]["protocols"]
    assert protocols["grpc"]["endpoint"].rsplit(":", 1)[1] == str(collector["grpcPort"])
    assert protocols["http"]["endpoint"].rsplit(":", 1)[1] == str(collector["httpPort"])


def _deployment():
    return render(
        "loco-control-plane", "obs-proxy-deployment.yaml", {"obsProxy": TEST_PROXY}
    )


def test_proxy_has_no_kubernetes_api_credentials():
    pod = _deployment()["spec"]["template"]["spec"]
    assert "serviceAccountName" not in pod
    assert pod["automountServiceAccountToken"] is False


def test_in_cluster_clickhouse_runs_a_single_node_keeper_for_the_lock():
    installation = clickhouse_installation({})
    config = installation["spec"]["configuration"]["files"]["config.d/extra_config.xml"]
    assert "<keeper_server>" in config
    assert "<keeper_map_path_prefix>" in config
    assert (
        installation["spec"]["configuration"]["clusters"][0]["layout"]["replicasCount"]
        == 1
    )


def test_chart_creates_build_namespace_by_default():
    namespace = render("loco-operator", "builds/namespace.yaml", {})
    assert namespace["kind"] == "Namespace"
    assert namespace["metadata"]["name"] == "loco-builds"
    assert (
        namespace["metadata"]["labels"]["pod-security.kubernetes.io/enforce"]
        == "privileged"
    )


def test_gitops_can_own_build_namespace():
    namespace = render(
        "loco-operator", "builds/namespace.yaml", {"builds": {"createNamespace": False}}
    )
    assert namespace is None


@pytest.mark.parametrize(
    "overrides",
    [
        {"replicaCount": 2},
        {"autoscaling": {"enabled": True}},
        {"rollout": {"strategy": "RollingUpdate"}},
        {"mode": "daemonset"},
    ],
)
def test_workload_queue_requires_one_persistent_owner(overrides):
    result = render_chart("loco-obs", {"otel-col-cluster": overrides}, check=False)
    assert result.returncode != 0
    assert "workload event state requires one Deployment replica" in result.stderr


def test_workload_metadata_isolated_from_application_ingestion(collector_tenancy):
    values = chart_values("loco-obs")
    users = {user["name"]: user for user in values["clickhouse"]["clickhouse"]["users"]}
    workload_tables = {"otel_workload_events", "otel_workload_owners"}
    expected = {
        f"GRANT INSERT, SHOW COLUMNS ON loco_obs.{table}" for table in workload_tables
    }
    assert set(users["loco_workload_ingest"]["grants"]) == expected
    assert all(
        "*" not in grant and not any(table in grant for table in workload_tables)
        for grant in users["loco_ingest"]["grants"]
    )
    config = collector_tenancy.configs["otel-col-cluster"]
    for suffix in ("events", "owners"):
        pipeline = config["service"]["pipelines"][f"logs/workload_{suffix}"]
        assert pipeline["exporters"] == [f"clickhouse/workload_{suffix}"]
        exporter = config["exporters"][pipeline["exporters"][0]]
        assert exporter["username"] == "loco_workload_ingest"
        assert exporter["logs_table_name"] == f"otel_workload_{suffix}"
        assert not exporter["create_schema"]
        assert exporter["sending_queue"]["block_on_overflow"]
        assert exporter["sending_queue"]["storage"] == "file_storage/workload_events"
        assert exporter["retry_on_failure"]["max_elapsed_time"] == "0s"
        assert config["processors"][f"transform/workload_{suffix}"]["error_mode"] == "propagate"
    events = config["receivers"]["k8sobjects/workload_events"]["objects"]
    assert events == [{"name": "events", "group": "events.k8s.io", "mode": "watch"}]


def test_workload_state_and_application_reader_share_exact_identity(collector_tenancy):
    documents = collector_tenancy.documents
    claim = next(document for document in documents if document["kind"] == "PersistentVolumeClaim")
    assert claim["spec"]["accessModes"] == ["ReadWriteOnce"]
    deployment = next(
        document for document in documents
        if document["kind"] == "Deployment" and document["metadata"]["name"] == "otel-col-cluster"
    )
    pod = deployment["spec"]["template"]["spec"]
    volume = next(volume for volume in pod["volumes"] if volume["name"] == "workload-event-state")
    assert volume["persistentVolumeClaim"]["claimName"] == claim["metadata"]["name"]
    assert pod["securityContext"]["runAsNonRoot"]
    assert pod["securityContext"]["fsGroup"] == 10001
    binding = next(
        document for document in documents
        if document["kind"] == "RoleBinding" and document["metadata"]["name"].endswith("-application-inventory")
    )
    assert binding["metadata"]["namespace"] == "loco-system"
    assert binding["subjects"] == [{
        "kind": "ServiceAccount", "name": pod["serviceAccountName"], "namespace": "platform-test"
    }]
    role = next(
        document for document in documents
        if document["kind"] == "Role" and document["metadata"]["name"] == binding["roleRef"]["name"]
    )
    assert role["rules"] == [{
        "apiGroups": ["infra.loco.io"], "resources": ["applications"], "verbs": ["get", "list", "watch"]
    }]


@pytest.mark.parametrize("installation", ["e2e", "local", "prod"])
def test_installations_supply_every_enabled_collector_password_secret(installation):
    if installation == "e2e":
        overrides = json.loads(subprocess.check_output(
            ["yq", "-o=json", ".", str(ROOT / "e2e" / "obs-values.yaml")], text=True
        ))
    else:
        overrides = environment_values(installation, {
            "APP_PORT": "8080", "GH_SHA": "test", "CLICKHOUSE_PASSWORD": "test",
            "CONTROL_PLANE_URL": "https://api.example.test", "LOCO_REGION": TEST_REGION,
            **CLICKHOUSE_USER_PASSWORDS,
        })
    documents = render_chart("loco-obs", overrides)
    secrets = {
        (document["metadata"].get("namespace", "platform-test"), document["metadata"]["name"]): document
        for document in documents if document["kind"] == "Secret"
    }
    for document in documents:
        if document["kind"] not in {"Deployment", "DaemonSet"}:
            continue
        if document["metadata"]["name"] not in {"otel-col-cluster", "otel-col-daemon-agent", "otel-col-deploy"}:
            continue
        namespace = document["metadata"].get("namespace", "platform-test")
        for container in document["spec"]["template"]["spec"]["containers"]:
            for variable in container.get("env", []):
                reference = variable.get("valueFrom", {}).get("secretKeyRef")
                if reference:
                    secret = secrets[(namespace, reference["name"])]
                    assert secret["stringData"][reference["key"]]


@pytest.mark.parametrize("service_account", ["workload-observer", "default"])
def test_application_inventory_binding_follows_collector_service_account(service_account):
    documents = render_chart("loco-obs", {
        "otel-col-cluster": {"serviceAccount": {
            "name": service_account, "create": service_account != "default"
        }}
    })
    deployment = next(
        document for document in documents
        if document["kind"] == "Deployment" and document["metadata"]["name"] == "otel-col-cluster"
    )
    binding = next(
        document for document in documents
        if document["kind"] == "RoleBinding" and document["metadata"]["name"].endswith("-application-inventory")
    )
    assert deployment["spec"]["template"]["spec"]["serviceAccountName"] == service_account
    assert binding["subjects"] == [{
        "kind": "ServiceAccount", "name": service_account, "namespace": "platform-test"
    }]



def test_workload_state_rejects_a_different_collector_namespace():
    result = render_chart("loco-obs", {
        "otel-col-cluster": {"namespaceOverride": "another-namespace"}
    }, check=False)
    assert result.returncode != 0
    assert "namespaceOverride must resolve to the loco-obs release namespace" in result.stderr
