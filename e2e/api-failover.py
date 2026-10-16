import argparse
import hashlib
import json
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
HEALTHY_PORT = 18080
CLOSED_PORT = 18081
DEADLINE_SECONDS = 90
PAYLOAD_BYTES = 8192
LARGE_PAYLOAD_BYTES = 2 * 1024 * 1024
REQUESTS_PER_STATUS = 12
MIXED_REQUEST_COUNT = 40
SAFE_TRIGGERS = {"connect-failure", "reset-before-request", "refused-stream"}


def command(*args, **kwargs):
    return subprocess.check_output(args, text=True, timeout=120, **kwargs)


def kube(*args, **kwargs):
    return command("kubectl", *args, **kwargs)


def wait_for(check, description):
    deadline = time.monotonic() + DEADLINE_SECONDS
    while True:
        value = check()
        if value:
            return value
        if time.monotonic() >= deadline:
            raise TimeoutError(description)
        time.sleep(0.25)


def get_json(url):
    with urllib.request.urlopen(url, timeout=5) as response:
        return json.load(response)


def stop_process(process):
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


def forward(namespace, pod, remote, output):
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    log = (output / f"forward-{port}.log").open("w")
    process = subprocess.Popen(
        ["kubectl", "-n", namespace, "port-forward", "pod/" + pod,
         f"{port}:{remote}", "--address", "127.0.0.1"],
        stdout=log, stderr=subprocess.STDOUT,
    )
    log.close()
    def ready():
        if process.poll() is not None:
            raise RuntimeError("port forward exited")
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                return True
        except OSError:
            return False
    try:
        wait_for(ready, "port forward readiness")
    except BaseException as original_error:
        try:
            stop_process(process)
        except Exception as cleanup_error:
            raise BaseExceptionGroup("Port forward readiness and cleanup failed", [original_error, cleanup_error]) from None
        raise
    return process, f"http://127.0.0.1:{port}"


def post(base, host, path, raw):
    request = urllib.request.Request(base + path, data=raw, headers={
        "Host": host, "Content-Type": "application/json",
    })
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            return response.status, json.load(response)
    except urllib.error.HTTPError as error:
        body = error.read()
        try:
            return error.code, json.loads(body)
        except json.JSONDecodeError:
            return error.code, {"body": body.decode(errors="replace")}


def route_configs(admin, namespace, names):
    result = {}
    def walk(value):
        if isinstance(value, dict):
            name = value.get("name", "")
            if "route" in value and any(
                name.startswith(f"httproute/{namespace}/{route}/") for route in names
            ):
                result[name] = value
            for child in value.values():
                walk(child)
        elif isinstance(value, list):
            for child in value:
                walk(child)
    walk(get_json(admin + "/config_dump?resource=dynamic_route_configs"))
    return result


def api_clusters(configs, hosts):
    return {host: next(route["route"]["cluster"] for route in configs.values()
        if route["name"].endswith("/" + host.replace(".", "_")) and any(
            ref.get("sectionName") == "api" for ref in route["metadata"]["filter_metadata"]["envoy-gateway"]["resources"]))
        for host in hosts}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--gateway-namespace", required=True)
    parser.add_argument("--gateway-url", required=True)
    parser.add_argument("--hostname-suffix", required=True)
    parser.add_argument("--image", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    name = "api-failover-" + secrets.token_hex(4)
    label = {"e2e.loco.io/fixture": name}
    owned = []
    forwards = []
    evidence = {"layouts": {}}
    admin = direct = None
    names = []
    def apply(document):
        metadata = document["metadata"]
        metadata.update(namespace=args.namespace)
        metadata.setdefault("labels", {}).update(label)
        key = (document["kind"], metadata["name"])
        if key not in owned:
            owned.append(key)
        kube("apply", "-f", "-", input=json.dumps(document))
    def obj(kind, object_name, version="v1"):
        return {"apiVersion": version, "kind": kind, "metadata": {"name": object_name}}
    try:
        pod = obj("Pod", name)
        pod["spec"] = {
            "restartPolicy": "Never", "automountServiceAccountToken": False,
            "securityContext": {"runAsNonRoot": True, "seccompProfile": {"type": "RuntimeDefault"}},
            "containers": [{"name": "server", "image": args.image, "imagePullPolicy": "IfNotPresent",
                "securityContext": {"allowPrivilegeEscalation": False, "capabilities": {"drop": ["ALL"]}},
                "ports": [{"name": "http", "containerPort": HEALTHY_PORT}],
                "readinessProbe": {"httpGet": {"path": "/", "port": "http"}},
                "resources": {"requests": {"cpu": "25m", "memory": "16Mi"}, "limits": {"memory": "64Mi"}},
            }],
        }
        print("CHECK API failover: starting owned fixture", flush=True)
        apply(pod)
        policy = obj("NetworkPolicy", name, "networking.k8s.io/v1")
        policy["spec"] = {"podSelector": {"matchLabels": label}, "policyTypes": ["Ingress"],
            "ingress": [{"from": [{"namespaceSelector": {"matchLabels": {
                "kubernetes.io/metadata.name": args.gateway_namespace}}, "podSelector": {"matchLabels": {
                "app.kubernetes.io/component": "proxy", "app.kubernetes.io/managed-by": "envoy-gateway"}}}],
                "ports": [{"protocol": "TCP", "port": port} for port in [HEALTHY_PORT, CLOSED_PORT]]}]}
        apply(policy)
        kube("-n", args.namespace, "wait", "pod/" + name, "--for=condition=Ready", "--timeout=120s")
        ip = json.loads(kube("-n", args.namespace, "get", "pod", name, "-o", "json"))["status"]["podIP"]
        pods = json.loads(kube("-n", args.gateway_namespace, "get", "pods", "-l",
            "gateway.envoyproxy.io/owning-gateway-name=eg,app.kubernetes.io/component=proxy", "-o", "json"))["items"]
        assert len(pods) == 1
        process, admin = forward(args.gateway_namespace, pods[0]["metadata"]["name"], 19000, args.output)
        forwards.append(process)
        process, direct = forward(args.namespace, name, HEALTHY_PORT, args.output)
        forwards.append(process)
        for layout in ["headless", "shared", "separate"]:
            release = name + "-" + layout
            host = release + "." + args.hostname_suffix
            print(f"CHECK API failover {layout}: programming owned routes and endpoints", flush=True)
            current = {}
            evidence["layouts"][layout] = current
            values = {"gatewayNamespace": args.gateway_namespace, "controlPlaneHostname": host,
                "api": {"image": {"tag": "e2e"}, "existingSecret": "unused"},
                "obsProxy": {"image": {"tag": "e2e"}, "clickhouse": {
                    "existingSecret": {"name": "unused", "key": "url"},
                    "migratorExistingSecret": {"name": "unused", "key": "url"}}},
                "ui": {"enabled": layout != "headless", "hostname": host if layout == "shared" else "ui-" + host,
                    "image": {"tag": "e2e"}}}
            value_file = args.output / (layout + "-values.json")
            value_file.write_text(json.dumps(values))
            rendered = command("helm", "template", release, str(ROOT / "charts/loco-control-plane"),
                "-n", args.namespace, "-f", str(value_file))
            documents = [json.loads(line) for line in command("yq", "-o=json", "-I=0", ".", "-", input=rendered).splitlines() if line != "null"]
            routes = [document for document in documents if document["kind"] == "HTTPRoute"]
            (traffic,) = [document for document in documents if document["kind"] == "BackendTrafficPolicy"]
            for suffix in ["-api", "-ui"]:
                service = obj("Service", release + suffix)
                service["spec"] = {"ports": [{"name": "http", "port": 80}]}
                apply(service)
                for endpoint_suffix, port in [("-healthy", HEALTHY_PORT), ("-closed", CLOSED_PORT)]:
                    endpoint = obj("EndpointSlice", release + suffix + endpoint_suffix, "discovery.k8s.io/v1")
                    endpoint["metadata"]["labels"] = {"kubernetes.io/service-name": release + suffix}
                    endpoint.update(addressType="IPv4", ports=[{"name": "http", "protocol": "TCP", "port": port}],
                        endpoints=[{"addresses": [ip], "conditions": {"ready": True}}])
                    apply(endpoint)
            for route in routes:
                apply(route)
            names = [route["metadata"]["name"] for route in routes]
            expected_rules = sum(len(route["spec"]["rules"]) for route in routes)
            def endpoints_ready():
                configs = route_configs(admin, args.namespace, names)
                clusters = {cluster["name"]: cluster for cluster in get_json(admin + "/clusters?format=json")["cluster_statuses"]}
                return len(configs) == expected_rules and all(
                    len(clusters.get(route["route"]["cluster"], {}).get("host_statuses", [])) == 2
                    for route in configs.values())
            wait_for(endpoints_ready, "healthy and closed endpoints programmed")
            before = wait_for(lambda: (configs if len(configs := route_configs(admin, args.namespace, names)) == expected_rules else None), "rendered routes")
            current["beforeRoutes"] = before
            assert all("retry_policy" not in route["route"] for route in before.values())
            targets = [
                (route["spec"]["hostnames"][0], rule["matches"][0]["path"]["value"].rstrip("/"))
                for route in routes for rule in route["spec"]["rules"] if rule.get("name") == "api"
            ]
            clusters = api_clusters(before, [host for host, _ in targets])
            assert len(set(clusters.values())) == len(targets)
            current["apiClusters"] = clusters
            def connection_stats():
                observed = {cluster["name"]: cluster for cluster in get_json(admin + "/clusters?format=json")["cluster_statuses"]}
                return {host: sum(int(stat.get("value", 0)) for endpoint in observed[cluster]["host_statuses"]
                    for stat in endpoint.get("stats", []) if stat["name"] == "cx_connect_fail")
                    for host, cluster in clusters.items()}
            current["connectionStatsBeforeBaseline"] = connection_stats()
            requests = []
            current["requests"] = requests
            def send(route_host, prefix, phase, path, expected, index, size=PAYLOAD_BYTES):
                identifier = f"{route_host}-{phase}-{path}-{index:03}"
                raw = json.dumps({"id": identifier, "payload": "x" * size}).encode()
                status, response = post(args.gateway_url, route_host, prefix + "/" + path, raw)
                handled = response.get("id") == identifier
                receipt = {"bytes": len(raw), "sha256": hashlib.sha256(raw).hexdigest()}
                requests.append({"id": identifier, "phase": phase, "status": status, "response": response,
                    "handled": handled, **receipt})
                if expected is None:
                    assert (handled and status == 200) or (not handled and status == 503 and "upstream connect error" in response.get("body", "")), requests[-1]
                else:
                    assert handled and status == expected, requests[-1]
                return handled
            cohorts = {}
            current["cohorts"] = cohorts
            for route_host, prefix in targets:
                handled = sum(send(route_host, prefix, "red", "ok", None, index) for index in range(MIXED_REQUEST_COUNT))
                failures = MIXED_REQUEST_COUNT - handled
                assert 0 < failures < MIXED_REQUEST_COUNT
                cohorts[route_host] = {"beforeConnectionFailures": failures, "totalPerPhase": MIXED_REQUEST_COUNT}
            current["connectionStatsAfterBaseline"] = connection_stats()
            apply(traffic)
            def configured():
                configs = route_configs(admin, args.namespace, names)
                api = [route for route in configs.values() if any(
                    ref.get("sectionName") == "api" for ref in route["metadata"]["filter_metadata"]["envoy-gateway"]["resources"])]
                return configs if len(configs) == expected_rules and api and all("retry_policy" in route["route"] for route in api) else None
            after = wait_for(configured, "API retry policy")
            current["afterRoutes"] = after
            for key, route in after.items():
                clean = json.loads(json.dumps(route))
                retry = clean["route"].pop("retry_policy", None)
                if retry:
                    provenance = clean["metadata"]["filter_metadata"]["envoy-gateway"].pop("policies")
                    assert provenance == [{"namespace": args.namespace, "kind": "BackendTrafficPolicy", "name": traffic["metadata"]["name"]}]
                assert clean == before[key], (layout, key)
                if retry:
                    assert set(retry["retry_on"].split(",")) == SAFE_TRIGGERS and retry["num_retries"] == 1
                    assert not retry.get("retriable_status_codes") and not retry.get("per_try_timeout")
            def retry_stats():
                stats = {stat["name"]: stat["value"] for stat in get_json(admin + "/stats?format=json")["stats"] if "name" in stat}
                return {host: {key: int(stats.get("cluster." + cluster + "." + key, 0))
                    for key in ["upstream_rq_retry", "upstream_rq_retry_success", "upstream_rq_retry_limit_exceeded"]}
                    for host, cluster in clusters.items()}
            stats_before = retry_stats()
            for route_host, prefix in targets:
                handled = sum(send(route_host, prefix, "new", "ok", None, index) for index in range(MIXED_REQUEST_COUNT))
                failures = MIXED_REQUEST_COUNT - handled
                cohorts[route_host]["afterConnectionFailures"] = failures
                assert failures * 2 <= cohorts[route_host]["beforeConnectionFailures"], cohorts[route_host]
            stats_after = retry_stats()
            current.update(retryStatsBefore=stats_before, retryStatsAfter=stats_after, connectionStatsAfterPolicy=connection_stats())
            for route_host, _ in targets:
                assert stats_after[route_host]["upstream_rq_retry_success"] > stats_before[route_host]["upstream_rq_retry_success"], (stats_before, stats_after)
            for suffix in ["-api", "-ui"]:
                kube("-n", args.namespace, "delete", "endpointslice", release + suffix + "-closed", "--wait=true")
            def only_healthy():
                cluster_names = {route["route"]["cluster"] for route in after.values()}
                clusters = get_json(admin + "/clusters?format=json")["cluster_statuses"]
                selected = [cluster for cluster in clusters if cluster["name"] in cluster_names]
                return len(selected) == len(cluster_names) and all(len(cluster.get("host_statuses", [])) == 1 for cluster in selected)
            wait_for(only_healthy, "closed endpoint removed for large-body control")
            for route_host, prefix in targets:
                for path, expected in [("ok", 200), ("error", 500), ("handled-503", 503)]:
                    for index in range(REQUESTS_PER_STATUS):
                        send(route_host, prefix, "healthy", path, expected, index)
                    send(route_host, prefix, "large", path, expected, 0, LARGE_PAYLOAD_BYTES)
            ledger = get_json(direct + "/state")
            for item in requests:
                expected = [{"bytes": item["bytes"], "sha256": item["sha256"]}] if item["handled"] else []
                assert ledger.get(item["id"], []) == expected, (item, ledger.get(item["id"]))
            current.update(requestCount=len(requests), largeBodyBytes=LARGE_PAYLOAD_BYTES,
                ledger={item["id"]: ledger.get(item["id"], []) for item in requests})
            print(f"PASS API failover {layout}: {len(requests)} fixed requests; failures {cohorts}; handled 500/503 once-only", flush=True)
    except BaseException:
        for diagnostic, fetch in [
            ("failedRoutes", lambda: route_configs(admin, args.namespace, names)),
            ("failedClusters", lambda: [cluster for cluster in get_json(admin + "/clusters?format=json")["cluster_statuses"] if any(route_name in cluster["name"] for route_name in names)]),
            ("failedLedger", lambda: get_json(direct + "/state")),
        ]:
            try:
                evidence[diagnostic] = fetch()
            except Exception as error:
                evidence[diagnostic] = str(error)
        raise
    finally:
        original_error = sys.exception()
        errors = []
        for process in reversed(forwards):
            try:
                stop_process(process)
            except Exception as error:
                errors.append(error)
        for kind, object_name in reversed(owned):
            try:
                kube("-n", args.namespace, "delete", kind, object_name, "--ignore-not-found=true", "--wait=true", "--timeout=90s")
            except Exception as error:
                errors.append(error)
        try:
            remaining = json.loads(kube("-n", args.namespace, "get", "pod,service,endpointslice,httproute,backendtrafficpolicy,networkpolicy", "-l", "e2e.loco.io/fixture=" + name, "-o", "json"))["items"]
            assert not remaining, remaining
        except Exception as error:
            errors.append(error)
        evidence["cleanup"] = not errors
        evidence["errors"] = [str(error) for error in ([original_error] if original_error else []) + errors]
        (args.output / "api-failover.json").write_text(json.dumps(evidence, indent=2))
        if errors:
            raise BaseExceptionGroup("API failover cleanup failed", ([original_error] if original_error else []) + errors)


if __name__ == "__main__":
    main()
