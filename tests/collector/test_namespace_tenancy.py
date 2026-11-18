import copy
import json
import subprocess
import sys
import threading
import urllib.parse
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest
from helpers import (
    chart_values,
    docker,
    post_otlp,
    read_output,
    run_collector,
    wait_for,
)


@pytest.mark.parametrize("gateway", [False, True], ids=["dependency", "gateway"])
def test_ownership_uses_the_connected_pod_or_gateway_route_namespace(
    worker_relays, tmp_path, gateway
):
    """Exercise real pod association and namespace lookup, including an unmatched sender."""
    name = f"loco-namespace-tenancy-{uuid.uuid4().hex}"
    sender = f"{name}-sender"
    namespace = "loco-ws-environment-a"
    namespace_labels = {
        "loco.io/organization-id": "organization-a",
        "loco.io/workspace-id": "workspace-a",
        "loco.io/environment-id": "environment-a",
    }
    curl_image = chart_values("loco-control-plane")["openbao"]["curlImage"]
    docker("network", "create", name)
    server = None
    try:
        docker(
            "run",
            "--detach",
            "--name",
            sender,
            "--network",
            name,
            "--entrypoint",
            "sh",
            curl_image,
            "-c",
            "sleep 120",
        )
        sender_ip = json.loads(docker("inspect", sender))[0]["NetworkSettings"][
            "Networks"
        ][name]["IPAddress"]
        objects = {
            "/api/v1/pods": [
                {
                    "apiVersion": "v1",
                    "kind": "Pod",
                    "metadata": {
                        "name": "dependency",
                        "namespace": "gateway-system" if gateway else namespace,
                        "labels": (
                            {"gateway.envoyproxy.io/owning-gateway-name": "loco"}
                            if gateway
                            else {}
                        ),
                        "uid": "pod-a",
                        "resourceVersion": "1",
                    },
                    "spec": {
                        "nodeName": "node-a",
                        "containers": [{"name": "app", "image": "example/app:test"}],
                    },
                    "status": {"podIP": sender_ip},
                }
            ],
            "/api/v1/namespaces": [
                {
                    "apiVersion": "v1",
                    "kind": "Namespace",
                    "metadata": {
                        "name": namespace,
                        "uid": "namespace-a",
                        "labels": namespace_labels,
                        "resourceVersion": "1",
                    },
                }
            ],
        }

        class API(BaseHTTPRequestHandler):
            def do_GET(self):
                request = urllib.parse.urlparse(self.path)
                items = objects.get(request.path, [])
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                if urllib.parse.parse_qs(request.query).get("watch") == ["true"]:
                    for item in items:
                        self.wfile.write(
                            json.dumps({"type": "ADDED", "object": item}).encode()
                            + b"\n"
                        )
                    self.wfile.write(
                        json.dumps(
                            {
                                "type": "BOOKMARK",
                                "object": {
                                    "apiVersion": "v1",
                                    "kind": "PartialObjectMetadata",
                                    "metadata": {
                                        "resourceVersion": "1",
                                        "annotations": {
                                            "k8s.io/initial-events-end": "true"
                                        },
                                    },
                                },
                            }
                        ).encode()
                        + b"\n"
                    )
                else:
                    kind = (
                        "PodList"
                        if request.path.endswith("pods")
                        else "NamespaceList"
                        if request.path.endswith("namespaces")
                        else "ReplicaSetList"
                    )
                    self.wfile.write(
                        json.dumps(
                            {
                                "apiVersion": "v1",
                                "kind": kind,
                                "metadata": {"resourceVersion": "1"},
                                "items": items,
                            }
                        ).encode()
                    )

            def log_message(self, *_):
                pass

        server = ThreadingHTTPServer(("0.0.0.0", 0), API)
        api_thread = threading.Thread(target=server.serve_forever)
        api_thread.start()
        kubeconfig = {
            "apiVersion": "v1",
            "kind": "Config",
            "clusters": [
                {
                    "name": "test",
                    "cluster": {
                        "server": f"http://host.docker.internal:{server.server_port}"
                    },
                }
            ],
            "contexts": [{"name": "test", "context": {"cluster": "test"}}],
            "current-context": "test",
        }
        (tmp_path / "kubeconfig").write_text(json.dumps(kubeconfig))
        config = copy.deepcopy(worker_relays["otel-col-deploy"])
        for processor, settings in config["processors"].items():
            if processor.startswith("k8s_attributes/"):
                settings.update(auth_type="kubeConfig", wait_for_metadata=True)
        config["receivers"] = {
            "otlp": {"protocols": {"http": {"endpoint": "0.0.0.0:4318"}}}
        }
        config["exporters"] = {
            "file": {"path": "/data/output.json", "flush_interval": "100ms"}
        }
        pipeline = config["service"]["pipelines"]["traces"]
        pipeline.update(receivers=["otlp"], exporters=["file"])
        config["service"] = {"pipelines": {"traces": pipeline}}
        config.pop("extensions", None)
        host_gateway = (
            ["--add-host", "host.docker.internal:host-gateway"]
            if sys.platform == "linux"
            else []
        )
        with run_collector(
            tmp_path,
            config,
            "otel-col-deploy",
            "--network",
            name,
            *host_gateway,
            "--env",
            "KUBECONFIG=/data/kubeconfig",
        ) as collector:
            attributes = [
                {"key": key, "value": {"stringValue": value}}
                for key, value in {
                    "k8s.namespace.name": namespace,
                    "k8s.pod.name": "forged-pod",
                    "loco.organization.id": "forged-org",
                    "loco.workspace.id": "forged-workspace",
                    "loco.resource.id": "forged-resource",
                }.items()
            ]
            payload = {
                "resourceSpans": [
                    {
                        "resource": {"attributes": attributes},
                        "scopeSpans": [
                            {
                                "spans": [
                                    {
                                        "traceId": "0123456789abcdef0123456789abcdef",
                                        "spanId": "0123456789abcdef",
                                        "name": "dependency",
                                        "attributes": [
                                            {
                                                "key": "upstream_cluster",
                                                "value": {
                                                    "stringValue": (
                                                        f"httproute/{namespace}/"
                                                        "resource-resource-a-route/rule/0"
                                                    )
                                                },
                                            }
                                        ],
                                        "startTimeUnixNano": "1700000000000000000",
                                        "endTimeUnixNano": "1700000000000000001",
                                    }
                                ]
                            }
                        ],
                    }
                ]
            }
            wait_for(
                collector,
                lambda: (
                    subprocess.run(
                        [
                            "docker",
                            "exec",
                            "-i",
                            sender,
                            "curl",
                            "--silent",
                            "--show-error",
                            "--fail",
                            "-H",
                            "Content-Type: application/json",
                            "--data-binary",
                            "@-",
                            f"http://{collector.name}:4318/v1/traces",
                        ],
                        input=json.dumps(payload),
                        text=True,
                        capture_output=True,
                    ).returncode
                    == 0
                ),
            )
            post_otlp(collector, "traces", payload)
            received = read_output(
                collector, lambda document: document["resourceSpans"], 2
            )
        attrs = [
            {
                a["key"]: a["value"].get("stringValue")
                for a in r["resource"]["attributes"]
            }
            for r in received
        ]
        assert (
            attrs[0].items()
            >= {
                "loco.organization.id": "organization-a",
                "loco.workspace.id": "workspace-a",
                "loco.environment.id": "environment-a",
                "k8s.namespace.name": namespace,
                "k8s.pod.name": "dependency",
            }.items()
        )
        if gateway:
            assert attrs[0]["loco.resource.id"] == "resource-a"
        else:
            assert "loco.resource.id" not in attrs[0]
        assert not any(
            key in attrs[1]
            for key in (
                "loco.organization.id",
                "loco.workspace.id",
                "loco.environment.id",
                "loco.resource.id",
                "k8s.namespace.name",
            )
        )
    finally:
        try:
            subprocess.run(
                ["docker", "rm", "--force", sender],
                check=False,
                capture_output=True,
            )
            subprocess.run(
                ["docker", "network", "rm", name], check=False, capture_output=True
            )
        finally:
            if server is not None:
                server.shutdown()
                server.server_close()
                api_thread.join()
