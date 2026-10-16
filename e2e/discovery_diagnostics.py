import argparse
import json
import subprocess
from pathlib import Path

COMMAND_TIMEOUT_SECONDS = 10
LOG_TAIL_LINES = 100
DNS_NAMESPACE = "kube-system"
DNS_SELECTOR = "k8s-app=kube-dns"
CALLER_SERVICE = "caller"
TARGET_SERVICE = "cart"


def collect(kubeconfig, context, namespaces, output):
    output.mkdir(parents=True, exist_ok=True)
    base = ["kubectl", "--kubeconfig", str(kubeconfig), "--context", context, "--request-timeout=5s"]
    outcomes = []

    def command(namespace, *args):
        try:
            result = subprocess.run(
                [*base, "-n", namespace, *args], capture_output=True, text=True, timeout=COMMAND_TIMEOUT_SECONDS,
            )
            outcomes.append({"namespace": namespace, "operation": list(args), "exitCode": result.returncode, "stderr": result.stderr})
            return result.stdout
        except subprocess.TimeoutExpired:
            outcomes.append({"namespace": namespace, "operation": list(args), "timeout": True})
            return ""

    def objects(namespace, kind):
        text = command(namespace, "get", kind, "-o", "json")
        return json.loads(text).get("items", []) if text else []

    def write(name, data):
        (output / name).write_text(json.dumps(data, indent=2) + "\n")

    def pod_status(pod):
        return {
            "name": pod["metadata"]["name"], "uid": pod["metadata"]["uid"],
            "node": pod["spec"].get("nodeName"), "phase": pod.get("status", {}).get("phase"),
            "containers": [{"name": container["name"], "ready": container.get("ready"),
                            "restarts": container.get("restartCount"),
                            "state": {key: value.get("reason") for key, value in container.get("state", {}).items()}}
                           for container in pod.get("status", {}).get("containerStatuses", [])],
        }

    for namespace in namespaces:
        if "/" in namespace or namespace in (".", ".."):
            raise ValueError("invalid namespace")
        services = objects(namespace, "services")
        write(f"{namespace}-services.json", [{"name": item["metadata"]["name"], "spec": {
            key: value for key, value in item["spec"].items() if key in ("clusterIP", "ports", "selector")
        }} for item in services])
        slices = objects(namespace, "endpointslices")
        write(f"{namespace}-endpointslices.json", [{"name": item["metadata"]["name"],
              "addressType": item["addressType"], "ports": item.get("ports", []),
              "endpoints": [{key: value for key, value in endpoint.items() if key in ("addresses", "conditions", "nodeName")}
                            for endpoint in item.get("endpoints", [])]} for item in slices])
        policies = objects(namespace, "networkpolicies")
        write(f"{namespace}-networkpolicies.json", [{"name": item["metadata"]["name"], "spec": item["spec"]} for item in policies])
        pods = objects(namespace, "pods")
        write(f"{namespace}-pods.json", [pod_status(pod) for pod in pods])
        caller = next((service for service in services if service["metadata"]["name"] == CALLER_SERVICE), None)
        selector = caller["spec"].get("selector", {}) if caller else {}
        pod = next((pod for pod in pods if selector and all(pod["metadata"].get("labels", {}).get(key) == value for key, value in selector.items())
                    and not pod["metadata"].get("deletionTimestamp") and pod.get("status", {}).get("phase") == "Running"), None)
        if pod:
            name = pod["metadata"]["name"]
            resolver = command(namespace, "exec", name, "--", "cat", "/etc/resolv.conf")
            (output / f"{namespace}-caller-resolv.conf").write_text(resolver)
            lookup = command(namespace, "exec", name, "--", "nslookup", f"{TARGET_SERVICE}.{namespace}.svc.cluster.local")
            (output / f"{namespace}-caller-nslookup.txt").write_text(lookup)

    dns = command(DNS_NAMESPACE, "get", "pods", "-l", DNS_SELECTOR, "-o", "json")
    write("coredns-pods.json", [pod_status(pod) for pod in json.loads(dns).get("items", [])] if dns else [])
    logs = command(DNS_NAMESPACE, "logs", "-l", DNS_SELECTOR, "--all-containers", "--prefix", f"--tail={LOG_TAIL_LINES}", "--max-log-requests=2")
    (output / "coredns.log").write_text(logs)
    write("commands.json", outcomes)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", type=Path, required=True)
    parser.add_argument("--context", required=True)
    parser.add_argument("--namespace", action="append", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    collect(args.kubeconfig, args.context, args.namespace, args.output)


if __name__ == "__main__":
    main()
