import json
import os
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from contextlib import contextmanager
from pathlib import Path
from typing import NamedTuple

ROOT = Path(__file__).resolve().parents[2]
RELEASE_NAMESPACE = "platform-test"
COLLECTOR_CONFIGMAPS = ("otel-col-daemon-agent", "otel-col-deploy", "otel-col-cluster")
CI_VALUES = "ci/default-values.yaml"
QUANTITY_SUFFIXES = {"m": 0.001, "Mi": 2**20, "Gi": 2**30}
OTLP_HTTP_PORT = "4318"
TIMEOUT_SECONDS = 30
POLL_SECONDS = 0.1


def chart_values(chart, values="values.yaml"):
    return json.loads(
        subprocess.check_output(
            ["yq", "-o=json", ".", str(ROOT / "charts" / chart / values)],
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


def render_chart(chart, overrides):
    with tempfile.TemporaryDirectory() as directory:
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
            str(ROOT / "charts" / chart / CI_VALUES),
            "-f",
            str(values),
        ]
        result = subprocess.run(command, capture_output=True, text=True)
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


class Collector(NamedTuple):
    name: str
    port: str
    directory: Path


def docker(*args):
    return subprocess.check_output(
        ["docker", *args], text=True, stderr=subprocess.STDOUT
    ).strip()


@contextmanager
def run_collector(directory, config, collector, *options):
    """Run loco-worker's image and arguments for collector on config, mounting directory at /data."""
    values = chart_values("loco-worker")[collector]
    image = values["image"]
    (directory / "config.json").write_text(json.dumps(config))
    name = f"loco-{collector}-{uuid.uuid4().hex}"
    docker(
        "run",
        "--detach",
        "--name",
        name,
        "--user",
        str(os.getuid()),
        "--publish",
        f"127.0.0.1::{OTLP_HTTP_PORT}",
        "--volume",
        f"{directory}:/data",
        *options,
        f"{image['repository']}:{image['tag']}",
        "--config=/data/config.json",
        *values.get("command", {}).get("extraArgs", []),
    )
    try:
        port = docker("port", name, f"{OTLP_HTTP_PORT}/tcp").rsplit(":", 1)[1]
        yield Collector(name, port, directory)
    finally:
        subprocess.run(["docker", "rm", "--force", name], capture_output=True)


def wait_for(collector, check):
    """Return check()'s first truthy result, failing with the collector's logs at the deadline."""
    deadline = time.monotonic() + TIMEOUT_SECONDS
    while not (result := check()):
        assert time.monotonic() < deadline, docker("logs", collector.name)
        time.sleep(POLL_SECONDS)
    return result


def read_output(collector, items, count):
    """Wait until the file exporter's /data/output.json holds count items."""
    output = collector.directory / "output.json"

    def received():
        if not output.exists():
            return None
        found = [
            item
            for line in output.read_text().split("\n")[:-1]
            for item in items(json.loads(line))
        ]
        return found if len(found) >= count else None

    return wait_for(collector, received)


def post_otlp(collector, signal, payload):
    """POST an OTLP/JSON export of signal to the collector until it accepts it."""
    request = urllib.request.Request(
        f"http://127.0.0.1:{collector.port}/v1/{signal}",
        json.dumps(payload).encode(),
        {"Content-Type": "application/json"},
    )

    def accepted():
        try:
            with urllib.request.urlopen(request, timeout=1) as response:
                return response.status == 200
        except urllib.error.URLError, ConnectionError:
            return False

    wait_for(collector, accepted)
