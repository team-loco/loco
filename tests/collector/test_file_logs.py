import json

from helpers import read_output, run_collector

POD_LOGS = "/var/log/pods"
TEST_LOGS = "/data/pods"
TIMESTAMP = "2026-10-19T12:00:00.000000000Z"


def file_log_pipeline(relays):
    """The node collector's file-log receiver and processors, reading pod logs from /data/pods."""
    config = relays["otel-col-daemon-agent"]
    receiver = json.loads(
        json.dumps(config["receivers"]["file_log"]).replace(POD_LOGS, TEST_LOGS)
    )
    receiver.pop("storage")
    receiver["start_at"] = "beginning"
    processors = [
        name
        for name in config["service"]["pipelines"]["logs"]["processors"]
        if not name.startswith(("memory_limiter", "k8s_attributes"))
    ]
    return {
        "receivers": {"file_log": receiver},
        "processors": {name: config["processors"][name] for name in processors},
        "exporters": {"file": {"path": "/data/output.json", "flush_interval": "100ms"}},
        "service": {
            "pipelines": {
                "logs": {
                    "receivers": ["file_log"],
                    "processors": processors,
                    "exporters": ["file"],
                }
            }
        },
    }


def write_pod_log(directory, namespace, lines, pod="app", container="app"):
    path = (
        directory
        / "pods"
        / f"{namespace}_{pod}_00000000-0000-4000-8000-000000000001"
        / container
        / "0.log"
    )
    path.parent.mkdir(parents=True)
    path.write_text("".join(f"{TIMESTAMP} stdout F {line}\n" for line in lines))


def collected_bodies(collector, count):
    records = read_output(
        collector,
        lambda document: [
            record
            for resource in document["resourceLogs"]
            for scope in resource["scopeLogs"]
            for record in scope["logRecords"]
        ],
        count,
    )
    return sorted(record["body"]["stringValue"] for record in records)


def test_platform_namespace_logs_stay_on_the_node(worker_relays, tmp_path):
    for namespace in (
        "kube-system",
        "flux-system",
        "cert-manager",
        "loco-system",
        "loco-obs",
        "loco-ws-a",
    ):
        write_pod_log(tmp_path, namespace, [f"from {namespace}"])
    write_pod_log(
        tmp_path,
        "loco-obs-node",
        ["from the collector"],
        "otel-col-daemon-x",
        "otel-col-daemon",
    )
    config = file_log_pipeline(worker_relays)
    with run_collector(tmp_path, config, "otel-col-daemon") as collector:
        assert collected_bodies(collector, 3) == [
            "from loco-obs",
            "from loco-system",
            "from loco-ws-a",
        ]
