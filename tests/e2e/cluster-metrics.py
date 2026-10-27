import argparse
import hashlib
import json
import subprocess
import time
import uuid
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RUN = hashlib.sha256(str(ROOT).encode()).hexdigest()[:8]
MARKER = uuid.uuid4().hex[:8]
CLUSTER = f"loco-metrics-{RUN}"
NODE = f"{CLUSTER}-control-plane"
NAMESPACE = "platform-metrics"
NODE_NAMESPACE = "platform-metrics-node"
TENANT_NAMESPACE = "tenant-metrics-fixture"
TIMEOUT = 180
POLL = 2
COMMAND_TIMEOUT = 300
KUBE_REQUEST_TIMEOUT = "20s"
OUTAGE_SECONDS = 8
INTERVAL = "3s"
SINK_PATH = "/var/local/loco-platform-metrics"
TOKEN = "local-metrics-fixture"
RUN_DIR = ROOT / "tests/e2e/runs" / CLUSTER
CHART = ROOT / "charts/loco-worker"
COLLECTOR_PART_OF = "opentelemetry-collector"
KUBECONFIG = RUN_DIR / "kubeconfig"


def run(*args, body=None, check=True):
    result = subprocess.run(args, input=body, text=True, capture_output=True, timeout=COMMAND_TIMEOUT)
    if check and result.returncode:
        raise RuntimeError(f"{' '.join(args)}\n{result.stdout}\n{result.stderr}")
    return result.stdout.strip()


def kube(*args, body=None, check=True):
    return run("kubectl", "--kubeconfig", str(KUBECONFIG), "--request-timeout", KUBE_REQUEST_TIMEOUT, *args, body=body, check=check)


def apply(*documents):
    kube("apply", "-f", "-", body=json.dumps({"apiVersion": "v1", "kind": "List", "items": list(documents)}))


def wait(label, check):
    deadline = time.monotonic() + TIMEOUT
    last_error = None
    while time.monotonic() < deadline:
        try:
            if check():
                print(f"PASS {label}", flush=True)
                return
        except (RuntimeError, json.JSONDecodeError, FileNotFoundError) as error:
            last_error = error
        time.sleep(POLL)
    raise RuntimeError(f"Timed out: {label}: {last_error}")


def rollout(kind, name, namespace_name):
    def ready():
        document = json.loads(kube("get", "--raw", f"/apis/apps/v1/namespaces/{namespace_name}/{kind}/{name}"))
        status = document.get("status", {})
        if status.get("observedGeneration", 0) < document["metadata"]["generation"]:
            return False
        if kind == "daemonsets":
            desired = status.get("desiredNumberScheduled", 0)
            return desired > 0 and status.get("updatedNumberScheduled") == desired and status.get("numberReady") == desired
        desired = document["spec"].get("replicas", 1)
        return status.get("updatedReplicas") == desired and status.get("availableReplicas") == desired and status.get("replicas") == desired
    wait(f"{namespace_name}/{name} is ready", ready)


def values():
    return json.loads(run("yq", "-o=json", ".", str(CHART / "values.yaml")))


def namespace(name, privileged=False):
    return {"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": name, "labels": {"pod-security.kubernetes.io/enforce": "privileged" if privileged else "baseline"}}}


def attributes(resource):
    return {item["key"]: next(iter(item["value"].values())) for item in resource.get("attributes", [])}


def sink_documents():
    data = run("docker", "exec", NODE, "cat", f"{SINK_PATH}/output.json", check=False)
    return [json.loads(line) for line in data.splitlines() if line.strip()]


def metric_points(documents):
    result = []
    for document in documents:
        for resource in document.get("resourceMetrics", []):
            attrs = attributes(resource.get("resource", {}))
            for scope in resource.get("scopeMetrics", []):
                for metric in scope.get("metrics", []):
                    for kind in ("gauge", "sum", "histogram"):
                        for point in metric.get(kind, {}).get("dataPoints", []):
                            result.append((metric["name"], attrs, point))
    return result


def log_records(documents):
    return [(attributes(resource.get("resource", {})), record)
            for document in documents for resource in document.get("resourceLogs", [])
            for scope in resource.get("scopeLogs", []) for record in scope.get("logRecords", [])]


def install_sink(image):
    config = {
        "receivers": {"otlp": {"protocols": {"http": {"endpoint": "0.0.0.0:4318", "auth": {"authenticator": "bearertokenauth"}}}}},
        "extensions": {"bearertokenauth": {"token": TOKEN}},
        "exporters": {"file": {"path": "/data/output.json", "flush_interval": "100ms"}},
        "service": {"extensions": ["bearertokenauth"], "pipelines": {
            "metrics": {"receivers": ["otlp"], "exporters": ["file"]},
            "logs": {"receivers": ["otlp"], "exporters": ["file"]},
        }},
    }
    apply(
        {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "otlp-sink", "namespace": NAMESPACE}, "data": {"config.json": json.dumps(config)}},
        {"apiVersion": "apps/v1", "kind": "Deployment", "metadata": {"name": "otlp-sink", "namespace": NAMESPACE}, "spec": {"replicas": 1, "selector": {"matchLabels": {"app": "otlp-sink"}}, "template": {"metadata": {"labels": {"app": "otlp-sink"}}, "spec": {"containers": [{"name": "sink", "image": image, "args": ["--config=/conf/config.json"], "securityContext": {"runAsUser": 0}, "volumeMounts": [{"name": "config", "mountPath": "/conf"}, {"name": "output", "mountPath": "/data"}]}], "volumes": [{"name": "config", "configMap": {"name": "otlp-sink"}}, {"name": "output", "hostPath": {"path": SINK_PATH, "type": "DirectoryOrCreate"}}]}}}},
        {"apiVersion": "v1", "kind": "Service", "metadata": {"name": "otlp-sink", "namespace": NAMESPACE}, "spec": {"selector": {"app": "otlp-sink"}, "ports": [{"port": 4318}]}},
        {"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": {"name": "allow-test-otlp", "namespace": NAMESPACE}, "spec": {"podSelector": {"matchLabels": {"app": "otlp-sink"}}, "policyTypes": ["Ingress"], "ingress": [{"from": [{"namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": name}}} for name in (NAMESPACE, NODE_NAMESPACE)], "ports": [{"port": 4318, "protocol": "TCP"}]}]}},
    )
    rollout("deployments", "otlp-sink", NAMESPACE)


def collector_manifest():
    settings = {
        "enabled": True, "platformOnly": True, "installation": "metrics-e2e",
        "cluster": CLUSTER, "environment": "test", "collectionInterval": INTERVAL,
        "namespaces": [],
    }
    exporter = {"endpoint": f"http://otlp-sink.{NAMESPACE}:4318", "ingestToken": {"enabled": True}}
    overrides = {
        "global": {"region": "test-region", "platformTelemetry": settings, "exporter": exporter},
        "agent": {"image": {"tag": "unused"}},
        "controller": {"image": {"tag": "unused"}},
        "builds": {"enabled": False},
        "certManager": {"existingTLSSecret": "unused"},
        "gateway-helm": {"enabled": False},
        "otel-col-deploy": {"enabled": False},
        "otel-col-cluster": {"namespaceOverride": NAMESPACE},
        "otel-col-daemon": {"namespaceOverride": NODE_NAMESPACE},
    }
    path = RUN_DIR / "values.json"
    path.write_text(json.dumps(overrides))
    rendered = run("helm", "template", "metrics", str(CHART), "--namespace", NAMESPACE, "--values", str(path))
    documents = [json.loads(line) for line in run("yq", "-o=json", "-I=0", ".", "-", body=rendered).splitlines() if line.strip() not in ("", "null")]
    collectors = [document for document in documents if document["metadata"].get("labels", {}).get("app.kubernetes.io/part-of") == COLLECTOR_PART_OF]
    return json.dumps({"apiVersion": "v1", "kind": "List", "items": collectors})


def install_collectors(collectors):
    token = values()["global"]["exporter"]["ingestToken"]
    secret = {"apiVersion": "v1", "kind": "Secret", "type": "Opaque", "metadata": {"name": token["secretName"], "namespace": NAMESPACE}, "stringData": {token["secretKey"]: TOKEN}}
    node_secret = {**secret, "metadata": {"name": token["secretName"], "namespace": NODE_NAMESPACE}}
    apply(secret, node_secret)
    kube("apply", "-f", "-", body=collectors)
    rollout("daemonsets", "otel-col-daemon-agent", NODE_NAMESPACE)
    rollout("statefulsets", "otel-col-cluster", NAMESPACE)


def event(name, namespace_name):
    apply({"apiVersion": "v1", "kind": "Event", "metadata": {"name": name, "namespace": namespace_name}, "involvedObject": {"apiVersion": "v1", "kind": "Pod", "name": "metrics-fixture", "namespace": namespace_name}, "reason": "MetricsFixture", "message": name, "type": "Normal", "source": {"component": "metrics-e2e"}})


def execute():
    chart = values()
    image_config = chart["otel-col-daemon"]["image"]
    image = f'{image_config["repository"]}:{image_config["tag"]}'
    apply(namespace(NAMESPACE, privileged=True), namespace(NODE_NAMESPACE, privileged=True), namespace(TENANT_NAMESPACE))
    collectors = collector_manifest()
    kube("delete", "--ignore-not-found", "--wait=true", "-f", "-", body=collectors)
    kube("-n", NAMESPACE, "delete", "deployment", "otlp-sink", "--ignore-not-found", "--wait=true")
    run("docker", "exec", NODE, "rm", "-f", f"{SINK_PATH}/output.json")
    install_sink(image)
    install_collectors(collectors)
    wait("actual cluster and node metrics reach the authenticated OTLP destination", lambda: {"k8s.deployment.available", "k8s.node.allocatable_cpu", "k8s.node.cpu.usage"} <= {name for name, _, _ in metric_points(sink_documents())})
    points = metric_points(sink_documents())
    assert any(name == "k8s.deployment.available" and attrs.get("k8s.deployment.name") == "otlp-sink" and int(point.get("asInt", point.get("asDouble", 0))) == 1 for name, attrs, point in points)
    assert all(attrs.get("loco.telemetry.scope") == "platform" and attrs.get("k8s.cluster.name") == CLUSTER and attrs.get("loco.region") == "test-region" for _, attrs, _ in points)
    assert not any(attrs.get("k8s.namespace.name") == TENANT_NAMESPACE for _, attrs, _ in points)
    event(f"platform-before-{MARKER}", NAMESPACE)
    event(f"tenant-before-{MARKER}", TENANT_NAMESPACE)
    wait("platform Kubernetes events reach OTLP", lambda: f"platform-before-{MARKER}" in json.dumps(log_records(sink_documents())))
    assert f"tenant-before-{MARKER}" not in json.dumps(sink_documents())
    cutoff = time.time_ns()
    kube("-n", NAMESPACE, "scale", "deployment/otlp-sink", "--replicas=0")
    kube("-n", NAMESPACE, "wait", "--for=delete", "pod", "-l", "app=otlp-sink", f"--timeout={TIMEOUT}s")
    event(f"platform-queued-{MARKER}", NAMESPACE)
    time.sleep(OUTAGE_SECONDS)
    for namespace_name, selector in ((NAMESPACE, "app.kubernetes.io/name=otel-col-cluster"), (NODE_NAMESPACE, "app.kubernetes.io/name=otel-col-daemon")):
        kube("-n", namespace_name, "delete", "pod", "-l", selector, "--wait=true")
    rollout("statefulsets", "otel-col-cluster", NAMESPACE)
    rollout("daemonsets", "otel-col-daemon-agent", NODE_NAMESPACE)
    recovery = time.time_ns()
    kube("-n", NAMESPACE, "scale", "deployment/otlp-sink", "--replicas=1")
    rollout("deployments", "otlp-sink", NAMESPACE)
    wait("queued events survive destination failure and collector pod replacement", lambda: f"platform-queued-{MARKER}" in json.dumps(log_records(sink_documents())))
    wait("replayed metrics retain timestamps from the outage", lambda: any(cutoff < int(point.get("timeUnixNano", 0)) < recovery for _, _, point in metric_points(sink_documents())))
    assert f"tenant-before-{MARKER}" not in json.dumps(sink_documents())
    assert not any(attrs.get("k8s.namespace.name") == TENANT_NAMESPACE for _, attrs, _ in metric_points(sink_documents()))
    (RUN_DIR / "result.txt").write_text("PASS: real Kubernetes node/cluster metrics, scoped events, Secret-backed bearer token OTLP, and outage replay across collector pod replacement\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--no-teardown", action="store_true")
    options = parser.parse_args()
    RUN_DIR.mkdir(parents=True, exist_ok=True)
    for artifact in ("result.txt", "diagnostics.txt"):
        (RUN_DIR / artifact).unlink(missing_ok=True)
    try:
        clusters = run("kind", "get", "clusters").splitlines()
        if CLUSTER not in clusters:
            print(f"Creating local test cluster {CLUSTER}", flush=True)
            run("kind", "create", "cluster", "--name", CLUSTER, "--kubeconfig", str(KUBECONFIG))
        else:
            run("kind", "export", "kubeconfig", "--name", CLUSTER, "--kubeconfig", str(KUBECONFIG))
        execute()
    except Exception:
        diagnostics = kube("get", "pods", "-A", "-o", "wide", check=False)
        for namespace_name, workload in ((NAMESPACE, "statefulset/otel-col-cluster"), (NODE_NAMESPACE, "daemonset/otel-col-daemon-agent"), (NAMESPACE, "deployment/otlp-sink")):
            diagnostics += "\n" + kube("-n", namespace_name, "logs", workload, "--tail=80", check=False)
        (RUN_DIR / "diagnostics.txt").write_text(diagnostics)
        print(f"Diagnostics: {RUN_DIR / 'diagnostics.txt'}", flush=True)
        raise
    finally:
        if not options.no_teardown:
            for attempt in range(2):
                try:
                    run("kind", "delete", "cluster", "--name", CLUSTER)
                    break
                except RuntimeError:
                    if attempt:
                        raise
                    time.sleep(POLL)


if __name__ == "__main__":
    main()
