from test_platform_contracts import TEST_EXPORTER_ENDPOINT, TEST_REGION, chart_values, render_chart

COLLECTORS = {"otel-col-daemon-agent", "otel-col-deploy", "otel-col-cluster"}
WAIT = "exporter-token-wait"


def _workloads(enabled):
    overrides = {
        "global": {
            "region": TEST_REGION,
            "exporter": {"endpoint": TEST_EXPORTER_ENDPOINT, "bearerToken": {"enabled": enabled}},
        }
    }
    return {
        document["metadata"]["name"]: document["spec"]["template"]["spec"]
        for document in render_chart("loco-obs", overrides)
        if document["kind"] in {"DaemonSet", "StatefulSet"}
    }


def _wait(pod):
    (container,) = [container for container in pod["initContainers"] if container["name"] == WAIT]
    return container


def test_every_collector_waits_for_the_token_file_before_starting():
    token = chart_values("loco-obs")["global"]["exporter"]["bearerToken"]
    pods = _workloads(True)
    assert COLLECTORS <= set(pods)
    for name in COLLECTORS:
        pod = pods[name]
        wait = _wait(pod)
        assert pod["initContainers"][-1]["name"] == WAIT, name
        env = {variable["name"]: variable["value"] for variable in wait["env"]}
        assert env["TOKEN_FILE"] == f'{token["directory"]}/{token["secretKey"]}', name
        assert env["WAIT_INTERVAL_SECONDS"] == str(token["waitIntervalSeconds"]), name
        assert wait["command"][:2] == ["sh", "-c"], name
        assert 'until [ -s "$TOKEN_FILE" ]' in wait["command"][2], name
        assert 'sleep "$WAIT_INTERVAL_SECONDS"' in wait["command"][2], name
        assert "timeout" not in wait["command"][2] and "exit 1" not in wait["command"][2], name
        (mount,) = wait["volumeMounts"]
        assert (mount["name"], mount["mountPath"], mount["readOnly"]) == ("exporter-token", token["directory"], True), name
        context = wait["securityContext"]
        assert context["runAsNonRoot"] and not context["allowPrivilegeEscalation"], name
        assert context["capabilities"]["drop"] == ["ALL"], name
        volume = next(volume for volume in pod["volumes"] if volume["name"] == "exporter-token")
        assert volume["secret"]["secretName"] == token["secretName"], name


def test_collectors_without_a_bearer_token_start_without_waiting():
    pods = _workloads(False)
    for name in COLLECTORS:
        assert _wait(pods[name])["command"][2] == "", name
