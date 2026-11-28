import json
import subprocess

import pytest
from helpers import chart_values, post_otlp, read_output, render_chart, run_collector
from test_platform_telemetry import platform_pipeline_config


@pytest.mark.parametrize("enabled,logs_enabled", [(False, True), (True, False)])
def test_control_plane_log_gates_remove_the_daemonset(enabled, logs_enabled):
    documents = render_chart(
        "loco-control-plane",
        {
            "platformTelemetry": {
                "enabled": enabled,
                "region": "test-region",
                "logs": {"enabled": logs_enabled},
            }
        },
    )
    assert not any(document["kind"] == "DaemonSet" for document in documents)


@pytest.mark.parametrize("external", [False, True])
def test_control_plane_log_config_is_valid_and_uses_the_selected_exporter(
    tmp_path, external
):
    settings = {"enabled": True, "region": "test-region"}
    if external:
        settings["exporter"] = {
            "endpoint": "https://otel.example.com",
            "headers": {"Authorization": "Bearer synthetic"},
        }
    documents = render_chart("loco-control-plane", {"platformTelemetry": settings})
    relay = next(
        document["data"]["relay"]
        for document in documents
        if document["kind"] == "ConfigMap"
        and document["metadata"]["name"] == "loco-control-plane-control-plane-logs"
    )
    config = json.loads(
        subprocess.check_output(["yq", "-o=json", ".", "-"], input=relay, text=True)
    )
    assert list(config["service"]["pipelines"]) == ["logs"]
    assert list(config["receivers"]) == ["file_log"]
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
        assert len(secrets) == 2
        assert secrets[0]["data"]["token"] == secrets[1]["data"]["token"]
        daemon = next(
            document for document in documents if document["kind"] == "DaemonSet"
        )
        labels = daemon["spec"]["template"]["metadata"]["labels"]
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
            == daemon["metadata"]["namespace"]
            and peer["podSelector"]["matchLabels"].items() <= labels.items()
            for rule in policy["spec"]["ingress"]
            for peer in rule.get("from", [])
        )
    path = tmp_path / "collector.yaml"
    path.write_text(relay)
    image = chart_values("loco-control-plane/telemetry")["logs"]["image"]
    subprocess.run(
        [
            "docker",
            "run",
            "--rm",
            "--env",
            "OTEL_K8S_NODE_NAME=synthetic-node",
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
    relay = next(
        document["data"]["relay"]
        for document in documents
        if document["kind"] == "ConfigMap"
        and document["metadata"]["name"] == "loco-control-plane-control-plane-logs"
    )
    rendered = json.loads(
        subprocess.check_output(["yq", "-o=json", ".", "-"], input=relay, text=True)
    )
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
        tmp_path, config, "logs", chart="loco-control-plane/telemetry"
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
