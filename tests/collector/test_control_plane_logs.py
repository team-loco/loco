import json
import subprocess
from pathlib import Path

import pytest
from helpers import chart_values, post_otlp, read_output, render_chart, run_collector
from test_platform_telemetry import platform_pipeline_config


def control_plane_config(documents, name):
    relay = next(
        document["data"]["relay"]
        for document in documents
        if document["kind"] == "ConfigMap"
        and document["metadata"]["name"] == "loco-control-plane-" + name
    )
    return json.loads(
        subprocess.check_output(["yq", "-o=json", ".", "-"], input=relay, text=True)
    )


def test_control_plane_telemetry_is_opt_in():
    documents = render_chart("loco-control-plane", {})
    assert not any(
        document["kind"] in ("DaemonSet", "StatefulSet")
        and document["metadata"]["name"]
        in ("node-collector-agent", "cluster-collector")
        for document in documents
    )


def test_control_plane_log_opt_out_keeps_node_metrics_cluster_state_and_events():
    documents = render_chart(
        "loco-control-plane",
        {
            "platformTelemetry": {
                "enabled": True,
                "region": "test-region",
                "logs": {"enabled": False},
            }
        },
    )
    node = control_plane_config(documents, "node-collector")
    cluster = control_plane_config(documents, "cluster-collector")
    assert set(node["service"]["pipelines"]) == {"metrics"}
    assert set(node["receivers"]) == {"kubeletstats"}
    assert set(cluster["service"]["pipelines"]) == {"metrics", "logs"}
    assert set(cluster["receivers"]) == {"k8s_cluster", "k8sobjects"}
    assert any(document["kind"] == "DaemonSet" for document in documents)


def test_control_plane_collector_delegation_retains_sdk_exports():
    documents = render_chart(
        "loco-control-plane",
        {
            "platformTelemetry": {"enabled": True, "region": "test-region"},
            "nodeCollector": {"enabled": False},
            "clusterCollector": {"enabled": False},
        },
    )
    assert not any(
        document["kind"] in ("DaemonSet", "StatefulSet")
        and document["metadata"]["name"]
        in ("node-collector-agent", "cluster-collector")
        for document in documents
    )
    api = next(
        document
        for document in documents
        if document["kind"] == "Deployment"
        and document["metadata"]["name"] == "loco-control-plane-api"
    )
    assert any(
        variable["name"] == "OTEL_EXPORTER_OTLP_ENDPOINT"
        for variable in api["spec"]["template"]["spec"]["containers"][0]["env"]
    )
    assert (
        "loco.io/platform-log-owner"
        not in api["spec"]["template"]["metadata"]["labels"]
    )


@pytest.mark.parametrize(
    "name,role,receivers,pipelines",
    [
        (
            "node-collector",
            "nodeCollector",
            {"file_log", "kubeletstats"},
            {"logs", "metrics"},
        ),
        (
            "cluster-collector",
            "clusterCollector",
            {"k8s_cluster", "k8sobjects"},
            {"logs", "metrics"},
        ),
    ],
)
@pytest.mark.parametrize("external", [False, True])
def test_control_plane_config_is_valid_and_uses_the_selected_exporter(
    tmp_path, name, role, receivers, pipelines, external
):
    settings = {"enabled": True, "region": "test-region"}
    if external:
        settings["exporter"] = {
            "endpoint": "https://otel.example.com",
            "headers": {"Authorization": "Bearer synthetic"},
        }
    documents = render_chart("loco-control-plane", {"platformTelemetry": settings})
    config = control_plane_config(documents, name)
    assert set(config["service"]["pipelines"]) == pipelines
    assert set(config["receivers"]) == receivers
    if name == "node-collector":
        assert config["receivers"]["file_log"]["storage"] == "file_storage"
    exporter = config["exporters"]["otlp_http"]
    assert exporter["sending_queue"]["storage"] == "file_storage/exporter"
    assert exporter["retry_on_failure"]["max_elapsed_time"] == "0s"
    if external:
        assert exporter["endpoint"] == settings["exporter"]["endpoint"]
        assert exporter["headers"] == settings["exporter"]["headers"]
        assert "auth" not in exporter
    else:
        assert (
            exporter["endpoint"]
            == "http://loco-control-plane-system-ingest.platform-test.svc.cluster.local"
        )
        assert exporter["auth"] == {"authenticator": "bearertokenauth/exporter"}
        secrets = [
            document
            for document in documents
            if document["kind"] == "Secret"
            and document["metadata"]["name"].endswith("-system-ingest-token")
        ]
        assert len(secrets) == 3
        assert len({secret["data"]["token"] for secret in secrets}) == 1
        workload = next(
            document
            for document in documents
            if document["kind"] in ("DaemonSet", "StatefulSet")
            and document["metadata"]["name"]
            == name + ("-agent" if name == "node-collector" else "")
        )
        labels = workload["spec"]["template"]["metadata"]["labels"]
        policy = next(
            document
            for document in documents
            if document["kind"] == "NetworkPolicy"
            and document["metadata"]["name"].endswith("-obs-proxy-ingest")
        )
        assert any(
            peer.get("namespaceSelector", {})
            .get("matchLabels", {})
            .get("kubernetes.io/metadata.name")
            == workload["metadata"]["namespace"]
            and peer["podSelector"]["matchLabels"].items() <= labels.items()
            for rule in policy["spec"]["ingress"]
            for peer in rule.get("from", [])
        )
    path = tmp_path / "collector.yaml"
    path.write_text(json.dumps(config))
    # Synthetic credentials let validate construct clients; installed tests prove authorization.
    service_account = tmp_path / "serviceaccount"
    service_account.mkdir()
    (service_account / "token").write_text("synthetic-token")
    (service_account / "namespace").write_text("synthetic-namespace")
    (service_account / "ca.crt").write_bytes(
        (Path(__file__).parent / "fixtures" / "ca.crt").read_bytes()
    )
    image = chart_values("loco-control-plane")[role]["image"]
    subprocess.run(
        [
            "docker",
            "run",
            "--rm",
            "--env",
            "OTEL_K8S_NODE_NAME=synthetic-node",
            "--env",
            "OTEL_K8S_NODE_IP=127.0.0.1",
            "--env",
            "MY_POD_IP=127.0.0.1",
            "--env",
            "KUBERNETES_SERVICE_HOST=127.0.0.1",
            "--env",
            "KUBERNETES_SERVICE_PORT=443",
            "--volume",
            f"{service_account}:/var/run/secrets/kubernetes.io/serviceaccount:ro",
            "--tmpfs",
            "/var/lib/otelcol",
            "--tmpfs",
            "/var/lib/otelcol-exporter",
            "--volume",
            f"{path}:/conf/collector.yaml:ro",
            f"{image['repository']}:{image['tag']}",
            "validate",
            "--config=/conf/collector.yaml",
        ],
        check=True,
        capture_output=True,
        text=True,
    )


def test_control_plane_logs_preserve_typed_fields_and_drop_export_feedback(tmp_path):
    # The real chart's processors run with synthetic Kubernetes metadata over OTLP.
    documents = render_chart(
        "loco-control-plane",
        {"platformTelemetry": {"enabled": True, "region": "test-region"}},
    )
    rendered = control_plane_config(documents, "node-collector")
    config = platform_pipeline_config(rendered, "logs")
    processors = config["service"]["pipelines"]["logs"]["processors"]
    processors[:] = [
        name
        for name in processors
        if not name.startswith(("memory_limiter", "k8s_attributes"))
    ]
    config["processors"] = {name: config["processors"][name] for name in processors}
    record = {
        "msg": "synthetic request failed",
        "level": "ERROR",
        "request": {"id": "synthetic", "attempt": 2},
        "literal": '{"must":"stay a string"}',
    }
    resource = {
        "attributes": [
            {
                "key": "loco.platform.log.owner",
                "value": {"stringValue": "control-plane"},
            },
            {"key": "k8s.pod.uid", "value": {"stringValue": "synthetic-pod"}},
        ]
    }
    payload = {
        "resourceLogs": [
            {
                "resource": resource,
                "scopeLogs": [
                    {
                        "logRecords": [
                            {"body": {"stringValue": json.dumps(body)}}
                            for body in (
                                record,
                                {
                                    "msg": "export failure",
                                    "loco.telemetry.internal": True,
                                },
                            )
                        ]
                    }
                ],
            }
        ]
    }
    with run_collector(
        tmp_path, config, "node-collector-agent", chart="loco-control-plane"
    ) as collector:
        post_otlp(collector, "logs", payload)
        received = read_output(
            collector,
            lambda document: [
                record
                for resource in document["resourceLogs"]
                for scope in resource["scopeLogs"]
                for record in scope["logRecords"]
            ],
            1,
        )
    assert len(received) == 1
    observed = received[0]
    assert observed["body"] == {"stringValue": record["msg"]}
    assert observed["severityNumber"] == 17
    attributes = {item["key"]: item["value"] for item in observed["attributes"]}
    assert attributes["literal"] == {"stringValue": record["literal"]}
    assert {
        item["key"]: item["value"]
        for item in attributes["request"]["kvlistValue"]["values"]
    } == {"id": {"stringValue": "synthetic"}, "attempt": {"doubleValue": 2}}
