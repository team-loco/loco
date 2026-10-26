import json
import shutil
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RELEASE_NAMESPACE = "platform-test"
COLLECTOR_CONFIGMAPS = ("otel-col-daemon-agent", "otel-col-deploy", "otel-col-cluster")
TEST_IMAGE = {"tag": "test"}
TEST_PROXY = {
    "image": TEST_IMAGE,
    "query": {
        "clickhouse": {
            "existingSecret": {"name": "proxy-reader", "key": "url"},
            "migratorExistingSecret": {"name": "proxy-migrator", "key": "url"},
        },
    },
}
API_VALUES = {"image": TEST_IMAGE, "existingSecret": "regional-api-env"}
TEST_REGION = "test-region-1"
TEST_EXPORTER_ENDPOINT = "https://ingest.test.local"
CONTROL_PLANE_BASE_VALUES = {"certManager": {"issuer": {"email": "ops@region.test"}}}
WORKER_BASE_VALUES = {
    "global": {"region": TEST_REGION, "exporter": {"endpoint": TEST_EXPORTER_ENDPOINT}},
    "certManager": {"issuer": {"email": "ops@worker.test"}},
    "agent": {"image": TEST_IMAGE},
    "controller": {"image": TEST_IMAGE},
    "builds": {"builderImage": TEST_IMAGE},
}
BASE_VALUES = {"loco-worker": WORKER_BASE_VALUES, "loco-control-plane": CONTROL_PLANE_BASE_VALUES}
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


def render_template(chart, template, overrides, includes=()):
    source = ROOT / "charts" / chart
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        (root / "templates").mkdir()
        metadata = json.loads(
            subprocess.check_output(
                ["yq", "-o=json", ".", str(source / "Chart.yaml")], text=True
            )
        )
        metadata.pop("dependencies", None)
        (root / "Chart.yaml").write_text(json.dumps(metadata))
        shutil.copyfile(source / "values.yaml", root / "values.yaml")
        target = root / "templates" / template
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source / "templates" / template, target)
        for included in includes:
            shutil.copyfile(source / "templates" / included, root / "templates" / included)
        for helper in (source / "templates").glob("*.tpl"):
            shutil.copyfile(helper, root / "templates" / helper.name)
        (root / "overrides.json").write_text(json.dumps(overrides))
        rendered = subprocess.check_output(
            [
                "helm",
                "template",
                chart,
                str(root),
                "-n",
                RELEASE_NAMESPACE,
                "-f",
                str(root / "overrides.json"),
            ],
            text=True,
            stderr=subprocess.STDOUT,
        )
    return parse_documents(rendered)


def render_template_object(chart, template, overrides):
    documents = render_template(chart, template, overrides)
    return documents[0] if documents else None


def render_chart(chart, overrides, check=True):
    with tempfile.TemporaryDirectory() as directory:
        base = Path(directory) / "base.json"
        base.write_text(json.dumps(BASE_VALUES.get(chart, {})))
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


def kinds(documents):
    return {document["kind"] for document in documents}


def by_kind(documents, kind):
    return {document["metadata"]["name"]: document for document in documents if document["kind"] == kind}


def container_env(deployment):
    return {
        item["name"]: item
        for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"]
    }


def agent_env(values):
    agent = {"image": TEST_IMAGE, **values.get("agent", {})}
    documents = render_template(
        "loco-worker",
        "agent-deployment.yaml",
        {**values, "agent": agent},
        includes=("env-config-secret.yml",),
    )
    (deployment,) = [document for document in documents if document["kind"] == "Deployment"]
    (container,) = deployment["spec"]["template"]["spec"]["containers"]
    return {variable["name"]: variable for variable in container["env"]}


def quantity(value):
    for suffix, scale in QUANTITY_SUFFIXES.items():
        if value.endswith(suffix):
            return float(value.removesuffix(suffix)) * scale
    return float(value)


def collector_documents(documents):
    return [
        document
        for document in documents
        if document["metadata"].get("labels", {}).get("app.kubernetes.io/part-of") == "opentelemetry-collector"
    ]


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
