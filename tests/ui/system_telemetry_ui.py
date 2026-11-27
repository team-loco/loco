import argparse
import json
import re
from datetime import UTC, datetime, timedelta

from harness import expect_rpc, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check Loco's own logs, metrics and traces in System: who sees them and how they filter"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "system_telemetry", "tests/ui/fixtures/system/scenario.json")
ui = state["ui_url"]

OBS = "**/loco.observability.v1.ObservabilityService/"
TRACE = "5b8aa5a2d2c872e8321cf37308d69df2"
ROOT = "POST /v1/applications"
LEVELS = {"info": "LOG_LEVEL_INFO", "warn": "LOG_LEVEL_WARN"}
now = datetime.now(UTC)


def stamp(seconds):
    return (now - timedelta(seconds=seconds)).isoformat().replace("+00:00", "Z")


def line(seconds, level, body, component, cluster):
    return {
        "timestamp": stamp(seconds),
        "level": LEVELS[level],
        "body": body,
        "resourceAttributes": {"service.name": component, "loco.cluster.id": cluster},
    }


LOGS = [
    line(5, "info", "request served", "loco-api", "control-plane"),
    line(9, "warn", "slow query on organizations", "loco-api", "control-plane"),
    line(14, "info", "reconciled application", "loco-controller", "us-ca-1"),
]


def span(span_id, parent, name, offset, duration):
    return {
        "traceId": TRACE,
        "spanId": span_id,
        "parentSpanId": parent,
        "name": name,
        "kind": "Server",
        "serviceName": "loco-api",
        "startTime": stamp(60 - offset),
        "durationNanos": str(duration * 1000000),
        "statusCode": "Ok",
        "resourceAttributes": {
            "service.name": "loco-api",
            "loco.cluster.id": "control-plane",
        },
    }


SPANS = [
    span("00000000000000a1", "", ROOT, 0, 40),
    span("00000000000000a2", "00000000000000a1", "INSERT applications", 5, 12),
]


def request_body(request, method):
    """The JSON of a request; a stream's body is one enveloped message after a five-byte header."""
    if method == "TailLogs":
        return json.loads(request.post_data_buffer[5:])
    return request.post_data_json or {}


def mock_telemetry(context, requests):
    """Answers every observability call from fixed data and records each request body."""

    def respond(route):
        method = route.request.url.rsplit("/", 1)[-1]
        body = request_body(route.request, method)
        requests.append((method, body))
        query = body.get("query", "")
        if method == "QueryLogs":
            entries = [
                entry
                for entry in LOGS
                if "@component:" not in query
                or f"@component:{entry['resourceAttributes']['service.name']}" in query
            ]
            route.fulfill(json={"entries": [] if "@trace:" in query else entries})
        elif method == "QueryLogBuckets":
            route.fulfill(
                json={
                    "buckets": [
                        {"startTime": stamp(10), "level": level, "count": "1"}
                        for level in LEVELS.values()
                    ]
                }
            )
        elif method == "QueryMetrics":
            points = [
                {"timestamp": stamp(seconds), "value": value}
                for seconds, value in ((900, 2), (600, 5), (300, 3))
            ]
            route.fulfill(json={"series": [{"points": points}]})
        elif method == "ListTraces":
            route.fulfill(
                json={
                    "totalCount": "1",
                    "summaries": [
                        {
                            "traceId": TRACE,
                            "rootSpanName": ROOT,
                            "serviceName": "loco-api",
                            "startTime": stamp(60),
                            "durationNanos": "40000000",
                            "status": "TRACE_STATUS_OK",
                            "spanCount": str(len(SPANS)),
                        }
                    ],
                }
            )
        elif method == "QueryTraceBuckets":
            route.fulfill(
                json={
                    "buckets": [],
                    "total": {
                        "traceCount": "1",
                        "errorCount": "0",
                        "p50DurationNanos": "40000000",
                        "p95DurationNanos": "40000000",
                    },
                }
            )
        elif method == "GetTrace":
            route.fulfill(json={"spans": SPANS})
        else:
            route.continue_()

    context.route(OBS + "*", respond)


def scoped(requests, method):
    return [body for name, body in requests if name == method]


with session(repo / "tests/ui/out/system-telemetry") as browser:
    # Without a system role, the telemetry pages are not found and no telemetry is requested.
    context, page, errors = open_page(browser, user="member", state=state)
    requests = []
    mock_telemetry(context, requests)
    for path in ("/system/logs", "/system/metrics", "/system/traces"):
        page.goto(ui + path)
        expect(
            page.get_by_role("heading", name="Page not found", exact=True)
        ).to_be_visible()
    assert not requests, requests
    context.close()

    # A system reader reads Loco's own logs, filters them by component and opens a trace.
    context, page, errors = open_page(browser, user="reader", state=state)
    requests = []
    mock_telemetry(context, requests)
    page.goto(ui + "/system/logs?live=0")
    expect(
        page.get_by_role("heading", level=1, name="Logs", exact=True)
    ).to_be_visible()
    sidebar = page.locator('[data-slot="sidebar"]')
    for name in ("Logs", "Metrics", "Traces"):
        expect(sidebar.get_by_role("link", name=name, exact=True)).to_be_visible()
    lines = page.get_by_role("region", name="Log lines")
    expect(lines).to_contain_text("request served")
    expect(lines).to_contain_text("reconciled application")
    expect(lines.get_by_role("columnheader", name="Component")).to_be_visible()
    expect(lines.get_by_role("columnheader", name="Cluster")).to_be_visible()
    expect(lines.get_by_role("columnheader", name="Resource")).to_have_count(0)
    first = scoped(requests, "QueryLogs")[0]
    assert first.get("installation") is True and "workspaceId" not in first, first

    search = page.get_by_placeholder(re.compile("^Search messages"))
    search.fill("@")
    expect(page.get_by_role("option", name=re.compile("^@component"))).to_be_visible()
    expect(page.get_by_role("option", name=re.compile("^@cluster"))).to_be_visible()
    expect(page.get_by_role("option", name=re.compile("^@resource"))).to_have_count(0)
    search.fill("@component:loco-api")
    search.press("Enter")
    page.locator("h1").click()
    expect(page).to_have_url(re.compile(r"component"))
    expect(lines).to_contain_text("request served")
    expect(lines).not_to_contain_text("reconciled application")

    # The range and the filter follow the reader to the other pages.
    sidebar.get_by_role("link", name="Metrics", exact=True).click()
    expect(page).to_have_url(re.compile(r"/system/metrics.*component"))
    expect(
        page.get_by_role("heading", level=1, name="Metrics", exact=True)
    ).to_be_visible()
    expect(page.get_by_role("heading", name="API", exact=True)).to_be_visible()
    expect(
        page.get_by_role("heading", name="Build controller", exact=True)
    ).to_have_count(0)
    expect(
        page.get_by_role("heading", name="Worker clusters", exact=True)
    ).to_have_count(0)
    expect(page.get_by_role("region", name="Open streams")).to_be_visible()
    metrics = scoped(requests, "QueryMetrics")
    assert metrics and all(m.get("installation") is True for m in metrics), metrics
    assert all(m["attributes"]["component"] == "loco-api" for m in metrics), metrics
    assert not any("controller" in m["metricName"] for m in metrics), metrics

    # Suggestions use the IDs ingestion stores, including the control plane, and selecting
    # one sends that ID in each metric request. Cluster metadata comes from the seeded API.
    with expect_rpc(page, "ListClusters", lambda body: True) as fleet:
        page.goto(ui + "/system/metrics")
    cluster = next(c for c in fleet.value.json()["clusters"] if c["name"] == "us-ca-1")
    search = page.get_by_placeholder("Type @component or @cluster to filter")
    search.fill("@cluster:")
    expect(
        page.get_by_role("option", name="@cluster:control-plane", exact=True)
    ).to_be_visible()
    expect(
        page.get_by_role("option", name=f"@cluster:{cluster['id']}", exact=True)
    ).to_be_visible()
    with expect_rpc(
        page,
        "QueryMetrics",
        lambda body: body.get("attributes", {}).get("cluster") == cluster["id"],
    ) as filtered:
        page.get_by_role("option", name=f"@cluster:{cluster['id']}", exact=True).click()
    page.locator("h1").click()
    expect(page).to_have_url(re.compile(cluster["id"]))
    assert filtered.value.request.post_data_json["installation"] is True

    # Clearing the filter shows every component's group.
    page.goto(ui + "/system/metrics")
    for group in (
        "API",
        "Application controller",
        "Build controller",
        "Worker clusters",
    ):
        expect(page.get_by_role("heading", name=group, exact=True)).to_be_visible()
    expect(page.get_by_role("region", name="Reconciles")).to_have_count(2)
    expect(page.get_by_role("region", name="Node CPU in use")).to_be_visible()

    sidebar.get_by_role("link", name="Traces", exact=True).click()
    expect(
        page.get_by_role("heading", level=1, name="Traces", exact=True)
    ).to_be_visible()
    row = page.get_by_role("row", name=f"Open trace {ROOT}", exact=True)
    expect(row).to_contain_text("loco-api")
    listed = scoped(requests, "ListTraces")[0]["scope"]
    assert listed.get("installation") is True and "workspaceId" not in listed, listed
    row.click(position={"x": 20, "y": 10})
    drawer = page.get_by_role("dialog", name=ROOT)
    expect(drawer).to_be_visible()
    expect(drawer.get_by_role("tab", name=re.compile("^Logs"))).to_be_visible()
    expect(drawer.get_by_role("tab", name=re.compile("^Events"))).to_have_count(0)
    expect(drawer.get_by_role("tab", name="Infrastructure")).to_have_count(0)
    traced = scoped(requests, "GetTrace")[0]["scope"]
    assert traced.get("installation") is True and "workspaceId" not in traced, traced

    # Live tail reads the installation scope too.
    with page.expect_request(lambda request: request.url.endswith("/TailLogs")):
        page.goto(ui + "/system/logs")
    for _ in range(50):
        if scoped(requests, "TailLogs"):
            break
        page.wait_for_timeout(100)
    assert scoped(requests, "TailLogs"), requests

    # Every telemetry request, the nested trace scope, buckets and the drawer's logs included,
    # asks for the installation and names no workspace, environment or resource.
    methods = {method for method, _ in requests}
    for expected in ("QueryLogs", "QueryLogBuckets", "QueryMetrics", "ListTraces"):
        assert expected in methods, methods
    for method, body in requests:
        scope = body.get("scope", body)
        assert scope.get("installation") is True, (method, body)
        for field in ("workspaceId", "environmentId", "resourceIds"):
            assert not scope.get(field), (method, body)
    assert not errors, errors
    context.close()
