import re

from helpers import ROOT, chart_values, collector_relays, render_chart, run_collector

WORKSPACE = "0199e0a4-0000-7000-8000-00000000a001"
RESOURCE = "0199e0a4-0000-7000-8000-00000000b002"
TENANCY = ("loco.workspace.id", "loco.resource.id", "loco.telemetry.scope")


def application_name_prefix():
    source = (ROOT / "k8sapi/v1alpha1/application.go").read_text()
    return re.search(r'applicationNamePrefix\s*=\s*"([^"]+)"', source)[1]


def any_value(value):
    if isinstance(value, dict):
        return {"kvlistValue": {"values": [{"key": key, "value": any_value(item)} for key, item in value.items()]}}
    return {"stringValue": value}


def watched_event(namespace, kind, name):
    regarding = {"kind": kind, "name": name, "namespace": namespace}
    return {"type": "ADDED", "object": {"reason": "BackOff", "regarding": regarding}}


def test_events_are_attributed_to_the_resource_their_object_belongs_to(tmp_path):
    namespace = chart_values("loco-worker")["global"]["workspaceNamespacePrefix"] + WORKSPACE
    application = application_name_prefix() + RESOURCE
    owned = {
        "deployment": (namespace, "Deployment", application),
        "replica set": (namespace, "ReplicaSet", f"{application}-5d9c7b8f4"),
        "pod": (namespace, "Pod", f"{application}-5d9c7b8f4-x2x8q"),
        "env secret": (namespace, "Secret", f"{application}-env"),
    }
    unowned = {
        "object of no resource": (namespace, "Service", "web"),
        "name that only starts with the resource ID": (namespace, "Pod", f"{application}x"),
        "namespace of no workspace": ("kube-system", "Pod", f"{application}-5d9c7b8f4-x2x8q"),
        "malformed resource ID": (namespace, "Pod", application_name_prefix() + "web-5d9c7b8f4-x2x8q"),
    }
    cases = list(owned.items()) + list(unowned.items())
    relay = collector_relays(render_chart("loco-worker", {}))["otel-col-cluster"]
    config = {
        "receivers": {"otlp": {"protocols": {"http": {"endpoint": "0.0.0.0:4318"}}}},
        "processors": {"transform/kubernetes_events": relay["processors"]["transform/kubernetes_events"]},
        "exporters": {"file": {"path": "/data/output.json", "flush_interval": "100ms"}},
        "service": {"pipelines": {"logs": {
            "receivers": ["otlp"], "processors": ["transform/kubernetes_events"], "exporters": ["file"],
        }}},
    }
    # one resource per event, as the k8sobjects receiver sends watched objects
    payload = {"resourceLogs": [
        {"scopeLogs": [{"logRecords": [{"body": any_value(watched_event(*event))}]}]} for _, event in cases
    ]}

    received = run_collector(tmp_path, config, len(cases), payload)

    for (name, _), (resource, _) in zip(cases, received, strict=True):
        attributes = {item["key"]: item["value"]["stringValue"] for item in resource.get("attributes", [])}
        tenancy = {key: attributes[key] for key in TENANCY if key in attributes}
        if name in owned:
            assert tenancy == dict(zip(TENANCY, (WORKSPACE, RESOURCE, "kubernetes-event"))), name
        else:
            assert tenancy == {}, name


def test_cluster_events_are_attributed_before_export(worker_relays):
    processors = worker_relays["otel-col-cluster"]["service"]["pipelines"]["logs"]["processors"]
    assert "transform/kubernetes_events" in processors[:-2]
