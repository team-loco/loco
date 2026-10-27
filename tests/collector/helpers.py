import json
import subprocess
import tempfile
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
