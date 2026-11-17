import copy
import json
import os
import subprocess
import sys
import threading
import time
import urllib.parse
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from helpers import chart_values


def docker(*args):
    return subprocess.check_output(
        ["docker", *args], text=True, stderr=subprocess.STDOUT
    ).strip()


def test_dependency_ownership_uses_the_connected_pods_namespace(
    worker_relays, tmp_path
):
    """Exercise real pod association and namespace lookup, including an unmatched sender."""
    name = f"loco-namespace-tenancy-{uuid.uuid4().hex}"
    sender = f"{name}-sender"
    namespace = "workspace-a"
    namespace_labels = {
        "loco.io/organization-id": "organization-a",
        "loco.io/workspace-id": "workspace-a",
        "loco.io/environment-id": "environment-a",
    }
    collector_image = chart_values("loco-worker")["otel-col-deploy"]["image"]
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
                        "namespace": namespace,
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
        (tmp_path / "config.json").write_text(json.dumps(config))
        host_gateway = (
            ["--add-host", "host.docker.internal:host-gateway"]
            if sys.platform == "linux"
            else []
        )
        docker(
            "run",
            "--detach",
            "--name",
            name,
            "--network",
            name,
            *host_gateway,
            "--user",
            str(os.getuid()),
            "--publish",
            "127.0.0.1::4318",
            "--env",
            "KUBECONFIG=/data/kubeconfig",
            "--volume",
            f"{tmp_path}:/data",
            f"{collector_image['repository']}:{collector_image['tag']}",
            "--config=/data/config.json",
        )
        port = docker("port", name, "4318/tcp").rsplit(":", 1)[1]
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
                                    "startTimeUnixNano": "1700000000000000000",
                                    "endTimeUnixNano": "1700000000000000001",
                                }
                            ]
                        }
                    ],
                }
            ]
        }
        deadline = time.monotonic() + 30
        while True:
            result = subprocess.run(
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
                    f"http://{name}:4318/v1/traces",
                ],
                input=json.dumps(payload),
                text=True,
                capture_output=True,
            )
            if result.returncode == 0:
                break
            assert time.monotonic() < deadline, docker("logs", name)
            time.sleep(0.1)
        request = urllib.request.Request(
            f"http://127.0.0.1:{port}/v1/traces",
            json.dumps(payload).encode(),
            {"Content-Type": "application/json"},
        )
        with urllib.request.urlopen(request, timeout=5) as response:
            assert response.status == 200
        output = tmp_path / "output.json"
        received = []
        while len(received) < 2:
            if output.exists():
                received = [
                    r
                    for line in output.read_text().split("\n")[:-1]
                    for r in json.loads(line)["resourceSpans"]
                ]
            assert time.monotonic() < deadline, docker("logs", name)
            time.sleep(0.1)
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
                ["docker", "rm", "--force", name, sender],
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
