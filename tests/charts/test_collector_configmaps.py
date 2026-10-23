import pytest

from test_platform_contracts import collector_relays, render_chart


@pytest.mark.parametrize("name,kind,namespace,key", [
    ("otel-col-daemon", "DaemonSet", "observability-node", "config"),
    ("otel-col-cluster", "Deployment", "platform-test", "alternateConfig"),
])
def test_collector_config_changes_roll_pods_and_match_mounted_config(name, kind, namespace, key):
    checksums = []
    for timeout in ("1s", "2s"):
        documents = render_chart("loco-obs", {name: {key: {"processors": {"batch": {"timeout": timeout}}}}})
        workload = next(document for document in documents if document["kind"] == kind and document["metadata"]["labels"].get("app.kubernetes.io/name") == name)
        pod = workload["spec"]["template"]
        checksums.append(pod["metadata"]["annotations"]["checksum/platform-telemetry"])
        mount = next(mount for mount in pod["spec"]["containers"][0]["volumeMounts"] if mount["mountPath"] == "/conf")
        mounted = next(volume["configMap"]["name"] for volume in pod["spec"]["volumes"] if volume["name"] == mount["name"])
        configmap = next(document for document in documents if document["kind"] == "ConfigMap" and document["metadata"]["name"] == mounted)
        assert configmap["metadata"]["namespace"] == namespace
        assert collector_relays(documents)[mounted]["processors"]["batch"]["timeout"] == timeout
    assert checksums[0] != checksums[1]
