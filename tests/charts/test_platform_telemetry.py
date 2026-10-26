import pytest

from test_platform_contracts import collector_relays, render_chart

IDENTITY = {
    "enabled": True,
    "installation": "test-installation",
    "cluster": "test-cluster",
    "environment": "test",
}


def configured(**settings):
    return {"global": {"platformTelemetry": {**IDENTITY, **settings}}}


def test_platform_collection_is_opt_in():
    for config in collector_relays(render_chart("loco-worker", {})).values():
        assert "metrics/platform" not in config["service"]["pipelines"]


def test_platform_metrics_share_the_cluster_exporter():
    configs = collector_relays(render_chart("loco-worker", configured()))
    for name in ("otel-col-daemon-agent", "otel-col-cluster"):
        config = configs[name]
        pipeline = config["service"]["pipelines"]["metrics/platform"]
        assert pipeline["exporters"] == ["otlp_http"]
        assert config["service"]["pipelines"]["metrics"]["exporters"] == ["otlp_http"]
        assert "filter/platform" in pipeline["processors"]
        assert "resource/platform" in pipeline["processors"]
    assert "metrics/platform" not in configs["otel-col-deploy"]["service"]["pipelines"]


@pytest.mark.parametrize("field", ["installation", "cluster", "environment"])
def test_platform_identity_is_required(field):
    result = render_chart("loco-worker", configured(**{field: ""}), check=False)
    assert result.returncode != 0
    assert field in result.stderr


def test_platform_only_keeps_only_platform_pipelines():
    values = configured(platformOnly=True)
    values.update({"otel-col-deploy": {"enabled": False}})
    configs = collector_relays(render_chart("loco-worker", values))
    for config in configs.values():
        assert set(config["service"]["pipelines"]) <= {"metrics/platform", "logs/platform"}
        assert set(config["exporters"]) == {"otlp_http"}


def test_application_pipelines_keep_their_configuration():
    original = collector_relays(render_chart("loco-worker", {}))
    enabled = collector_relays(render_chart("loco-worker", configured()))
    for name, config in original.items():
        for pipeline, settings in config["service"]["pipelines"].items():
            assert enabled[name]["service"]["pipelines"][pipeline] == settings


def test_platform_identity_and_capacity_are_exported():
    configs = collector_relays(render_chart("loco-worker", configured()))
    for name in ("otel-col-daemon-agent", "otel-col-cluster"):
        attributes = {item["key"]: item["value"] for item in configs[name]["processors"]["resource/platform"]["attributes"]}
        assert attributes == {
            "loco.installation.id": "test-installation",
            "k8s.cluster.name": "test-cluster",
            "deployment.environment.name": "test",
            "loco.region": "test-region-1",
            "loco.telemetry.scope": "platform",
        }
    receiver = configs["otel-col-cluster"]["receivers"]["k8s_cluster"]
    assert receiver["node_conditions_to_report"] == ["Ready", "MemoryPressure", "DiskPressure", "PIDPressure"]
    assert receiver["allocatable_types_to_report"] == ["cpu", "memory", "ephemeral-storage", "pods"]


def test_changing_platform_settings_restarts_the_collectors():
    checksums = []
    for interval in ("30s", "60s"):
        workloads = [document for document in render_chart("loco-worker", configured(collectionInterval=interval))
                     if document["kind"] in ("DaemonSet", "StatefulSet") and document["metadata"]["name"] in ("otel-col-daemon-agent", "otel-col-cluster")]
        checksums.append({document["metadata"]["name"]: document["spec"]["template"]["metadata"]["annotations"]["checksum/platform-telemetry"] for document in workloads})
    assert checksums[0].keys() == checksums[1].keys()
    assert all(checksums[0][name] != checksums[1][name] for name in checksums[0])


def test_disabling_an_individual_collector_still_renders():
    overrides = configured()
    overrides["otel-col-daemon"] = {"enabled": False}
    configs = collector_relays(render_chart("loco-worker", overrides))
    assert "otel-col-daemon-agent" not in configs
    assert "metrics/platform" in configs["otel-col-cluster"]["service"]["pipelines"]


def test_collectors_have_only_read_access():
    collectors = set()
    for document in render_chart("loco-worker", configured()):
        if document["kind"] not in ("ClusterRole", "Role"):
            continue
        if document["metadata"].get("labels", {}).get("app.kubernetes.io/name") not in ("otel-col-daemon", "otel-col-cluster", "otel-col-deploy"):
            continue
        collectors.add(document["metadata"]["labels"]["app.kubernetes.io/name"])
        for rule in document.get("rules", []):
            assert set(rule["verbs"]) <= {"get", "list", "watch"}
            assert "secrets" not in rule.get("resources", [])
    assert {"otel-col-daemon", "otel-col-cluster"} <= collectors


@pytest.mark.parametrize("scaling", [{"replicaCount": 2}, {"autoscaling": {"enabled": True}}])
def test_cluster_collection_cannot_duplicate_watchers(scaling):
    overrides = configured()
    overrides["otel-col-cluster"] = scaling
    result = render_chart("loco-worker", overrides, check=False)
    assert result.returncode != 0
    assert "one otel-col-cluster replica" in result.stderr
