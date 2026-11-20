import argparse
import itertools
import json
import math
import re
import time
import urllib.parse
from datetime import UTC, datetime, timedelta
from pathlib import Path

from harness import (
    call,
    evidence,
    expect_rpc,
    load_state,
    open_page,
    repo,
    session,
    sql,
    wait_for_query,
)
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check compact controls, segmented motion and settings navigation"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/shared-controls"))
args = parser.parse_args()
state = load_state(
    args, "shared-controls", "tests/ui/fixtures/settings-labels/scenario.json"
)
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]
org = state["orgs"]["acme"]["id"]


def service(cpu="100m"):
    return {
        "routing": {"port": 8080},
        "regions": {
            "us-east-1": {
                "enabled": True,
                "cpu": cpu,
                "memory": "128Mi",
                "minReplicas": 1,
                "maxReplicas": 1,
            }
        },
    }


resource = state.get("shared_controls_resource")
if resource is None:
    resource = evidence.seed_resources(
        state,
        [{"workspace": "acme/shared", "name": "controls-pilot", "service": service()}],
    )["acme/shared/controls-pilot"]
    state["shared_controls_resource"] = resource
    evidence.write_json(Path(args.state), state)
sorted_resources = state.get("controls_resources")
if sorted_resources is None:
    sorted_resources = list(
        evidence.seed_resources(
            state,
            [
                {"workspace": "acme/shared", "name": name, "service": service(cpu)}
                for name, cpu in (("controls-alpha", "100m"), ("controls-zeta", "200m"))
            ],
        ).values()
    )
    state["controls_resources"] = sorted_resources
    evidence.write_json(Path(args.state), state)
base = f"/org/{org}/wks/{workspace}"
retired = state.get("controls_retired")
if retired is None:
    retired = evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": "controls-retired",
                "service": service(),
            }
        ],
    )["acme/shared/controls-retired"]
    call(
        state,
        "loco.resource.v1.ResourceService/DeleteResource",
        {"resourceId": retired},
        user=state["orgs"]["acme"]["owner"],
    )
    state["controls_retired"] = retired
    evidence.write_json(Path(args.state), state)
# Traffic of a resource that never existed in the workspace, so no audit event names it.
UNKNOWN_RESOURCE = "00000000-0000-4000-8000-0000000000aa"


def stamp(now, delta):
    return (now - delta).isoformat().replace("+00:00", "Z")


telemetry_points = []
# The value each gateway traffic series answers at every point.
TRAFFIC_RATES = {
    "cluster.upstream_rq_completed": 10,
    "cluster.upstream_rq_xx": 1,
    "cluster.upstream_cx_rx_bytes_total": 1000,
    "cluster.upstream_rq_time": 190,
}
traffic_empty = False
log_bucket_requests = []
LEVELS = ("LOG_LEVEL_INFO", "LOG_LEVEL_WARN", "LOG_LEVEL_ERROR", "LOG_LEVEL_DEBUG")


def mock_telemetry(context):
    now = datetime.now(UTC)
    logs = [
        {
            "timestamp": stamp(now, timedelta(seconds=i * 13 + 1)),
            "level": LEVELS[i % 4],
            "body": f"Request completed ({i + 1})",
            "resourceId": resource,
            "resourceAttributes": {"cloud.region": "us-east-1"},
        }
        for i in range(18)
    ]
    points = [
        {"timestamp": stamp(now, timedelta(minutes=minute)), "value": value}
        for minute, value in ((50, 0.25), (30, 0.55), (10, 0.35))
    ]
    telemetry_points[:] = points
    context.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogs",
        lambda route: route.fulfill(
            json={"entries": logs, "totalMatched": str(len(logs))}
        ),
    )

    def log_buckets(route):
        body = route.request.post_data_json
        width = body["intervalSeconds"]
        at = datetime.fromisoformat(body["endTime"].replace("Z", "+00:00")).timestamp()
        start = (
            datetime.fromtimestamp(int(at) // width * width, UTC)
            .isoformat()
            .replace("+00:00", "Z")
        )
        if body["groupBy"] == "LOG_BUCKET_GROUP_RESOURCE":
            route.fulfill(
                json={
                    "buckets": [
                        {"startTime": start, "resourceId": resource, "count": "647"}
                    ]
                }
            )
            return
        log_bucket_requests.append(body)
        route.fulfill(
            json={
                "buckets": [
                    {"startTime": start, "level": level, "count": str(count)}
                    for level, count in zip(LEVELS, (10005, 2, 3, 4), strict=True)
                ]
            }
        )

    context.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogBuckets", log_buckets
    )

    def query_metrics(route):
        body = route.request.post_data_json
        rate = TRAFFIC_RATES.get(body["metricName"])
        if rate is None:
            series = [{"resourceId": resource, "points": points}]
        elif traffic_empty:
            series = []
        else:
            # Buckets sit inside the requested window, as the server's do: the same
            # three offsets from the start of the run, moved back whole windows for
            # the prior-period query.
            end = datetime.fromisoformat(body["endTime"].replace("Z", "+00:00"))
            start = datetime.fromisoformat(body["startTime"].replace("Z", "+00:00"))
            window = end - start
            anchor = now - round((now - end) / window) * window
            traffic_points = [
                {"timestamp": stamp(anchor, timedelta(minutes=minute)), "value": rate}
                for minute in (50, 30, 10)
            ]
            series = [
                {"resourceId": rid, "points": traffic_points}
                for rid in body.get("resourceIds")
                or [resource, retired, UNKNOWN_RESOURCE]
            ]
        route.fulfill(json={"series": series})

    context.route(
        "**/loco.observability.v1.ObservabilityService/QueryMetrics", query_metrics
    )


def assert_sidebar_slide(target):
    target.hover()
    highlight_selector = '[data-slot="sidebar-highlight"]'
    traveled = target.evaluate(
        """async (node, selector) => {
        const sidebar = node.closest('[data-slot="sidebar-content"]');
        const row = node.closest('li');
        const center = rect => rect.top + rect.height / 2;
        const start = center(sidebar.querySelector(selector).getBoundingClientRect());
        const end = center(node.getBoundingClientRect());
        const direction = Math.sign(end - start);
        const deadline = performance.now() + 1000;
        node.click();
        while (performance.now() < deadline) {
            await new Promise(requestAnimationFrame);
            const highlight = row.querySelector(selector);
            if (!highlight) continue;
            const rect = highlight.getBoundingClientRect();
            const y = center(rect);
            if (Math.abs(y - end) <= 2) return false;
            if ((y - start) * direction <= 2) continue;
            // The trailing edge should remain visible outside the destination row.
            const trailingEdge = y - direction * (rect.height / 2 - 1);
            highlight.style.pointerEvents = 'auto';
            const visible = document.elementsFromPoint(rect.left + rect.width / 2, trailingEdge)
                .includes(highlight);
            highlight.style.removeProperty('pointer-events');
            if (visible) return true;
        }
        return false;
    }""",
        highlight_selector,
    )
    assert traveled, (
        f"Sidebar highlight did not visibly travel to {target.inner_text()}"
    )
    expect(target).to_have_attribute("aria-current", "page")
    expect(target.locator("..").locator(highlight_selector)).to_have_css(
        "transform", "none"
    )


def shell_navigation(page):
    page.emulate_media(reduced_motion="no-preference")
    page.set_viewport_size(evidence.DESKTOP)
    page.goto(state["ui_url"] + base + "?env=production")
    activity = page.get_by_role(
        "button", name="Workspace activity, 0 active", exact=True
    )
    activity.click()
    drawer = page.get_by_role("dialog", name="Workspace activity", exact=True)
    expect(drawer).to_contain_text("Nothing needs attention.")
    events = drawer.get_by_role("link", name="All events", exact=True)
    expect(events).to_be_visible()
    drawer.get_by_role("button", name="Close activity", exact=True).click()
    expect(drawer).not_to_be_visible()
    expect(activity).to_be_focused()
    sidebar = page.locator('[data-slot="sidebar-content"]')
    resources = sidebar.get_by_role("link", name="Resources", exact=True)
    dashboard = sidebar.get_by_role("link", name="Dashboard", exact=True)
    assert_sidebar_slide(resources)
    assert_sidebar_slide(dashboard)
    page.emulate_media(reduced_motion="reduce")
    resources.press("Enter")
    expect(resources).to_have_attribute("aria-current", "page")
    expect(sidebar.locator('[data-slot="sidebar-highlight"]')).to_have_css(
        "transform", "none"
    )
    activity.hover()
    activity.click()
    page.keyboard.press("Escape")
    page.emulate_media(reduced_motion="no-preference")


def mobile_sidebar(page):
    page.set_viewport_size(evidence.NARROW)
    page.goto(state["ui_url"] + base + "?env=production")
    sheet = page.get_by_role("dialog", name="Sidebar", exact=True)
    page.locator('[data-slot="top-bar"]').get_by_role(
        "button", name="Open sidebar"
    ).click()
    expect(sheet).to_be_visible()
    sheet.get_by_role("link", name="Resources", exact=True).click()
    expect(page).to_have_url(re.compile(r"/resources"))
    expect(sheet).to_have_count(0)
    page.set_viewport_size(evidence.DESKTOP)


def activity_deployments(page):
    now = datetime.now(UTC)

    def deployment(id, status, minutes, replicas=(), **extra):
        return {
            "id": id,
            "resourceId": resource,
            "status": f"DEPLOYMENT_STATUS_{status}",
            "placements": [
                {
                    "region": "us-east-1",
                    "status": f"PLACEMENT_STATUS_{status}",
                    "desiredReplicas": 1,
                    "replicas": list(replicas),
                }
            ],
            "createdAt": stamp(now, timedelta(minutes=minutes)),
            **extra,
        }

    finished = now - timedelta(minutes=7)
    deployments = [
        deployment(
            "00000000-0000-7000-8000-000000000001",
            "RUNNING",
            8,
            createdAt=(finished - timedelta(minutes=1))
            .isoformat()
            .replace("+00:00", "Z"),
            startedAt=(finished - timedelta(minutes=1, seconds=-16))
            .isoformat()
            .replace("+00:00", "Z"),
            completedAt=finished.isoformat().replace("+00:00", "Z"),
        ),
        deployment("00000000-0000-7000-8000-000000000002", "DEPLOYING", 1),
    ]
    page.route(
        "**/loco.deployment.v1.DeploymentService/ListDeployments",
        lambda route: route.fulfill(json={"deployments": deployments}),
    )
    production = sql(
        state,
        f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND name='production'",
    )

    def report_ready(route):
        response = route.fetch()
        body = response.json()
        for listed in body["resources"]:
            if listed["id"] == resource:
                listed["environmentStatuses"] = [
                    {
                        "environmentId": production,
                        "environmentName": "production",
                        "status": "RESOURCE_STATUS_HEALTHY",
                        "regions": [
                            {
                                "region": "us-east-1",
                                "status": "RESOURCE_STATUS_HEALTHY",
                                "readyReplicas": 1,
                                "desiredReplicas": 1,
                                "deploymentId": deployments[0]["id"],
                            }
                        ],
                    }
                ]
        route.fulfill(response=response, json=body)

    page.route(
        "**/loco.resource.v1.ResourceService/ListWorkspaceResources", report_ready
    )
    page.goto(state["ui_url"] + base + "?env=production")
    activity = page.get_by_role(
        "button", name="Workspace activity, 0 active", exact=True
    )
    activity.click()
    drawer = page.get_by_role("dialog", name="Workspace activity", exact=True)
    cards = drawer.get_by_role("listitem")
    expect(cards).to_have_count(2)
    expect(cards.nth(0)).to_contain_text("Deploying")
    expect(cards.nth(0)).to_contain_text("Deploy replicas")
    expect(cards.nth(1)).to_contain_text("Deployed")
    expect(cards.nth(1)).to_contain_text("1 of 1 ready")
    expect(cards.nth(1)).to_contain_text(re.compile(r"took 16s · 7m ago"))
    expect(
        cards.nth(0).get_by_role("link", name="Open controls-pilot", exact=True)
    ).to_be_visible()
    page.keyboard.press("Escape")
    page.unroute("**/loco.deployment.v1.DeploymentService/ListDeployments")
    page.unroute("**/loco.resource.v1.ResourceService/ListWorkspaceResources")


def log_levels(page):
    page.goto(
        state["ui_url"]
        + base
        + "/observability?env=production&view=logs&q="
        + urllib.parse.quote("@level:warn")
    )
    levels = page.get_by_role("group", name="Log levels")
    warn = levels.get_by_role("button", name=re.compile(r"^Warn"))
    expect(warn).to_contain_text("2")
    page.get_by_role("button", name=re.compile(r"Request completed \(2\)$")).click()
    expect(page.get_by_role("dialog")).to_contain_text("Warning")
    page.keyboard.press("Escape")
    error = levels.get_by_role("button", name=re.compile(r"^Error"))
    expect(warn).to_have_attribute("aria-pressed", "true")
    error.click()
    expect(error).to_have_attribute("aria-pressed", "true")
    wait_for_query(page, lambda query: query.get("q") == ["@level:warn @level:error"])
    warn.focus()
    page.keyboard.press("Space")
    expect(warn).to_have_attribute("aria-pressed", "false")
    wait_for_query(page, lambda query: query.get("q") == ["@level:error"])


def log_columns(page):
    attribute = "container.image.name"
    entries = [
        {
            "timestamp": stamp(datetime.now(UTC), timedelta(seconds=i)),
            "level": "LOG_LEVEL_WARN",
            "body": "Slow request",
            "resourceId": resource,
            "logAttributes": {attribute: "registry.example/app:1"},
        }
        for i in range(3)
    ]
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogs",
        lambda route: route.fulfill(json={"entries": entries, "totalMatched": "3"}),
    )
    page.goto(
        state["ui_url"]
        + base
        + f"/observability?env=production&view=logs&cols=level,resource,{attribute}"
    )
    lines = page.get_by_role("region", name="Log lines")
    row = lines.get_by_role("button", name=re.compile(r"Slow request$")).first
    expect(row).to_be_visible()
    head = page.locator(".list-head").first
    for label in ("Time", "Level", "Resource", attribute):
        expect(head.get_by_text(label, exact=True)).to_be_visible()
    clipped = page.evaluate(
        """() => [...document.querySelectorAll('.list-head span, .list-row > span')]
            .filter(e => e.scrollWidth > e.clientWidth + 1 && e.querySelector('.truncate') === null
                && /^(Time|Level|Warning|[\\d:.]+|container\\.image\\.name)$/.test(e.textContent.trim()))
            .map(e => e.textContent.trim())"""
    )
    assert clipped == [], clipped
    page.evaluate("localStorage.setItem('loco-clock', '12')")
    page.reload()
    time_cell = (
        lines.get_by_role("button", name=re.compile(r"Slow request$"))
        .first.locator("span")
        .first
    )
    expect(time_cell).to_have_text(re.compile(r"^\d+:\d\d:\d\d\.\d{3} [AP]M$"))
    assert time_cell.evaluate("e => e.scrollWidth <= e.clientWidth"), (
        "12-hour time clipped"
    )
    before = time_cell.evaluate("e => e.clientWidth")
    page.get_by_role("separator", name="Resize column Time").focus()
    page.keyboard.press("ArrowRight")
    expect(page).to_have_url(re.compile(r"widths=time:\d+"))
    assert time_cell.evaluate("e => e.clientWidth") > before
    page.evaluate("localStorage.removeItem('loco-clock')")
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogs")


def workload_logs(page):
    now = datetime.now(UTC)
    entries = [
        {
            "timestamp": stamp(now, timedelta(seconds=5)),
            "level": "LOG_LEVEL_INFO",
            "body": "Checkout request served",
            "resourceId": resource,
            "resourceAttributes": {"k8s.pod.name": "controls-pilot-7d9f-x2k4p"},
        },
        {
            "timestamp": stamp(now, timedelta(seconds=9)),
            "level": "LOG_LEVEL_ERROR",
            "body": "Broker connection lost",
            "resourceAttributes": {
                "k8s.statefulset.name": "kafka",
                "k8s.pod.name": "kafka-0",
            },
        },
    ]
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogs",
        lambda route: route.fulfill(json={"entries": entries, "totalMatched": "2"}),
    )
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    lines = page.get_by_role("region", name="Log lines")
    workload_row = lines.get_by_role(
        "button", name=re.compile(r"Broker connection lost$")
    )
    expect(workload_row).to_contain_text("kafka")
    expect(lines).not_to_contain_text("unknown")
    expect(
        workload_row.get_by_title("Kubernetes workload, not a Loco resource")
    ).to_be_visible()
    search = page.locator('input[aria-label="Search and filter"]')
    search.fill("@resource:")
    expect(
        page.get_by_role("option", name=re.compile(r"^@resource:controls-pilot"))
    ).to_be_visible()
    expect(page.get_by_role("option", name=re.compile(r"kafka"))).to_have_count(0)
    search.press("Escape")
    workload_row.click()
    detail = page.get_by_role("dialog")
    expect(
        detail.get_by_title("Kubernetes workload, not a Loco resource")
    ).to_be_visible()
    expect(detail.get_by_text("@resource", exact=True)).to_have_count(0)
    expect(detail.get_by_text("@k8s.statefulset.name", exact=True)).to_be_visible()
    detail.get_by_role("button", name="More for this line", exact=True).click()
    expect(
        page.get_by_role("menuitem", name=re.compile(r"^Logs around this line"))
    ).to_be_visible()
    expect(
        page.get_by_role("menuitem", name=re.compile(r"^Metrics around"))
    ).to_have_count(0)
    expect(page.get_by_role("menuitem", name=re.compile(r"^View "))).to_have_count(0)
    page.keyboard.press("Escape")
    page.keyboard.press("Escape")
    expect(detail).to_have_count(0)
    lines.get_by_role("button", name=re.compile(r"Checkout request served$")).click()
    expect(detail.get_by_text("@resource", exact=True)).to_be_visible()
    detail.get_by_role("button", name="More for this line", exact=True).click()
    expect(
        page.get_by_role("menuitem", name=re.compile(r"^Metrics around"))
    ).to_be_visible()
    page.keyboard.press("Escape")
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogs")


def template_logs(page):
    template = "GetCartAsync called with userId={userId} in {region}"
    entries = [
        {
            "timestamp": stamp(datetime.now(UTC), timedelta(seconds=5)),
            "level": "LOG_LEVEL_INFO",
            "body": template,
            "resourceId": resource,
            "logAttributes": {"userId": "u-42"},
        }
    ]
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogs",
        lambda route: route.fulfill(json={"entries": entries, "totalMatched": "1"}),
    )
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    rendered = "GetCartAsync called with userId=u-42 in {region}"
    row = page.get_by_role("region", name="Log lines").get_by_role(
        "button", name=re.compile(re.escape(rendered) + "$")
    )
    row.click()
    detail = page.get_by_role("dialog")
    expect(detail.get_by_text(rendered, exact=True)).to_be_visible()
    expect(detail.get_by_text(template, exact=True)).to_be_visible()
    page.keyboard.press("Escape")
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogs")


def log_drawer(page):
    entries = [
        {
            "timestamp": stamp(datetime.now(UTC), timedelta(seconds=5)),
            "level": "LOG_LEVEL_INFO",
            "body": "Drawer line",
            "resourceId": resource,
            "resourceAttributes": {
                "container.image.name": "registry.example/app",
                "k8s.cluster.name": "staging",
            },
        }
    ]
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogs",
        lambda route: route.fulfill(json={"entries": entries, "totalMatched": "1"}),
    )
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    row = page.get_by_role("region", name="Log lines").get_by_role(
        "button", name=re.compile(r"Drawer line$")
    )
    row.click()
    detail = page.get_by_role("dialog")
    expect(detail).to_be_visible()
    page.wait_for_timeout(500)
    for key in ("@container.image.name", "@k8s.cluster.name"):
        label = detail.get_by_text(key, exact=True)
        expect(label).to_be_visible()
        fits = label.evaluate("el => el.scrollWidth <= el.clientWidth")
        assert fits, f"{key} is truncated"
        assert label.bounding_box()["width"] > 96
    actions = detail.get_by_role("button", name=re.compile(r"^(Only|Hide|Add) "))
    assert actions.count() >= 3
    for i in range(actions.count()):
        box = actions.nth(i).bounding_box()
        assert box["width"] <= 24 and box["height"] <= 24, box
    before = detail.bounding_box()["width"]
    handle = detail.get_by_role("separator", name="Resize drawer")
    box = handle.bounding_box()
    x, y = box["x"] + box["width"] / 2, box["y"] + box["height"] / 2
    page.mouse.move(x, y)
    page.mouse.down()
    page.mouse.move(x - 120, y, steps=5)
    page.mouse.up()
    wide = detail.bounding_box()["width"]
    assert wide > before + 100, (before, wide)
    view = detail.get_by_role("group", name="Detail view")
    expect(view.get_by_role("button", name="Attributes")).to_have_attribute(
        "aria-pressed", "true"
    )
    view.get_by_role("button", name="JSON").click()
    expect(detail.get_by_text("@k8s.cluster.name", exact=True)).to_have_count(0)
    expect(detail.get_by_role("textbox", name="Filter attributes")).to_have_count(0)
    expect(detail.get_by_text("registry.example/app").first).to_be_visible()
    page.keyboard.press("Escape")
    expect(detail).to_have_count(0)
    row.click()
    expect(detail).to_be_visible()
    page.wait_for_timeout(500)
    assert abs(detail.bounding_box()["width"] - wide) <= 1
    expect(detail.get_by_text("@k8s.cluster.name", exact=True)).to_have_count(0)
    view.get_by_role("button", name="Attributes").click()
    expect(detail.get_by_text("@k8s.cluster.name", exact=True)).to_be_visible()
    handle.focus()
    page.keyboard.press("ArrowRight")
    assert detail.bounding_box()["width"] < wide
    handle.dblclick()
    page.wait_for_timeout(100)
    assert abs(detail.bounding_box()["width"] - before) <= 1
    page.keyboard.press("Escape")
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogs")


def live_focus(page):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    expect(page.get_by_text("Request completed (1)", exact=True)).to_be_visible()
    live = page.get_by_role("button", name=re.compile(r"^(Live|Starting…)"))
    expect(live).to_have_attribute("aria-pressed", "true")
    expect(page).not_to_have_url(re.compile(r"live="))
    live.focus()
    page.keyboard.press("Enter")
    expect(live).to_have_attribute("aria-pressed", "false")
    wait_for_query(page, lambda query: query.get("live") == ["0"])
    page.reload()
    expect(live).to_have_attribute("aria-pressed", "false")
    # Resuming keeps the volume chart while its request is in flight.
    slider = page.get_by_role("slider", name=re.compile(r"^Log volume"))
    expect(slider).to_be_visible()
    buckets = []
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogBuckets",
        lambda route: buckets.append(route),
    )
    # Hold the latest-logs request so the button stays in its pending state.
    held = []
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogs",
        lambda route: held.append(route),
    )
    live.focus()
    page.keyboard.press("Enter")
    expect(live).to_have_text("Starting…")
    expect(live).to_have_attribute("aria-busy", "true")
    page.keyboard.press("Enter")
    # Chromium moves focus off a disabled button on its next rendering update, not at once.
    page.evaluate("() => new Promise(requestAnimationFrame)")
    expect(live).to_be_focused()
    assert len(held) == 1, len(held)
    # A held route cannot fall back to the context mock; answer it here.
    held[0].fulfill(json={"entries": [], "totalMatched": "0"})
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogs")
    expect(live).to_have_attribute("aria-pressed", "true")
    expect(live).to_be_focused()
    assert buckets
    expect(slider).to_be_visible()
    for route in buckets:
        route.fulfill(json={"buckets": []})
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogBuckets")
    wait_for_query(page, lambda query: "live" not in query)
    page.keyboard.press("Enter")
    expect(live).to_have_attribute("aria-pressed", "false")


def log_paging(page):
    now = datetime.now(UTC)
    many = [
        {
            "timestamp": stamp(now, timedelta(seconds=i * 7 + 1)),
            "level": "LOG_LEVEL_INFO",
            "body": f"Paged line {i + 1}",
            "resourceId": resource,
            "resourceAttributes": {"cloud.region": "us-east-1"},
            "logAttributes": {"http.request.method": "GET"},
        }
        for i in range(120)
    ]
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogs",
        lambda route: route.fulfill(json={"entries": many, "totalMatched": "0"}),
    )
    page.goto(
        state["ui_url"]
        + base
        + "/observability?env=production&view=logs&live=0&size=25"
    )
    pager_label = page.get_by_text(re.compile(r"of 120 loaded$"))
    expect(pager_label).to_have_text("1–25 of 120 loaded")
    expect(page.get_by_role("button", name="Newest", exact=True)).to_be_disabled()
    page.get_by_role("button", name="Oldest", exact=True).click()
    expect(pager_label).to_have_text("101–120 of 120 loaded")
    wait_for_query(page, lambda query: query.get("page") == ["last"])
    expect(page.get_by_role("button", name="Oldest", exact=True)).to_be_disabled()
    page.get_by_role("button", name="Newest", exact=True).click()
    expect(pager_label).to_have_text("1–25 of 120 loaded")
    page.get_by_role("button", name="Older", exact=True).click()
    expect(pager_label).to_have_text("26–50 of 120 loaded")
    page.get_by_role("group", name="Rows per page").get_by_role(
        "button", name="250", exact=True
    ).click()
    expect(pager_label).to_have_text("1–120 of 120 loaded")
    wait_for_query(page, lambda query: query.get("size") == ["250"])
    page.goto(
        state["ui_url"]
        + base
        + "/observability?env=production&view=logs&live=0&size=25&page=2"
    )
    expect(pager_label).to_have_text("26–50 of 120 loaded")
    page.get_by_role("button", name=re.compile(r"Paged line 30$")).click()
    wait_for_query(page, lambda query: "log" in query)
    page.reload()
    detail = page.get_by_role("dialog")
    expect(detail).to_contain_text("Paged line 30")
    page.keyboard.press("ArrowDown")
    expect(detail).to_contain_text("Paged line 31")
    page.keyboard.press("ArrowUp")
    expect(detail).to_contain_text("Paged line 30")
    expect(detail).to_contain_text("@resource")
    detail.get_by_role("button", name="More for this line", exact=True).click()
    expect(
        page.get_by_role("menuitem", name=re.compile(r"^View controls-pilot"))
    ).to_be_visible()
    expect(page.get_by_role("menuitem", name=re.compile(r"^View trace"))).to_have_count(
        0
    )
    page.keyboard.press("Escape")
    expect(page.get_by_role("menu")).to_have_count(0)
    expect(detail).to_be_visible()
    page.keyboard.press("Escape")
    expect(detail).to_have_count(0)
    wait_for_query(page, lambda query: "log" not in query)
    page.context.grant_permissions(["clipboard-read", "clipboard-write"])
    row = page.get_by_role("button", name=re.compile(r"Paged line 30$"))
    menu = row.locator("xpath=following-sibling::div").get_by_role(
        "button", name="More for this line", exact=True
    )
    row.hover()
    menu.click()
    page.get_by_role("menuitem", name="Copy message", exact=True).click()
    assert page.evaluate("navigator.clipboard.readText()") == "Paged line 30"
    row.hover()
    menu.click()
    page.get_by_role("menuitem", name="Copy as JSON", exact=True).click()
    copied = json.loads(page.evaluate("navigator.clipboard.readText()"))
    assert copied["message"] == "Paged line 30"
    assert re.search(r"(Z|[+-]\d\d:\d\d)$", copied["timestamp"]), copied
    assert "trace_id" not in copied and "span_id" not in copied, copied
    page.get_by_role("button", name="Add a column", exact=True).click()
    page.get_by_role("menuitemcheckbox", name="Method", exact=True).click()
    page.keyboard.press("Escape")
    wait_for_query(page, lambda query: "method" in query.get("cols", [""])[0])
    expect(
        page.get_by_role("button", name=re.compile(r"GET Paged line 30$"))
    ).to_have_count(1)
    page.get_by_role("separator", name="Resize column Resource").focus()
    page.keyboard.press("ArrowRight")
    expect(page).to_have_url(re.compile(r"widths=resource:156"))
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogs")


def log_cap(page):
    now = datetime.now(UTC)
    rows = [
        {
            "timestamp": stamp(now, timedelta(seconds=i * 7 + 1)),
            "level": "LOG_LEVEL_INFO",
            "body": f"Capped line {i + 1}",
            "resourceId": resource,
        }
        for i in range(30)
    ]
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogs",
        lambda route: route.fulfill(
            json={"entries": rows, "totalMatched": "0", "nextCursor": "more"}
        ),
    )
    page.goto(
        state["ui_url"] + base + "/observability?env=production&view=logs&size=25"
    )
    expect(page.get_by_text(re.compile(r"^1–25 of 30\+ loaded"))).to_contain_text(
        "10,014 lines in range, 1,000 per load, Older loads more"
    )
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogs")


def time_presets(page):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    page.get_by_role("button", name=re.compile(r"^Past ")).click()
    time_range = page.get_by_role("dialog", name="Time range")
    active = time_range.get_by_role("group", name="Presets").locator(
        '[aria-pressed="true"]'
    )
    expect(active).to_have_count(1)
    active.click()
    expect(time_range).not_to_be_visible()


def time_preferences(browser):
    context, page, errors = open_page(
        browser,
        state=state,
        timezone_id="America/New_York",
        storage={"loco-clock": "12"},
    )
    mock_telemetry(context)
    start = int(datetime(2026, 11, 1, 6, 30, 12, 123000, tzinfo=UTC).timestamp() * 1000)
    end = int(datetime(2026, 11, 1, 6, 45, 23, 456000, tzinfo=UTC).timestamp() * 1000)
    page.goto(
        state["ui_url"] + base + f"/observability?view=logs&from={start}&to={end}"
    )
    page.get_by_role("button", name=re.compile(r" (AM|PM).* (EST|EDT)")).click()
    picker = page.get_by_role("dialog", name="Time range")
    start_input = picker.get_by_role("textbox", name="From time", exact=True)
    expect(start_input).to_have_value(re.compile(r"\d+:\d{2} (AM|PM)"))
    picker.get_by_role("group", name="Time zone", exact=True).get_by_role(
        "button", name="UTC", exact=True
    ).click()
    expect(start_input).to_have_value("6:30 AM")
    clock = picker.get_by_role("group", name="Clock", exact=True)
    clock.get_by_role("button", name="24-hour", exact=True).click()
    expect(start_input).to_have_value("06:30")
    clock.get_by_role("button", name="12-hour", exact=True).click()
    expect(start_input).to_have_value("6:30 AM")
    assert page.evaluate("localStorage.getItem('loco-clock')") == "12"
    zone = picker.get_by_role("group", name="Time zone", exact=True)
    zone.get_by_role("button", name=re.compile(r"^Local")).click()
    expect(start_input).to_have_value("1:30 AM")
    zone.get_by_role("button", name="UTC", exact=True).click()
    picker.get_by_role("button", name="Apply", exact=True).click()
    wait_for_query(
        page,
        lambda query: (
            query.get("from") == [str(start)] and query.get("to") == [str(end)]
        ),
    )
    assert page.evaluate("localStorage.getItem('loco-tz')") == "utc"
    assert not errors, errors
    context.close()


def calendar(page):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    page.get_by_role("button", name=re.compile(r"^Past ")).click()
    time_range = page.get_by_role("dialog", name="Time range")
    date_trigger = time_range.get_by_role("button", name="From date")
    date_trigger.press("Space")
    popup = page.get_by_role("dialog", name="From calendar")
    expect(popup.get_by_role("grid")).to_be_visible()
    page.keyboard.press("Escape")
    expect(popup).not_to_be_visible()
    expect(date_trigger).to_be_focused()
    expect(time_range).to_be_visible()
    before = date_trigger.inner_text()
    date_trigger.click()
    popup.get_by_role("grid").get_by_role(
        "button", name=re.compile(r"selected", re.I)
    ).focus()
    page.keyboard.press("ArrowLeft")
    page.keyboard.press("Enter")
    expect(popup).not_to_be_visible()
    expect(time_range).to_be_visible()
    expect(date_trigger).not_to_have_text(before)


def custom_range_future_end(page):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    page.get_by_role("button", name=re.compile(r"^Past ")).click()
    time_range = page.get_by_role("dialog", name="Time range")
    end_date = time_range.get_by_role("button", name="To date")
    end_date.click()
    popup = page.get_by_role("dialog", name="To calendar")
    popup.get_by_role("grid").get_by_role(
        "button", name=re.compile(r"selected", re.I)
    ).focus()
    page.keyboard.press("ArrowRight")
    page.keyboard.press("ArrowRight")
    page.keyboard.press("Enter")
    expect(popup).not_to_be_visible()
    time_range.get_by_role("button", name="Apply", exact=True).click()
    expect(time_range.get_by_role("alert")).to_have_text(
        "The end can't be in the future."
    )
    expect(page).not_to_have_url(re.compile(r"[?&]to="))


def query_enter(page):
    page.goto(
        state["ui_url"]
        + base
        + "/observability?env=production&view=logs&q="
        + urllib.parse.quote("@level:warn")
    )
    search = page.locator('input[aria-label="Search and filter"]')
    search.click()
    suggestions = page.get_by_role("listbox", name="Suggestions")
    expect(suggestions).to_be_visible()
    search.press("Enter")
    expect(suggestions).not_to_be_visible()
    expect(search).to_have_value("")
    wait_for_query(page, lambda query: query.get("q") == ["@level:warn"])
    search.press_sequentially("checkout ")
    expect(suggestions).to_be_visible()
    search.press("Enter")
    expect(suggestions).not_to_be_visible()
    expect(page).to_have_url(re.compile(r"checkout"))
    wait_for_query(page, lambda query: query.get("q") == ["@level:warn checkout"])


def query_suggestions(page):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    search = page.locator('input[aria-label="Search and filter"]')
    search.fill("@lev")
    expect(page.get_by_role("option", name=re.compile(r"^@level"))).to_be_visible()
    expect(search).to_have_attribute("aria-expanded", "true")
    search.press("Enter")
    expect(search).to_have_value("@level:")
    search.fill("@level:warn")
    search.press("Tab")
    expect(search).to_have_value("")
    search.fill("@resource:")
    resource_option = page.get_by_role(
        "option", name=re.compile(r"^@resource:controls-pilot")
    )
    expect(resource_option).to_contain_text("647")
    expect(
        page.get_by_role("option", name=re.compile(r"^@resource:controls-alpha"))
    ).to_be_visible()
    expect(page.get_by_text("@resource values", exact=True)).to_be_visible()
    search.fill("@region:us")
    expect(
        page.get_by_text("@region values · counts from loaded lines")
    ).to_be_visible()
    search.fill("@zz")
    expect(page.get_by_text("No matches", exact=True)).to_be_visible()
    search.press("Escape")
    expect(search).to_have_attribute("aria-expanded", "false")


def volume(page):
    end = int(datetime.now(UTC).timestamp() * 1000)
    info = page.get_by_role("group", name="Log levels").get_by_role(
        "button", name=re.compile(r"^Info")
    )

    def open_hour():
        with expect_rpc(
            page,
            "QueryLogBuckets",
            lambda body: body.get("groupBy") == "LOG_BUCKET_GROUP_LEVEL",
        ):
            page.goto(
                state["ui_url"]
                + base
                + f"/observability?env=production&view=logs&from={end - 3_600_000}&to={end}"
            )
        expect(info).to_contain_text("10,005")

    slider = page.get_by_role("slider", name=re.compile(r"^Log volume"))
    lines = page.get_by_role("region", name="Log lines")
    held = []
    for viewport in (evidence.DESKTOP, evidence.NARROW):
        page.set_viewport_size(viewport)
        held.clear()
        page.route(
            "**/loco.observability.v1.ObservabilityService/QueryLogBuckets",
            lambda route: held.append(route),
        )
        page.goto(
            state["ui_url"]
            + base
            + f"/observability?env=production&view=logs&from={end - 3_600_000}&to={end}"
        )
        expect(page.get_by_text("Request completed (1)", exact=True)).to_be_visible()
        expect(info).to_contain_text("—")
        assert held
        loading = lines.bounding_box()["y"]
        # A held route cannot fall back to the context mock; answer it here.
        for route in held:
            route.fulfill(json={"buckets": []})
        page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogBuckets")
        expect(slider).to_be_visible()
        assert lines.bounding_box()["y"] == loading, viewport
    page.set_viewport_size(evidence.DESKTOP)

    open_hour()
    expect(slider).to_be_visible()
    expect(page.get_by_text("Request completed (1)", exact=True)).to_be_visible()
    area = (
        page.get_by_role("region", name="Log volume")
        .locator('[data-slot="slider"]')
        .locator("..")
    )
    bars = area.locator("> div").first.locator("> div")
    width = math.ceil(3_600 / 59) * 1000
    count = end // width - (end - 3_600_000) // width + 1
    expect(bars).to_have_count(count)
    assert (
        bars.first.locator("span")
        .nth(1)
        .evaluate("node => getComputedStyle(node).boxShadow")
        != "none"
    )
    box = area.bounding_box()
    target = count - 5
    point = {"x": box["width"] * (target + 0.5) / count, "y": box["height"] / 2}
    area.hover(position=point)
    expect(area).to_have_attribute("title", re.compile(r"lines"))
    assert log_bucket_requests[-1]["groupBy"] == "LOG_BUCKET_GROUP_LEVEL"
    area.click(position=point)
    expect(page).not_to_have_url(re.compile(f"from={end - 3_600_000}"))
    clicked = urllib.parse.parse_qs(urllib.parse.urlparse(page.url).query)["from"][0]
    page.mouse.move(0, 0)
    open_hour()
    slider.focus()
    page.keyboard.press("ArrowRight")
    expect(slider).to_have_attribute("aria-valuenow", "0")
    page.keyboard.press("End")
    expect(slider).to_have_attribute("aria-valuenow", str(count - 1))
    expect(slider).to_have_attribute("max", str(count - 1))
    page.keyboard.press("Home")
    for _ in range(target):
        page.keyboard.press("ArrowRight")
    expect(slider).to_have_attribute("aria-valuenow", str(target))
    page.keyboard.press("Enter")
    expect(page).not_to_have_url(re.compile(f"from={end - 3_600_000}"))
    assert (
        urllib.parse.parse_qs(urllib.parse.urlparse(page.url).query)["from"][0]
        == clicked
    )


def volume_axis(page):
    end = int(datetime.now(UTC).timestamp() * 1000)
    chart = page.get_by_role("region", name="Log volume")
    labels = chart.get_by_text(re.compile(r"^\d{1,2}:\d{2}$")).filter(visible=True)
    for viewport in (evidence.NARROW, evidence.DESKTOP):
        page.set_viewport_size(viewport)
        page.goto(
            state["ui_url"]
            + base
            + f"/observability?env=production&view=logs&from={end - 900_000}&to={end}"
        )
        expect(
            page.get_by_role("slider", name=re.compile(r"^Log volume"))
        ).to_be_visible()
        expect(labels.first).to_be_visible()
        boxes = sorted(
            (box["x"], box["x"] + box["width"])
            for box in (label.bounding_box() for label in labels.all())
        )
        assert len(boxes) >= 2, viewport
        assert all(left[1] < right[0] for left, right in itertools.pairwise(boxes)), (
            viewport,
            labels.all_inner_texts(),
        )


def dashboard(page):
    page.set_viewport_size(evidence.DESKTOP)
    if not state.get("dashboard_resources"):
        cluster = evidence.seed_cluster(state, "us-east-1", name="dashboard-pilot")
        evidence.seed_resources(
            state,
            [
                {
                    "workspace": "acme/shared",
                    "name": f"dashboard-{index}",
                    "service": service(),
                    "deployments": [
                        {
                            "environment": "production",
                            "placements": {"us-east-1": {"cluster": cluster}},
                            "method": "cli",
                            "spec": {
                                "platform": "linux/amd64",
                                "build": {
                                    "type": "image",
                                    "image": "example.invalid/dashboard:1",
                                },
                                "port": 8080,
                                "regions": {
                                    "us-east-1": {
                                        "cpu": f"{(index + 1) * 100}m",
                                        "memory": "128Mi",
                                        "minReplicas": 1,
                                        "maxReplicas": 1,
                                    }
                                },
                                "observability": {"injectOtel": True},
                            },
                        }
                    ],
                }
                for index in range(7)
            ],
        )
        state["dashboard_resources"] = True
        evidence.write_json(Path(args.state), state)
    sql(
        state,
        f"UPDATE deployment_placements SET replicas=CASE WHEN resources.name='dashboard-0' THEN 48 ELSE 12 END FROM deployments, resources WHERE deployment_placements.deployment_id=deployments.id AND deployments.resource_id=resources.id AND resources.workspace_id='{workspace}' AND resources.name IN ('dashboard-0','dashboard-1'); UPDATE placements SET ready_replicas=CASE WHEN resources.name='dashboard-0' THEN 47 ELSE 12 END FROM resources WHERE placements.resource_id=resources.id AND resources.workspace_id='{workspace}' AND resources.name IN ('dashboard-0','dashboard-1'); INSERT INTO resource_domains(resource_id, environment_id, domain, domain_source, subdomain_label, platform_domain, is_primary) SELECT resources.id, environments.id, 'dashboard-0.apps.example.test', 'platform_provided', 'dashboard-0', 'apps.example.test', true FROM resources, environments WHERE resources.workspace_id='{workspace}' AND resources.name='dashboard-0' AND environments.workspace_id='{workspace}' AND environments.name='production' AND NOT EXISTS (SELECT 1 FROM resource_domains WHERE resource_id=resources.id);",
    )
    for color, mode in (
        (color, mode)
        for color in (
            "loco",
            "catppuccin",
            "rose-pine",
            "solarized",
            "paper",
            "neobrutal",
        )
        for mode in ("light", "dark")
    ):
        page.evaluate("color => localStorage.setItem('loco:color:v1', color)", color)
        page.goto(state["ui_url"] + base + "?env=production")
        replicas = page.get_by_label(
            "47 of 48 ready · 1 starting\nus-east-1: 47/48", exact=True
        )
        expect(replicas).to_be_visible()
        # The authentication init script starts every navigation in light mode.
        if mode == "dark":
            page.get_by_role("button").filter(has_text="Olivia Owner").click()
            page.get_by_role("radiogroup", name="Mode").get_by_role(
                "radio", name="Dark", exact=True
            ).click()
            page.keyboard.press("Escape")
            expect(page.locator("html")).to_have_class(re.compile(r"\bdark\b"))
        else:
            expect(page.locator("html")).not_to_have_class(re.compile(r"\bdark\b"))
        expect(replicas).to_contain_text("47/48")
        full_row = (
            page.get_by_role("region", name="Resources", exact=True)
            .get_by_role("row")
            .filter(has=page.get_by_role("link", name="dashboard-1", exact=True))
        )
        expect(full_row).to_contain_text("12/12")
        bounds = full_row.evaluate("""row => {
            const count = [...row.querySelectorAll('span')].find(n => n.children.length === 0 && n.textContent === '12/12');
            const cell = count.closest('td');
            return {countRight: count.getBoundingClientRect().right, cellRight: cell.getBoundingClientRect().right,
                trafficLeft: cell.nextElementSibling.getBoundingClientRect().left};
        }""")
        assert (
            bounds["countRight"] + 10 <= bounds["cellRight"] <= bounds["trafficLeft"]
        ), bounds
        replicas.focus()
        expect(page.locator('[data-slot="tooltip-content"]')).to_contain_text(
            "us-east-1: 47/48"
        )
        page.keyboard.press("Escape")
        table = page.get_by_role("region", name="Deployments", exact=True).get_by_role(
            "table"
        )
        for viewport in (evidence.DESKTOP, evidence.NARROW):
            page.set_viewport_size(viewport)
            bounds = table.evaluate("""table => {
                const scroller = table.parentElement;
                scroller.scrollLeft = window.innerWidth < 600 ? scroller.scrollWidth : 0;
                const cell = table.querySelector('tbody tr td:last-child');
                const range = document.createRange();
                range.selectNodeContents(cell);
                const text = range.getBoundingClientRect(), box = scroller.getBoundingClientRect();
                return {width: scroller.clientWidth, content: scroller.scrollWidth, scroll: scroller.scrollLeft,
                    left: text.left, right: text.right, edgeLeft: box.left, edgeRight: box.right};
            }""")
            if viewport == evidence.DESKTOP:
                assert bounds["content"] <= bounds["width"] + 1, (color, mode, bounds)
            else:
                assert bounds["scroll"] > 0, (color, mode, bounds)
            assert (
                bounds["edgeLeft"]
                <= bounds["left"]
                <= bounds["right"]
                <= bounds["edgeRight"] + 1
            ), (color, mode, bounds)
        page.set_viewport_size(evidence.DESKTOP)
        totals = page.get_by_role("region", name="Totals", exact=True)
        allocated = page.get_by_role("group", name=re.compile("^Allocated:"))
        expect(allocated).to_be_visible()
        cards = totals.locator(":scope > div")
        heights = cards.evaluate_all(
            "nodes => nodes.map(n => n.getBoundingClientRect().height)"
        )
        assert max(heights) - min(heights) <= 1, heights
        if color == "neobrutal":
            value = allocated.get_by_text(re.compile(r"^\d+(?:\.\d+)? vCPU$")).first
            geometry = value.evaluate(
                "node => ({height: node.getBoundingClientRect().height, line: parseFloat(getComputedStyle(node).lineHeight), width: node.clientWidth, content: node.scrollWidth})"
            )
            assert (
                geometry["height"] <= geometry["line"] + 1
                and geometry["content"] <= geometry["width"]
            ), geometry
            for label in ("Filter resources by status", "Filter deployments by status"):
                selected = page.get_by_label(label, exact=True).locator(
                    'button[aria-pressed="true"]'
                )
                count = selected.locator(":scope > span").last
                expect(count).to_have_css(
                    "color", selected.evaluate("node => getComputedStyle(node).color")
                )
        allocated.hover()
        strip = allocated.locator('[data-slot="allocation-strip"]')
        expect(strip.locator(":scope > span")).to_have_count(6)
        expect(strip.locator("[data-rest]")).to_have_count(1)
        bars = allocated.locator('[data-slot="allocation-bar"] > span')
        expect(bars).to_have_count(5)
        colors = strip.locator(":scope > span:not([data-rest])").evaluate_all(
            "nodes => nodes.map(n => getComputedStyle(n).backgroundColor)"
        )
        assert len(set(colors)) == 5, colors
        assert (
            bars.evaluate_all(
                "nodes => nodes.map(n => getComputedStyle(n).backgroundColor)"
            )
            == colors
        )
        page.get_by_role("button").filter(has_text="Olivia Owner").click()
        next_mode = "Dark" if mode == "light" else "System"
        action = page.get_by_role("radiogroup", name="Mode").get_by_role(
            "radio", name=next_mode, exact=True
        )
        action.click()
        expect(action).to_have_attribute("aria-checked", "true")
        assert page.evaluate("localStorage.getItem('loco:mode:v1')") == (
            "dark" if next_mode == "Dark" else None
        )
        page.keyboard.press("Escape")
    sql(
        state,
        f"UPDATE placements SET ready_replicas=48 FROM resources WHERE placements.resource_id=resources.id AND resources.workspace_id='{workspace}' AND resources.name='dashboard-0';",
    )
    page.reload()
    ready = page.get_by_label("48 of 48 ready\nus-east-1: 48/48", exact=True)
    expect(ready).to_be_visible()
    ready.focus()
    expect(page.locator('[data-slot="tooltip-content"]')).to_have_text(
        "48 of 48 ready us-east-1: 48/48"
    )
    page.keyboard.press("Escape")
    page.evaluate(
        "localStorage.setItem('loco:color:v1', 'loco'); localStorage.setItem('loco:mode:v1', 'light')"
    )


def dashboard_counts(page):
    page.goto(state["ui_url"] + base + "?env=production")
    section = page.get_by_role("region", name="Resources", exact=True)
    count = section.locator("h2 + span")
    everything = (
        page.get_by_label("Filter resources by status", exact=True)
        .get_by_role("button")
        .first
    )
    expect(section.get_by_role("link", name="dashboard-0", exact=True)).to_be_visible()
    total = int(count.inner_text())
    assert total >= 7, total
    expect(everything).to_have_text(re.compile(rf"^All\s*{total}$"))
    section.get_by_role("textbox", name="Search resources", exact=True).fill(
        "dashboard-1"
    )
    expect(count).to_have_text("1")
    expect(everything).to_have_text(re.compile(r"^All\s*1$"))
    expect(section.get_by_role("link", name="dashboard-1", exact=True)).to_be_visible()


def traffic(page):
    global traffic_empty
    totals = page.get_by_role("region", name="Totals", exact=True)
    section = page.get_by_role("region", name="Traffic", exact=True)
    for empty in (False, True):
        traffic_empty = empty
        page.goto(state["ui_url"] + base + "?env=production&range=1h")
        # 3 resources × 3 points × 10/s × 60 s buckets (1h range) = 5400 requests, 540 5xx.
        requests, errors = ("—", "—") if empty else ("5.4K", "540")
        for label, value in (("Public requests", requests), ("Public errors", errors)):
            card = totals.get_by_role("group", name=f"{label}: {value},")
            # The prior hour's stub equals this hour's.
            comparison = "No prior 1h data to compare" if empty else "0% vs prior 1h"
            expect(card.get_by_text(comparison, exact=True)).to_be_visible()
        if not empty:
            card = totals.get_by_role("group", name="Public requests: 5.4K,")
            card.hover()
            expect(
                card.get_by_role("link", name=re.compile("^controls-pilot"))
            ).to_be_visible()
            for name in ("controls-retired (deleted)", "Deleted resource"):
                expect(card.get_by_text(name, exact=True)).to_be_visible()
                expect(
                    card.get_by_role("link", name=re.compile(re.escape(name)))
                ).to_have_count(0)
        page.get_by_role("region", name="Resources", exact=True).get_by_role(
            "link", name="dashboard-0", exact=True
        ).click()
        if empty:
            expect(
                section.get_by_text("No data in this range", exact=True)
            ).to_have_count(4)
            expect(section.get_by_role("alert")).to_have_count(0)
        else:
            expect(section).to_contain_text("10%")
            expect(section).to_contain_text("10/s")
            expect(section).to_contain_text("190 ms")
    traffic_empty = False


def traffic_small_values(page):
    # Values by minute from the start of the window; the latest bucket is the readout.
    small = {
        "cluster.upstream_rq_completed": {10: 0.03, 30: 0.02, 50: 0.004},
        "cluster.upstream_rq_xx": {10: 0.0, 30: 0.0, 50: 0.0},
        "cluster.upstream_cx_rx_bytes_total": {10: 0.03, 30: 0.02, 50: 0.004},
        "cluster.upstream_rq_time": {10: 2400, 30: 1200, 50: 800},
    }

    def metrics(route):
        body = route.request.post_data_json
        values = small.get(body["metricName"])
        if values is None:
            route.fallback()
            return
        start = datetime.fromisoformat(body["startTime"].replace("Z", "+00:00"))
        start = start.replace(second=0, microsecond=0)
        points = [
            {"timestamp": stamp(start, timedelta(minutes=-minute)), "value": value}
            for minute, value in values.items()
        ]
        route.fulfill(
            json={
                "series": [
                    {"resourceId": rid, "points": points}
                    for rid in body.get("resourceIds") or [resource]
                ]
            }
        )

    page.route("**/loco.observability.v1.ObservabilityService/QueryMetrics", metrics)
    page.goto(state["ui_url"] + base + "?env=production")
    page.get_by_role("region", name="Resources", exact=True).get_by_role(
        "link", name="dashboard-0", exact=True
    ).click()
    section = page.get_by_role("region", name="Traffic", exact=True)
    expect(section.get_by_text("<0.01/s", exact=True)).to_have_count(1)
    expect(section.get_by_text("<0.01 B/s", exact=True)).to_have_count(1)
    expect(section.get_by_text("800 ms", exact=True)).to_have_count(1)
    ticks = section.locator(
        ".recharts-yAxis-tick-labels .recharts-cartesian-axis-tick-value"
    )
    expect(ticks).not_to_have_count(0)
    labels = ticks.all_text_contents()
    requests = [t for t in labels if t.endswith("/s") and not t.endswith("B/s")]
    assert requests == ["0/s", "0.01/s", "0.02/s", "0.03/s"], labels
    latency = [t for t in labels if t.endswith(" s") and not t.endswith("B/s")]
    assert latency == ["0 s", "1 s", "2 s", "3 s"], labels
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryMetrics", metrics)


def metrics_many(page):
    names = [f"controls-svc-{i:02d}" for i in range(13)]
    seeded = state.get("controls_many")
    if seeded is None:
        seeded = list(
            evidence.seed_resources(
                state,
                [
                    {"workspace": "acme/shared", "name": name, "service": service()}
                    for name in names
                ],
            ).values()
        )
        state["controls_many"] = seeded
        evidence.write_json(Path(args.state), state)
    requests = []

    def many(route):
        body = route.request.post_data_json
        if body["metricName"] not in (
            "k8s.pod.cpu_limit_utilization",
            "k8s.pod.memory.working_set_limit_utilization",
        ):
            route.fallback()
            return
        requests.append(body["metricName"])
        now = datetime.now(UTC)
        ids = body.get("resourceIds") or []
        series = [
            {
                "resourceId": rid,
                "points": [
                    {
                        "timestamp": stamp(now, timedelta(minutes=minute)),
                        "value": 0.0004 if i == 0 else (i + 1) / 20 * scale,
                    }
                    for minute, scale in ((50, 0.6), (30, 1.0), (10, 0.8))
                ],
            }
            for i, rid in enumerate(ids)
        ]
        route.fulfill(json={"series": series})

    page.route("**/loco.observability.v1.ObservabilityService/QueryMetrics", many)
    page.clock.install()
    page.goto(
        state["ui_url"] + base + "/observability?env=production&view=metrics&range=1h"
    )
    cpu_card = page.get_by_role("region", name="CPU", exact=True)
    chart = page.locator('[aria-label="CPU over time"]')
    expect(chart.locator(".recharts-line")).not_to_have_count(0)
    page.wait_for_timeout(500)
    count = chart.locator(".recharts-line").count()
    assert count >= 16, count
    expect(cpu_card.get_by_text("latest")).to_have_count(0)
    horizontal = chart.locator(".recharts-reference-line-line").evaluate_all(
        "els => els.filter(e => e.getAttribute('y1') === e.getAttribute('y2')).length"
    )
    assert horizontal == 1, horizontal
    box = chart.bounding_box()
    chart.hover(position={"x": box["width"] * 0.5, "y": 60})
    rows = page.locator("[data-tooltip-row]")
    expect(rows).to_have_count(8)
    expect(page.get_by_text(f"+{count - 8} more", exact=True)).to_be_visible()
    heights = rows.evaluate_all("els => els.map(e => e.getBoundingClientRect().height)")
    assert len({round(h) for h in heights}) == 1 and heights[0] <= 24, heights
    assert rows.evaluate_all("els => els.every(e => e.scrollWidth <= e.clientWidth)")
    assert "0 vCPU" not in " ".join(rows.all_text_contents()).replace("<0.001 vCPU", "")
    by_region = page.get_by_role("region", name="By resource and region")
    table_rows = by_region.locator("tbody tr")
    expect(table_rows).to_have_count(count)
    assert table_rows.first.evaluate("e => getComputedStyle(e).cursor") == "pointer"
    assert (
        chart.locator(".recharts-surface").evaluate("e => getComputedStyle(e).cursor")
        == "pointer"
    )
    page.mouse.move(0, 0)
    table_rows.first.hover()
    lines = chart.locator(".recharts-line-curve")
    opacities = lines.evaluate_all(
        "els => els.map(e => e.getAttribute('stroke-opacity'))"
    )
    assert opacities.count("1") == 1, opacities
    # A refetch keeps every row and chart element in place and never shows a skeleton.
    page.mouse.move(0, 0)
    table_rows.evaluate_all("els => els.forEach(e => { e.dataset.kept = '1'; })")
    page.evaluate(
        """() => {
            window.flashes = 0;
            new MutationObserver(() => {
                if (document.querySelector('[data-slot=skeleton]')) window.flashes++;
            }).observe(document.body, { childList: true, subtree: true });
        }"""
    )
    before = len(requests)
    page.clock.fast_forward(61_000)
    deadline = time.time() + 10
    while len(requests) == before and time.time() < deadline:
        page.wait_for_timeout(100)
    assert len(requests) > before
    page.wait_for_timeout(500)
    assert table_rows.evaluate_all("els => els.every(e => e.dataset.kept === '1')")
    expect(table_rows).to_have_count(count)
    assert page.evaluate("window.flashes") == 0
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryMetrics", many)


def token_empty(page):
    page.goto(state["ui_url"] + "/tokens?owner=personal")
    expect(
        page.get_by_role("heading", name="No personal tokens yet", exact=True)
    ).to_be_visible()
    art = page.locator("svg.es-tokens")
    expect(art).to_be_visible()
    page.wait_for_function(
        "document.querySelector('svg.es-tokens').getAnimations({subtree: true}).some(a => a.playState === 'running')"
    )
    page.emulate_media(reduced_motion="reduce")
    page.wait_for_function(
        "() => document.querySelector('svg.es-tokens').getAnimations({subtree: true}).length === 0"
    )
    create = page.get_by_role("button", name="New token", exact=True).last
    create.click()
    expect(page.get_by_role("dialog", name="New token", exact=True)).to_be_visible()
    page.keyboard.press("Escape")
    expect(create).to_be_focused()
    page.emulate_media(reduced_motion="no-preference")


def limit_display(page):
    user_id = owner["id"]
    sql(
        state,
        f"INSERT INTO api_tokens (display_suffix,token_hash,name,entity_type,entity_id,scopes,created_by,expires_at) SELECT 'test','limit-display-' || n,'limit-display-' || n,'user','{user_id}','[]','{user_id}',NOW() - INTERVAL '1 day' FROM generate_series(1,9) n;",
    )
    limit = 11
    config_url = "**/loco.config.v1.ConfigService/GetConfig"

    def config(route):
        response = route.fetch()
        data = response.json()
        data["countLimits"]["tokensPerEntity"] = limit
        route.fulfill(response=response, json=data)

    page.route(config_url, config)
    try:
        for limit, label in (
            (11, None),
            (10, "9 of 10 used"),
            (9, "Personal limit reached"),
        ):
            page.goto(state["ui_url"] + "/tokens?owner=personal")
            heading = page.get_by_role("heading", name=re.compile("^Tokens"))
            expect(page.get_by_text("limit-display-1", exact=True)).to_be_visible()
            pill = heading.get_by_role("link")
            if label is None:
                expect(pill).to_have_count(0)
            else:
                expect(pill).to_have_accessible_name(label)
                expect(page.get_by_role("link", name=label, exact=True)).to_have_count(
                    1
                )
                expect(pill).to_have_attribute(
                    "href", re.compile(r"/org/.*(?:scope=org|/settings)#s-lim$")
                )
                if limit == 10:
                    expect(pill).not_to_have_attribute("title", re.compile(".+"))
            create = page.get_by_role("button", name="New token", exact=True).first
            if limit == 9:
                expect(create).to_be_disabled()
                create.hover(force=True)
                expect(
                    page.get_by_text(
                        "You've reached the limit of 9 personal tokens. Delete one, or ask a Loco admin to raise it.",
                        exact=True,
                    )
                ).to_be_visible()
                expect(pill).to_have_attribute(
                    "title",
                    "You've reached the limit of 9 personal tokens. Delete one, or ask a Loco admin to raise it.",
                )
                page.keyboard.press("Escape")
                pill.click()
                expect(page).to_have_url(re.compile(r"scope=org#s-lim$"))
                limits_section = page.locator("#s-lim")
                expect(
                    limits_section.get_by_role("heading", name="Limits", exact=True)
                ).to_be_in_viewport()
                page.reload()
                expect(
                    limits_section.get_by_role("heading", name="Limits", exact=True)
                ).to_be_in_viewport()
                page.goto(
                    state["ui_url"] + base + "/settings?scope=org#organization-danger"
                )
                expect(
                    page.locator("#organization-danger").get_by_role(
                        "heading", name="Danger zone", exact=True
                    )
                ).to_be_in_viewport()
            else:
                expect(create).to_be_enabled()
    finally:
        page.unroute(config_url, config)
        sql(
            state,
            f"DELETE FROM api_tokens WHERE entity_id='{user_id}' AND name LIKE 'limit-display-%';",
        )


def access_picker(page):
    page.goto(state["ui_url"] + "/tokens")
    page.get_by_role("button", name="New token", exact=True).first.click()
    dialog = page.get_by_role("dialog")
    search = page.locator('input[aria-label="Add a workspace or resource"]')
    search.focus()
    expect(search).to_have_attribute("aria-expanded", "true")
    search.fill("controls-")
    expect(page.get_by_text("controls-zeta", exact=True)).to_be_visible()
    expect(search).to_have_attribute("aria-activedescendant", re.compile(r".+"))
    page.locator(
        f"[id={json.dumps(search.get_attribute('aria-activedescendant'))}]"
    ).hover()
    page.mouse.move(0, 0)
    expect(search).not_to_have_attribute("aria-activedescendant", re.compile(r".+"))
    search.press("Enter")
    expect(
        page.get_by_role("group", name=re.compile(r"^Level for controls-"))
    ).to_have_count(1)
    expect(search).to_have_value("")
    search.fill("no matching scope")
    expect(page.get_by_text("Nothing matches “no matching scope”.")).to_be_visible()
    search.press("Escape")
    expect(search).to_have_attribute("aria-expanded", "false")
    expect(dialog).to_be_visible()


def first_click_sorts_descending(table, label, first):
    button = table.page.get_by_role("button", name=label, exact=True)
    header = table.get_by_role("columnheader").filter(has=button)
    header.get_by_role("button", name=label, exact=True).press("Enter")
    expect(header).to_have_attribute("aria-sort", "descending")
    expect(table.locator("th[aria-sort]")).to_have_count(1)
    expect(table.get_by_role("row").nth(1)).to_contain_text(first)


def new_resource_menu(page):
    page.goto(state["ui_url"] + base + "/resources?env=production")
    page.get_by_role("button", name="New resource", exact=True).click()
    menu = page.get_by_role("menu")
    items = menu.get_by_role("menuitem")
    expect(items).to_have_text(
        [
            "New service",
            re.compile(r"^Database\s*Soon$"),
            re.compile(r"^Cache\s*Soon$"),
            re.compile(r"^Queue\s*Soon$"),
            re.compile(r"^Blob storage\s*Soon$"),
            re.compile(r"^Function\s*Soon$"),
        ]
    )
    expect(items.first).to_be_enabled()
    for name in ("Database", "Cache", "Queue", "Blob storage", "Function"):
        expect(
            menu.get_by_role("menuitem", name=re.compile(f"^{name}"))
        ).to_be_disabled()
    page.keyboard.press("Escape")


def resources(page):
    for route in ("/resources", ""):
        page.goto(state["ui_url"] + base + route + "?env=production")
        table = (
            page.get_by_role("table")
            .filter(has=page.get_by_role("columnheader", name="Resource", exact=True))
            .first
        )
        header = table.get_by_role("columnheader", name="Resource", exact=True)
        name = header.get_by_role("button", name="Resource", exact=True)
        name.press("Enter")
        name.press("Enter")
        if route:
            expect(page).to_have_url(re.compile(r"[?&]sort=name\.desc"))
            page.reload()
            expect(table.get_by_role("row").nth(1)).to_contain_text("controls-zeta")
            name.press("Enter")
            expect(table.get_by_role("row").nth(1)).to_contain_text("controls-alpha")
            first_click_sorts_descending(table, "Size", "controls-zeta")
            table.get_by_role("button", name="Columns", exact=True).click()
            created = page.get_by_role("menuitemcheckbox", name="Created")
            created.click()
            expect(created).to_have_attribute("aria-checked", "true")
            page.keyboard.press("Escape")
            first_click_sorts_descending(table, "Created", "controls-zeta")


def resource_view_history(page):
    dashboard_url = state["ui_url"] + base + "?env=production"
    page.goto(dashboard_url)
    page.goto(state["ui_url"] + base + "/resources?env=production")
    page.get_by_role("button", name="Map", exact=True).click()
    expect(page).to_have_url(re.compile(r"[?&]view=map"))
    page.go_back()
    expect(page).to_have_url(dashboard_url)
    page.go_forward()
    expect(page).to_have_url(re.compile(r"/resources\?env=production&view=map"))
    page.get_by_role("button", name="Table", exact=True).click()
    expect(page).to_have_url(re.compile(r"/resources\?env=production$"))
    page.go_back()
    expect(page).to_have_url(dashboard_url)


def metrics(page):
    environments = call(
        state,
        "loco.environment.v1.EnvironmentService/ListEnvironments",
        {"workspaceId": workspace},
    )["environments"]
    production = next(e["id"] for e in environments if e["name"] == "production")
    sql(
        state,
        f"UPDATE resources SET display_name='Controls Pilot' WHERE id='{resource}';"
        f"UPDATE resources SET display_name='Controls Alpha' WHERE id='{sorted_resources[0]}';",
    )
    # One ready deploy at each metric point, so a click on any point offers one.
    deploys = [
        {
            "id": f"01a1c8b3-0000-7000-8000-0000000de90{i}",
            "resourceId": resource,
            "placements": [{"region": "us-east-1"}],
            "environmentId": production,
            "createdAt": point["timestamp"],
            "startedAt": point["timestamp"],
            "spec": {
                "service": {
                    "build": {"image": "ghcr.io/acme/pilot:1.27"},
                    "regions": {"us-east-1": {"cpu": "500m", "memory": "512Mi"}},
                }
            },
        }
        for i, point in enumerate(telemetry_points)
    ]
    # A deploy that never became ready has no deploy time and no marker.
    deploys.append(
        {
            **deploys[len(deploys) // 2],
            "id": "01a1c8b3-0000-7000-8000-0000000de9ff",
            "startedAt": None,
        }
    )
    page.route(
        "**/loco.deployment.v1.DeploymentService/ListDeployments",
        lambda route: route.fulfill(
            json={
                "deployments": deploys
                if route.request.post_data_json.get("resourceId") == resource
                else []
            }
        ),
    )
    page.goto(
        state["ui_url"] + base + "/observability?env=production&view=metrics&range=1h"
    )
    chart = page.locator('[aria-label="CPU over time"]')
    expect(chart.locator(".recharts-line")).to_be_visible()
    cpu_card = page.get_by_role("region", name="CPU", exact=True)
    expect(cpu_card.get_by_text("% of limit, peak per bucket")).to_be_visible()
    expect(cpu_card.get_by_text("latest")).to_have_count(0)
    expect(chart.get_by_text("limit", exact=True)).to_have_count(1)
    expect(chart.get_by_text("Controls Pilot 000de900").first).to_be_visible()
    memory = page.locator('[aria-label="Memory over time"]')
    chart.hover(position={"x": chart.bounding_box()["width"] * 0.8, "y": 60})
    expect(memory.locator(".recharts-tooltip-cursor")).to_have_count(1)
    by_region = page.get_by_role("region", name="By resource and region")
    expect(
        by_region.get_by_role("row").filter(has_text="Controls Pilot")
    ).to_contain_text("0.175 vCPU 35%")
    expect(
        by_region.get_by_role("columnheader", name="CPU (latest) ↓")
    ).to_have_attribute("aria-sort", "descending")
    by_region.get_by_role("button", name="Resource", exact=True).click()
    expect(by_region.get_by_role("columnheader", name="Resource ↑")).to_have_attribute(
        "aria-sort", "ascending"
    )
    by_region.get_by_role("row").filter(has_text="Controls Alpha").get_by_role(
        "cell"
    ).nth(1).click()
    expect(page).to_have_url(re.compile(r"resource:controls-alpha"))
    page.goto(
        state["ui_url"] + base + "/observability?env=production&view=metrics&range=1h"
    )
    expect(chart.locator(".recharts-line")).to_be_visible()
    chart.click(position={"x": chart.bounding_box()["width"] / 2, "y": 60})
    menu = page.get_by_role("menu", name=re.compile(r"^Around"))
    expect(menu).to_be_visible()
    expect(
        menu.get_by_role("menuitem", name=re.compile(r"^Deploy 000de90")).first
    ).to_be_visible()
    expect(menu.get_by_role("menuitem", name=re.compile("000de9ff"))).to_have_count(0)
    first, second = (
        menu.get_by_role("menuitem").nth(0),
        menu.get_by_role("menuitem").nth(1),
    )
    first.focus()
    page.keyboard.press("ArrowDown")
    expect(second).to_be_focused()
    page.keyboard.press("Escape")
    expect(menu).not_to_be_visible()
    chart.click(position={"x": chart.bounding_box()["width"] / 2, "y": 60})
    expect(menu).to_be_visible()
    assert (
        page.evaluate(
            "getComputedStyle(document.documentElement).overflow + getComputedStyle(document.body).overflow"
        ).count("hidden")
        == 0
    )
    page.get_by_role("button", name="Earlier", exact=True).click()
    expect(menu).not_to_be_visible()
    expect(page).to_have_url(re.compile(r"[?&]from="))
    page.unroute("**/loco.deployment.v1.DeploymentService/ListDeployments")
    sql(
        state,
        f"UPDATE resources SET display_name=name WHERE id IN ('{resource}','{sorted_resources[0]}');",
    )


out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with session(out) as browser:
    context, page, errors = open_page(browser, state=state, timezone_id="UTC")
    mock_telemetry(context)
    time_preferences(browser)
    page.goto(state["ui_url"] + base + "/resource/" + resource + "?env=production")
    new_resource_menu(page)
    page.goto(state["ui_url"] + base + "/settings?scope=workspace")
    group = page.get_by_role("group", name="Settings for", exact=True)
    workspace_button = group.get_by_role("button", name="Workspace", exact=True)
    personal = group.get_by_role("button", name="Personal", exact=True)
    expect(workspace_button).to_have_attribute("aria-pressed", "true")
    indicator = group.locator('[data-slot="segmented-indicator"]').first
    assert (
        abs(indicator.bounding_box()["x"] - workspace_button.bounding_box()["x"]) <= 1
    )
    personal.evaluate("""node => {
                window.indicatorPositions = [];
                window.indicatorSampleDone = false;
                const group = node.parentElement;
                const target = node.getBoundingClientRect().x;
                const sample = () => {
                    window.indicatorPositions.push(group.querySelector('[data-slot="segmented-indicator"]').getBoundingClientRect().x);
                    if (window.indicatorPositions.length < 2 || Math.abs(window.indicatorPositions.at(-1) - target) > 1) requestAnimationFrame(sample);
                    else window.indicatorSampleDone = true;
                };
                node.click();
                requestAnimationFrame(sample);
            }""")
    expect(personal).to_have_attribute("aria-pressed", "true")
    page.wait_for_function("() => window.indicatorSampleDone")
    assert abs(indicator.bounding_box()["x"] - personal.bounding_box()["x"]) <= 1
    positions = page.evaluate("window.indicatorPositions")
    start = workspace_button.bounding_box()["x"]
    end = personal.bounding_box()["x"]
    assert any(start + 2 < position < end - 2 for position in positions), positions
    personal.focus()
    page.keyboard.press("ArrowLeft")
    expect(workspace_button).to_be_focused()
    page.keyboard.press("Space")
    expect(workspace_button).to_have_attribute("aria-pressed", "true")
    page.emulate_media(reduced_motion="reduce")
    personal.click()
    expect(indicator).to_have_css("transform", "none")
    assert abs(indicator.bounding_box()["x"] - personal.bounding_box()["x"]) <= 1
    assert indicator.evaluate("node => getComputedStyle(node).transform") == "none"
    personal.click()
    modes = page.get_by_role("radiogroup", name="Mode")
    html = page.locator("html")
    dark_class = re.compile(r"\bdark\b")
    modes.get_by_role("radio", name="Dark", exact=True).click()
    expect(html).to_have_class(dark_class)
    modes.get_by_role("radio", name="System", exact=True).click()
    expect(html).not_to_have_class(dark_class)
    colours = page.get_by_role("radiogroup", name="Color theme")
    loco = colours.get_by_role("radio", name="Loco", exact=True)
    expect(colours.get_by_role("radio", name="Solarized", exact=True)).to_be_visible()
    loco.focus()
    page.keyboard.press("ArrowRight")
    catppuccin = colours.get_by_role("radio", name="Catppuccin", exact=True)
    expect(catppuccin).to_be_focused()
    expect(catppuccin).to_be_checked()
    page.keyboard.press("ArrowLeft")
    expect(loco).to_be_checked()
    page.set_viewport_size(evidence.NARROW)
    workspace_button.click()
    page.get_by_role("textbox", name="Name", exact=True).wait_for()
    page.evaluate("window.scrollTo(0, document.documentElement.scrollHeight)")
    page.wait_for_function("() => window.scrollY > 0")
    name = page.get_by_role("textbox", name="Name", exact=True)
    name.focus()
    expect(name).to_be_focused()
    name.evaluate("node => node.scrollIntoView({block: 'nearest'})")
    box = name.bounding_box()
    assert 0 <= box["y"] and box["y"] + box["height"] <= page.viewport_size["height"]
    for check in (
        shell_navigation,
        mobile_sidebar,
        activity_deployments,
        log_levels,
        log_columns,
        workload_logs,
        template_logs,
        log_drawer,
        log_paging,
        log_cap,
        live_focus,
        time_presets,
        calendar,
        custom_range_future_end,
        metrics,
        query_enter,
        query_suggestions,
        volume,
        volume_axis,
        access_picker,
        resources,
        resource_view_history,
        token_empty,
        limit_display,
        dashboard,
        dashboard_counts,
        traffic,
        traffic_small_values,
        metrics_many,
    ):
        check(page)
    assert not errors, errors
    context.close()
print("PASS: shared controls", flush=True)
