import copy
import json
import select
import socket
import subprocess
import sys
import threading
import urllib.parse
import uuid
from collections import Counter
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
from test_route_tenancy import SIGNALS, attributes

OTLP_ROOTS = {
    "traces": "resourceSpans",
    "logs": "resourceLogs",
    "metrics": "resourceMetrics",
}


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
    watches = Counter()
    watch_lock = threading.Lock()
    stop_watches = threading.Event()
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
                metadata_only = "as=PartialObjectMetadata" in self.headers.get(
                    "Accept", ""
                )
                if metadata_only:
                    items = [
                        {
                            "apiVersion": "meta.k8s.io/v1",
                            "kind": "PartialObjectMetadata",
                            "metadata": item["metadata"],
                        }
                        for item in items
                    ]
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                if urllib.parse.parse_qs(request.query).get("watch") == ["true"]:
                    with watch_lock:
                        watches[request.path] += 1
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
                                    "apiVersion": "meta.k8s.io/v1"
                                    if metadata_only
                                    else "v1",
                                    "kind": "PartialObjectMetadata"
                                    if metadata_only
                                    else "Pod",
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
                    self.wfile.flush()
                    try:
                        while not stop_watches.wait(0.1):
                            readable, _, _ = select.select([self.connection], [], [], 0)
                            if readable and not self.connection.recv(
                                1, socket.MSG_PEEK
                            ):
                                break
                    finally:
                        with watch_lock:
                            watches[request.path] -= 1
                else:
                    kind = {
                        "/api/v1/pods": "PodList",
                        "/api/v1/namespaces": "NamespaceList",
                    }.get(request.path, "ReplicaSetList")
                    if metadata_only:
                        kind = "PartialObjectMetadataList"
                    self.wfile.write(
                        json.dumps(
                            {
                                "apiVersion": "meta.k8s.io/v1"
                                if metadata_only
                                else "v1",
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
        pipelines = {
            signal: config["service"]["pipelines"][signal] for signal in SIGNALS
        }
        for pipeline in pipelines.values():
            pipeline.update(receivers=["otlp"], exporters=["file"])
        config["service"] = {"pipelines": pipelines}
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
            "--memory",
            "512m",
            "--cpus",
            "1",
            *host_gateway,
            "--env",
            "KUBECONFIG=/data/kubeconfig",
        ) as collector:
            received = {}
            for signal, (cluster_key, payload, records) in SIGNALS.items():
                resource = {
                    "attributes": attributes(
                        {
                            "k8s.namespace.name": namespace,
                            "k8s.pod.name": "forged-pod",
                            "loco.organization.id": "forged-org",
                            "loco.workspace.id": "forged-workspace",
                            "loco.resource.id": "forged-resource",
                        }
                    )
                }
                record = attributes(
                    {
                        cluster_key: (
                            f"httproute/{namespace}/resource-resource-a-route/rule/0"
                        )
                    }
                )
                data = payload(resource, record)
                wait_for(
                    collector,
                    lambda signal=signal, data=data: (
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
                                f"http://{collector.name}:4318/v1/{signal}",
                            ],
                            input=json.dumps(data),
                            text=True,
                            capture_output=True,
                        ).returncode
                        == 0
                    ),
                )
                post_otlp(collector, signal, data)
                received[signal] = read_output(
                    collector,
                    lambda document, signal=signal, records=records: (
                        records(document) if OTLP_ROOTS[signal] in document else []
                    ),
                    2,
                )
            with watch_lock:
                assert watches["/api/v1/pods"] == 2, dict(watches)
                assert watches["/api/v1/namespaces"] == 2, dict(watches)
        for signal, items in received.items():
            attrs = [
                {
                    a["key"]: a["value"].get("stringValue")
                    for a in resource["attributes"]
                }
                for resource, _ in items
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
            ), (signal, attrs)
            if gateway:
                assert attrs[0]["loco.resource.id"] == "resource-a", signal
            else:
                assert "loco.resource.id" not in attrs[0], signal
            assert not any(
                key in attrs[1]
                for key in (
                    "loco.organization.id",
                    "loco.workspace.id",
                    "loco.environment.id",
                    "loco.resource.id",
                    "k8s.namespace.name",
                )
            ), (signal, attrs)
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
                stop_watches.set()
                server.shutdown()
                server.server_close()
                api_thread.join()
