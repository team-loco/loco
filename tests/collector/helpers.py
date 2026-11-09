import json
import os
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RELEASE_NAMESPACE = "platform-test"
COLLECTOR_CONFIGMAPS = ("otel-col-daemon-agent", "otel-col-deploy", "otel-col-cluster")
TEST_IMAGE = {"tag": "test"}
TEST_REGION = "test-region-1"
TEST_EXPORTER_ENDPOINT = "https://ingest.test.local"
WORKER_BASE_VALUES = {
    "global": {"region": TEST_REGION, "exporter": {"endpoint": TEST_EXPORTER_ENDPOINT}},
    "certManager": {"issuer": {"email": "ops@worker.test"}},
    "agent": {"image": TEST_IMAGE},
    "controller": {"image": TEST_IMAGE},
    "builds": {"builderImage": TEST_IMAGE},
}
QUANTITY_SUFFIXES = {"m": 0.001, "Mi": 2**20, "Gi": 2**30}
COLLECTOR_TIMEOUT_SECONDS = 30
COLLECTOR_POLL_SECONDS = 0.1


def chart_values(chart):
    return json.loads(
        subprocess.check_output(
            ["yq", "-o=json", ".", str(ROOT / "charts" / chart / "values.yaml")],
            text=True,
        )
    )


def parse_documents(rendered):
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
        base.write_text(json.dumps(WORKER_BASE_VALUES))
        values = Path(directory) / "overrides.json"
        values.write_text(json.dumps(overrides))
        command = [
            "helm",
            "template",
            chart,
            str(ROOT / "charts" / chart),
            "-n",
            RELEASE_NAMESPACE,
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
    return parse_documents(result.stdout)


def quantity(value):
    for suffix, scale in QUANTITY_SUFFIXES.items():
        if value.endswith(suffix):
            return float(value.removesuffix(suffix)) * scale
    return float(value)


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


def run_collector(directory, config, count, payload=None):
    """Runs the chart-pinned collector on config, posts payload as OTLP/HTTP logs, and returns the
    first count log records its file exporter writes to /data/output.json."""
    image = chart_values("loco-worker")["otel-col-daemon"]["image"]
    name = f"loco-collector-test-{uuid.uuid4().hex}"
    (directory / "config.json").write_text(json.dumps(config))
    subprocess.run([
        "docker", "run", "--detach", "--name", name,
        "--user", str(os.getuid()), "--publish", "127.0.0.1::4318",
        "--volume", f"{directory}:/data", f'{image["repository"]}:{image["tag"]}',
        "--config=/data/config.json",
    ], check=True, capture_output=True, text=True)
    try:
        deadline = time.monotonic() + COLLECTOR_TIMEOUT_SECONDS

        def wait():
            logs = subprocess.check_output(["docker", "logs", name], stderr=subprocess.STDOUT, text=True)
            assert time.monotonic() < deadline, logs
            time.sleep(COLLECTOR_POLL_SECONDS)

        port = subprocess.check_output(["docker", "port", name, "4318/tcp"], text=True).strip().rsplit(":", 1)[1]
        request = urllib.request.Request(
            f"http://127.0.0.1:{port}/v1/logs", json.dumps(payload).encode(), {"Content-Type": "application/json"}
        )
        while payload is not None:
            try:
                with urllib.request.urlopen(request, timeout=1) as response:
                    assert response.status == 200
                break
            except (urllib.error.URLError, ConnectionError):
                wait()
        output = directory / "output.json"
        received = []
        while len(received) < count:
            if output.exists():
                try:
                    documents = [json.loads(line) for line in output.read_text().splitlines()]
                    received = [
                        (resource.get("resource", {}), record)
                        for document in documents
                        for resource in document["resourceLogs"]
                        for scope in resource["scopeLogs"]
                        for record in scope["logRecords"]
                    ]
                except json.JSONDecodeError:
                    pass
            if len(received) < count:
                wait()
        return received
    finally:
        subprocess.run(["docker", "rm", "--force", name], check=True, capture_output=True)
