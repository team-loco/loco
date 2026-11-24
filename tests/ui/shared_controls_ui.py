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
    copied_text,
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
            "us-ca-1": {
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
resource_bucket_requests = []
LEVELS = ("LOG_LEVEL_INFO", "LOG_LEVEL_WARN", "LOG_LEVEL_ERROR", "LOG_LEVEL_DEBUG")


def mock_telemetry(context):
    now = datetime.now(UTC)
    logs = [
        {
            "timestamp": stamp(now, timedelta(seconds=i * 13 + 1)),
            "level": LEVELS[i % 4],
            "body": f"Request completed ({i + 1})",
            "resourceId": resource,
            "resourceAttributes": {"cloud.region": "us-ca-1"},
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
        lambda route: route.fulfill(json={"entries": logs}),
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
            resource_bucket_requests.append(body)
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
            # Buckets sit whole inside the requested window, as the server's do: the
            # same three fractions of the window back from the start of the run, moved
            # back whole windows for the prior-period query.
            end = datetime.fromisoformat(body["endTime"].replace("Z", "+00:00"))
            start = datetime.fromisoformat(body["startTime"].replace("Z", "+00:00"))
            window = end - start
            anchor = now - round((now - end) / window) * window
            traffic_points = [
                {"timestamp": stamp(anchor, window * sixtieths / 60), "value": rate}
                for sixtieths in (50, 30, 10)
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
    # Waits for any slide still running to finish before measuring.
    expect(
        target.page.locator(f'[data-slot="sidebar-content"] {highlight_selector}')
    ).to_have_css("transform", "none")
    slide = target.evaluate(
        """async (node, selector) => {
        const sidebar = node.closest('[data-slot="sidebar-content"]');
        const row = node.closest('li');
        const center = rect => rect.top + rect.height / 2;
        const start = center(sidebar.querySelector(selector).getBoundingClientRect());
        const end = center(row.getBoundingClientRect());
        let styled;
        const highlight = await new Promise(resolve => {
            // Motion styles the new highlight at its start in the commit's microtasks, before any frame ticks it on.
            styled = new MutationObserver(records => {
                const found = row.querySelector(selector);
                if (records.some(record => record.target === found)) resolve(found);
            });
            styled.observe(row, {subtree: true, attributeFilter: ['style']});
            node.click();
            // A highlight that does not slide gets no style; the first frame shows where it landed.
            const frame = () => {
                const found = row.querySelector(selector);
                if (found) resolve(found);
                else requestAnimationFrame(frame);
            };
            requestAnimationFrame(frame);
        });
        styled.disconnect();
        const first = center(highlight.getBoundingClientRect());
        // Hold the highlight halfway between the two rows, where the slide passes.
        const transform = highlight.style.transform;
        highlight.style.transform = `translateY(${(start - end) / 2}px)`;
        const rect = highlight.getBoundingClientRect();
        // The trailing edge should remain visible outside the destination row.
        const trailingEdge = center(rect) - Math.sign(end - start) * (rect.height / 2 - 1);
        highlight.style.pointerEvents = 'auto';
        const visible = document.elementsFromPoint(rect.left + rect.width / 2, trailingEdge)
            .includes(highlight);
        highlight.style.removeProperty('pointer-events');
        highlight.style.transform = transform;
        return {visible, start, first};
    }""",
        highlight_selector,
    )
    assert abs(slide["first"] - slide["start"]) <= 1, (
        f"Sidebar highlight starts its slide to {target.inner_text()} at y={slide['first']}, not at the item it left (y={slide['start']})"
    )
    assert slide["visible"], (
        f"Sidebar highlight is hidden part way to {target.inner_text()}"
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
    expect(drawer).to_contain_text("Nothing here yet.")
    expect(drawer.get_by_role("button", name="Warnings", exact=True)).to_be_visible()
    expect(
        drawer.get_by_role("button", name="Resource: All resources", exact=True)
    ).to_be_visible()
    drawer.get_by_role("button", name="Close workspace activity", exact=True).click()
    expect(drawer).not_to_be_visible()
    expect(activity).to_be_focused()
    sidebar = page.locator('[data-slot="sidebar-content"]')
    resources = sidebar.get_by_role("link", name="Resources", exact=True)
    dashboard = sidebar.get_by_role("link", name="Dashboard", exact=True)

    def nav_icon(name):
        return sidebar.locator(f'[data-nav="{name}"] svg > :nth-child(1)')

    expect(nav_icon("Dashboard")).to_have_css("animation-name", "none")
    assert_sidebar_slide(resources)
    assert_sidebar_slide(dashboard)
    settings = sidebar.get_by_role("link", name="Settings", exact=True)
    assert_sidebar_slide(settings)
    expect(nav_icon("Settings")).to_have_css("animation-name", "navFrom")
    expect(nav_icon("Dashboard")).to_have_css("animation-name", "none")
    # Scrolls until the page has moved; the shorter Team page pulls the scroll back to the top.
    page.wait_for_function(
        "() => { window.scrollTo(0, document.documentElement.scrollHeight); return window.scrollY > 0; }"
    )
    # The workspace links stay in the sidebar while an organization page opens.
    page.evaluate("""() => {
        window.navObserver = new MutationObserver(() => {
            const nav = document.querySelector('[data-slot="sidebar-content"]');
            const linked = href => nav?.querySelector(`a[href*="${href}"]`);
            if (nav && !(linked("/resources") && linked("/secrets"))) {
                window.navGap ??= `${location.pathname}: ${nav.innerText}`;
            }
        });
        window.navObserver.observe(document.body, { childList: true, subtree: true, attributes: true });
    }""")
    assert_sidebar_slide(sidebar.get_by_role("link", name="Team", exact=True))
    gap = page.evaluate(
        "() => { window.navObserver.disconnect(); return window.navGap ?? null; }"
    )
    assert gap is None, gap
    assert_sidebar_slide(dashboard)
    page.emulate_media(reduced_motion="reduce")
    resources.press("Enter")
    expect(resources).to_have_attribute("aria-current", "page")
    expect(sidebar.locator('[data-slot="sidebar-highlight"]')).to_have_css(
        "transform", "none"
    )
    settings.press("Enter")
    expect(settings).to_have_attribute("aria-current", "page")
    expect(nav_icon("Settings")).to_have_css("animation-name", "none")
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
    production = sql(
        state,
        f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND slug='production'",
    )

    def deployment(id, status, minutes, replicas=(), **extra):
        return {
            "id": id,
            "resourceId": resource,
            "environmentId": production,
            "status": f"DEPLOYMENT_STATUS_{status}",
            "placements": [
                {
                    "region": "us-ca-1",
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
                                "region": "us-ca-1",
                                "status": "RESOURCE_STATUS_HEALTHY",
                                "readyReplicas": 1,
                                "desiredReplicas": 1,
                                "deploymentId": deployments[0]["id"],
                                "servingDeploymentId": deployments[0]["id"],
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
    expect(cards.nth(1)).to_contain_text(re.compile(r"took 16s, 7m ago"))
    expect(
        cards.nth(0).get_by_role("link", name="Open controls-pilot", exact=True)
    ).to_be_visible()
    page.keyboard.press("Escape")
    page.unroute_all(behavior="wait")


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
    page.get_by_role("row", name=re.compile(r"Request completed \(2\)")).click()
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
        lambda route: route.fulfill(json={"entries": entries}),
    )
    page.goto(
        state["ui_url"]
        + base
        + f"/observability?env=production&view=logs&cols=level,resource,{attribute}"
    )
    lines = page.get_by_role("region", name="Log lines")
    row = lines.get_by_role("row", name=re.compile(r"Slow request")).first
    expect(row).to_be_visible()
    for label in ("Time", "Level", "Resource", attribute):
        expect(
            lines.get_by_role("columnheader", name=label, exact=True)
        ).to_be_visible()
    clipped = page.evaluate(
        """() => [...document.querySelectorAll('[aria-label="Log lines"] :is(th, td) span')]
            .filter(e => e.scrollWidth > e.clientWidth + 1 && e.querySelector('.truncate') === null
                && /^(Time|Level|Warning|[\\d:.]+|container\\.image\\.name)$/.test(e.textContent.trim()))
            .map(e => e.textContent.trim())"""
    )
    assert clipped == [], clipped
    page.evaluate("localStorage.setItem('loco-clock', '12')")
    page.reload()
    time_cell = (
        lines.get_by_role("row", name=re.compile(r"Slow request"))
        .first.locator("td")
        .first
    )
    expect(time_cell.locator("span").first).to_have_text(
        re.compile(r"^\d+:\d\d:\d\d\.\d{3} [AP]M$")
    )
    assert time_cell.evaluate("e => e.scrollWidth <= e.clientWidth"), (
        "12-hour time clipped"
    )
    before = time_cell.evaluate("e => e.clientWidth")
    page.get_by_role("separator", name="Resize Time").focus()
    page.keyboard.press("ArrowRight")
    expect(page).to_have_url(re.compile(r"widths=time(:|%3A)\d+"))
    page.wait_for_function(
        "([node, before]) => node.clientWidth > before",
        arg=[time_cell.element_handle(), before],
    )
    page.evaluate("localStorage.removeItem('loco-clock')")
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogs")
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogs",
        lambda route: route.fulfill(json={"entries": []}),
    )
    logs_url = state["ui_url"] + base + "/observability?env=production&view=logs&live=0"
    page.goto(logs_url + "&q=no-such-line")
    expect(
        page.get_by_text("No lines match these filters.", exact=True)
    ).to_be_visible()
    page.goto(logs_url)
    expect(
        page.get_by_text(
            "Anything shared's resources write to stdout or stderr lands here.",
            exact=True,
        )
    ).to_be_visible()
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
        lambda route: route.fulfill(json={"entries": entries}),
    )
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    lines = page.get_by_role("region", name="Log lines")
    workload_row = lines.get_by_role("row", name=re.compile(r"Broker connection lost"))
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
    page.mouse.move(0, 0)
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
    lines.get_by_role("row", name=re.compile(r"Checkout request served")).click()
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
        lambda route: route.fulfill(json={"entries": entries}),
    )
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    rendered = "GetCartAsync called with userId=u-42 in {region}"
    row = page.get_by_role("region", name="Log lines").get_by_role(
        "row", name=re.compile(re.escape(rendered))
    )
    row.click()
    detail = page.get_by_role("dialog")
    expect(detail.get_by_text(rendered, exact=True)).to_be_visible()
    expect(detail.get_by_text(template, exact=True)).to_be_visible()
    page.keyboard.press("Escape")
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogs")


def log_drawer(page):
    image_digest = "registry.example/app@sha256:" + "a" * 64
    entries = [
        {
            "timestamp": stamp(datetime.now(UTC), timedelta(seconds=5)),
            "level": "LOG_LEVEL_INFO",
            "body": "Drawer line",
            "resourceId": resource,
            "resourceAttributes": {
                "container.image.name": image_digest,
                "k8s.cluster.name": "staging",
            },
        }
    ]
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogs",
        lambda route: route.fulfill(json={"entries": entries}),
    )
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    row = page.get_by_role("region", name="Log lines").get_by_role(
        "row", name=re.compile(r"Drawer line")
    )
    row.click()
    detail = page.get_by_role("dialog")
    expect(detail).to_be_visible()
    page.wait_for_timeout(500)
    title = detail.get_by_role("heading", name="Message", exact=True)
    expect(title).to_be_visible()
    title_box = title.bounding_box()
    message_box = detail.get_by_text("Drawer line", exact=True).bounding_box()
    assert 0 < message_box["y"] - (title_box["y"] + title_box["height"]) <= 40, (
        title_box,
        message_box,
    )
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
    json_view = detail.locator("[data-slot=code-block] pre").last
    expect(json_view).to_contain_text(f'"{image_digest}"')
    wrap = detail.get_by_role("button", name="Wrap lines", exact=True)
    expect(wrap).to_have_attribute("aria-pressed", "true")
    assert json_view.evaluate("el => el.scrollWidth == el.clientWidth")
    wrap.focus()
    wrap.press("Space")
    expect(wrap).to_be_focused()
    expect(wrap).to_have_attribute("aria-pressed", "false")
    assert json_view.evaluate("el => el.scrollWidth > el.clientWidth")
    detail.get_by_role("button", name="Copy JSON", exact=True).press("Enter")
    expect(detail.get_by_role("button", name="Copied", exact=True)).to_be_visible()
    copied = json.loads(copied_text(page))
    assert copied["resource_attributes"]["container.image.name"] == image_digest
    page.keyboard.press("Escape")
    expect(detail).to_have_count(0)
    row.click()
    expect(detail).to_be_visible()
    page.wait_for_timeout(500)
    assert abs(detail.bounding_box()["width"] - wide) <= 1
    expect(detail.get_by_text("@k8s.cluster.name", exact=True)).to_have_count(0)
    expect(wrap).to_have_attribute("aria-pressed", "false")
    wrap.click()
    expect(wrap).to_have_attribute("aria-pressed", "true")
    assert json_view.evaluate("el => el.scrollWidth == el.clientWidth")
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
    live = page.locator("[data-live-button]")
    expect(live).to_have_attribute("aria-pressed", "true")
    expect(live).to_have_text("Pause")
    expect(page).not_to_have_url(re.compile(r"live="))
    live.focus()
    page.keyboard.press("Enter")
    expect(live).to_have_attribute("aria-pressed", "false")
    expect(live).to_have_text("Live")
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
    held[0].fulfill(json={"entries": []})
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

    now_ms = int(datetime.now(UTC).timestamp() * 1000)
    page.goto(
        state["ui_url"]
        + base
        + f"/observability?env=production&view=logs&from={now_ms - 7_200_000}&to={now_ms - 3_600_000}"
    )
    expect(live).to_be_enabled()
    expect(live).to_have_attribute("aria-pressed", "false")
    live.click()
    wait_for_query(
        page,
        lambda query: "from" not in query and "to" not in query,
    )
    expect(live).to_have_attribute("aria-pressed", "true")
    expect(live).to_have_text("Pause")


def log_paging(page):
    now = datetime.now(UTC)
    many = [
        {
            "timestamp": stamp(now, timedelta(seconds=i * 7 + 1)),
            "level": "LOG_LEVEL_INFO",
            "body": f"Paged line {i + 1}",
            "resourceId": resource,
            "resourceAttributes": {"cloud.region": "us-ca-1"},
            "logAttributes": {"http.request.method": "GET"},
        }
        for i in range(120)
    ]
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogs",
        lambda route: route.fulfill(json={"entries": many}),
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
    page.get_by_role("row", name=re.compile(r"Paged line 30\b")).click()
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
    row = page.get_by_role("row", name=re.compile(r"Paged line 30\b"))
    menu = row.get_by_role("button", name="Line actions", exact=True)
    row.hover()
    menu.click()
    page.get_by_role("menuitem", name="Copy message", exact=True).click()
    assert copied_text(page) == "Paged line 30"
    row.hover()
    menu.click()
    page.get_by_role("menuitem", name="Copy as JSON", exact=True).click()
    copied = json.loads(copied_text(page))
    assert copied["message"] == "Paged line 30"
    assert re.search(r"(Z|[+-]\d\d:\d\d)$", copied["timestamp"]), copied
    assert "trace_id" not in copied and "span_id" not in copied, copied
    page.get_by_role("button", name="Add a column", exact=True).click()
    page.get_by_role("dialog", name="Columns").get_by_role(
        "checkbox", name="Method", exact=True
    ).click()
    page.keyboard.press("Escape")
    wait_for_query(page, lambda query: "method" in query.get("cols", [""])[0])
    expect(
        page.get_by_role("row", name=re.compile(r"GET Paged line 30\b"))
    ).to_have_count(1)
    resource_head = page.get_by_role("columnheader", name="Resource", exact=True)
    width = resource_head.evaluate("node => node.getBoundingClientRect().width")
    page.get_by_role("separator", name="Resize Resource").focus()
    page.keyboard.press("ArrowRight")
    expect(page).to_have_url(re.compile(r"widths=resource(:|%3A)\d+"))
    page.wait_for_function(
        "([node, width]) => Math.abs(node.getBoundingClientRect().width - width) <= 1",
        arg=[resource_head.element_handle(), width + 16],
    )
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
        lambda route: route.fulfill(json={"entries": rows, "nextPageToken": "more"}),
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


def volume_error(browser):
    context, page, errors = open_page(
        browser, state=state, timezone_id="UTC", now=datetime.now(UTC)
    )
    mock_telemetry(context)
    buckets = "**/loco.observability.v1.ObservabilityService/QueryLogBuckets"
    page.route(
        buckets,
        lambda route: route.fulfill(
            status=500, json={"code": "internal", "message": "boom"}
        ),
    )
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    expect(page.locator("[data-live-button]")).to_have_attribute("aria-pressed", "true")
    expect(page.get_by_text("Request completed (1)", exact=True)).to_be_visible()
    panel = page.get_by_role("region", name="Log volume")
    failed = panel.get_by_role("alert")
    expect(failed).to_contain_text("Couldn’t load log volume")
    lines = page.get_by_role("region", name="Log lines")
    top = lines.bounding_box()["y"]
    # Live tail moves the volume window every 15 seconds; hold the next request.
    page.route(buckets, lambda route: None)
    with page.expect_request("**/QueryLogBuckets"):
        page.clock.run_for(15_000)
    assert failed.is_visible()
    assert lines.bounding_box()["y"] == top
    page.unroute(buckets)
    page.clock.run_for(15_000)
    expect(panel.get_by_role("slider", name=re.compile(r"^Log volume"))).to_be_visible()
    expect(failed).to_have_count(0)
    assert not errors, errors
    context.close()


def expired_session(browser):
    context, page, errors = open_page(
        browser, state=state, timezone_id="UTC", now=datetime.now(UTC)
    )
    mock_telemetry(context)
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    expect(page.locator("[data-live-button]")).to_have_attribute("aria-pressed", "true")
    expect(page.get_by_text("Request completed (1)", exact=True)).to_be_visible()
    rejected = {"code": "unauthenticated", "message": "session expired"}
    for method in (
        "loco.observability.v1.ObservabilityService/QueryLogBuckets",
        "loco.token.v1.TokenService/GetScopes",
        "loco.auth.v1.AuthService/RefreshWebSession",
        "loco.user.v1.UserService/WhoAmI",
    ):
        page.route(
            f"**/{method}",
            lambda route: route.fulfill(status=401, json=rejected),
        )
    sent = []
    page.on(
        "request",
        lambda request: (
            sent.append(request.url)
            if request.url.endswith("/QueryLogBuckets")
            else None
        ),
    )
    # Live tail moves the volume window every 15 seconds, so each tick is a new request.
    page.clock.run_for(15_000)
    # The shell sends the signed-out user to sign in, which unmounts the logs page.
    expect(page).not_to_have_url(re.compile(r"/observability"))
    page.clock.run_for(60_000)
    assert len(sent) <= 2, sent
    context.close()


def metrics_error(browser):
    context, page, errors = open_page(
        browser, state=state, timezone_id="UTC", now=datetime.now(UTC)
    )
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryMetrics",
        lambda route: route.fulfill(
            status=500, json={"code": "internal", "message": "boom"}
        ),
    )
    page.goto(
        state["ui_url"] + base + "/observability?env=production&view=metrics&range=1h"
    )
    failed = page.get_by_role("alert").filter(has_text="Couldn’t load metrics")
    expect(failed).to_be_visible()
    # Live metrics refetch every minute; hold those requests.
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryMetrics", lambda route: None
    )
    with page.expect_request("**/QueryMetrics"):
        page.clock.run_for(60_000)
    assert failed.is_visible()
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


def typed_range_error(page):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    page.get_by_role("button", name=re.compile(r"^Past ")).click()
    time_range = page.get_by_role("dialog", name="Time range")
    typed = time_range.get_by_role("textbox", name="Type a time range")
    expect(typed).to_have_attribute(
        "placeholder", re.compile(r"^Date range, like 15m, 6h")
    )
    typed.fill("soon")
    typed.press("Enter")
    alert = time_range.get_by_role("alert")
    expect(alert).to_have_count(1)
    expect(alert).to_have_text(re.compile(r"^Try a span like"))
    expect(typed).to_have_attribute("aria-invalid", "true")
    gap = alert.bounding_box()["y"] - (
        typed.bounding_box()["y"] + typed.bounding_box()["height"]
    )
    assert 0 <= gap <= 12, gap
    typed.fill("15m")
    expect(alert).to_have_count(0)
    page.keyboard.press("Escape")


def pin_needs_applied_query(page):
    page.goto(
        state["ui_url"]
        + base
        + "/observability?env=production&view=metrics&range=1h&q=checkout"
    )
    expect(page.get_by_text("Text search doesn't filter metrics")).to_be_visible()
    expect(page.get_by_role("button", name="Clear", exact=True)).to_be_visible()
    expect(page.get_by_role("button", name="Pin this search")).to_have_count(0)
    page.goto(
        state["ui_url"] + base + "/observability?env=production&view=logs&q=checkout"
    )
    expect(page.get_by_role("button", name="Pin this search")).to_be_visible()
    expect(page.locator('input[aria-label="Search and filter"]')).to_have_value(
        "checkout"
    )
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    search = page.locator('input[aria-label="Search and filter"]')
    search.fill("checkout @lev")
    expect(page.get_by_role("option", name=re.compile(r"^@level"))).to_be_visible()
    expect(page.get_by_role("button", name="Clear", exact=True)).to_have_count(0)
    expect(page.get_by_role("button", name="Pin this search")).to_have_count(0)
    search.press("Escape")


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
    sent = len(resource_bucket_requests)
    with expect_rpc(
        page,
        "QueryLogBuckets",
        lambda body: body.get("groupBy") == "LOG_BUCKET_GROUP_LEVEL",
    ):
        page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    search = page.locator('input[aria-label="Search and filter"]')
    expect(search).to_be_visible()
    # Per-resource counts load only once the suggestion list opens.
    assert len(resource_bucket_requests) == sent, resource_bucket_requests[sent:]
    search.fill("@lev")
    expect(page.get_by_role("option", name=re.compile(r"^@level"))).to_be_visible()
    expect(search).to_have_attribute("aria-expanded", "true")
    bar = search.locator('xpath=ancestor::div[@role="presentation"]')
    popup = page.locator('[data-slot="combobox-content"]')
    # Waits out the opening zoom before comparing widths.
    page.wait_for_function(
        "([bar, popup]) => Math.abs(Math.min(520, bar.getBoundingClientRect().width) - popup.getBoundingClientRect().width) <= 2",
        arg=[bar.element_handle(), popup.element_handle()],
    )
    expect(popup).to_contain_text("exclude")
    expect(popup).not_to_contain_text("either")
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
    expect(page.get_by_text("@region values, counts from loaded lines")).to_be_visible()
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
    expect(page.get_by_role("tooltip")).to_contain_text("lines")
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
    # A window inside one day: one that crosses midnight labels its axis with dates.
    end = int(datetime(2026, 11, 1, 12, tzinfo=UTC).timestamp() * 1000)
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
        cluster = evidence.seed_cluster(state, "us-ca-1", name="dashboard-pilot")
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
                            "placements": {"us-ca-1": {"cluster": cluster}},
                            "method": "cli",
                            "age": "1 hour",
                            "spec": {
                                "platform": "linux/amd64",
                                "build": {
                                    "type": "image",
                                    "image": "example.invalid/dashboard:1",
                                },
                                "port": 8080,
                                "regions": {
                                    "us-ca-1": {
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
        f"UPDATE deployment_placements SET replicas=CASE WHEN resources.slug='dashboard-0' THEN 48 ELSE 12 END FROM deployments, resources WHERE deployment_placements.deployment_id=deployments.id AND deployments.resource_id=resources.id AND resources.workspace_id='{workspace}' AND resources.slug IN ('dashboard-0','dashboard-1'); UPDATE placements SET ready_replicas=CASE WHEN resources.slug='dashboard-0' THEN 47 ELSE 12 END FROM resources WHERE placements.resource_id=resources.id AND resources.workspace_id='{workspace}' AND resources.slug IN ('dashboard-0','dashboard-1'); INSERT INTO resource_domains(resource_id, environment_id, domain, domain_source, subdomain_label, app_domain, is_primary) SELECT resources.id, environments.id, 'dashboard-0.apps.example.test', 'app', 'dashboard-0', 'apps.example.test', true FROM resources, environments WHERE resources.workspace_id='{workspace}' AND resources.slug='dashboard-0' AND environments.workspace_id='{workspace}' AND environments.slug='production' AND NOT EXISTS (SELECT 1 FROM resource_domains WHERE resource_id=resources.id);",
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
        replicas = page.get_by_label("us-ca-1: 47/48", exact=True)
        expect(replicas).to_be_visible()
        # The authentication init script starts every navigation in light mode.
        if mode == "dark":
            page.get_by_role("button").filter(has_text="Olivia Owner").click()
            modes = page.get_by_role("radiogroup", name="Mode")
            # The menu sits against the viewport, like the sidebar it opens from.
            expect(
                modes.locator(
                    'xpath=ancestor::*[@data-slot="dropdown-menu-content"]/..'
                )
            ).to_have_css("position", "fixed")
            for root_font in ("16px", "20px"):
                page.evaluate(
                    f"document.documentElement.style.fontSize = '{root_font}'"
                )
                gaps = modes.evaluate("""group => {
                    const box = group.getBoundingClientRect();
                    const pad = parseFloat(getComputedStyle(group).paddingLeft) + group.clientTop;
                    const thumb = group.querySelector('.mt-thumb').getBoundingClientRect();
                    const item = group.querySelector('[data-checked]');
                    const cell = item.getBoundingClientRect();
                    const icon = item.querySelector('svg').getBoundingClientRect();
                    return {
                        pad,
                        top: thumb.top - box.top,
                        bottom: box.bottom - thumb.bottom,
                        left: thumb.left - box.left,
                        right: box.right - thumb.right,
                        cellLeft: cell.left - box.left,
                        iconOffset: (icon.left + icon.width / 2) - (thumb.left + thumb.width / 2),
                    };
                }""")
                where = (color, root_font, gaps)
                assert abs(gaps["top"] - gaps["pad"]) <= 1, where
                assert abs(gaps["bottom"] - gaps["pad"]) <= 1, where
                assert abs(gaps["left"] - gaps["cellLeft"]) <= 1, where
                assert gaps["left"] >= gaps["pad"] - 1, where
                assert gaps["right"] >= gaps["pad"] - 1, where
                assert abs(gaps["iconOffset"]) <= 1, where
            page.evaluate("document.documentElement.style.fontSize = ''")
            modes.get_by_role("radio", name="Dark", exact=True).click()
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
            "us-ca-1: 47/48"
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
        links = allocated.get_by_role("link")
        expect(totals.locator('[data-slot="nav-arrow"]')).to_have_count(
            totals.get_by_role("link").count()
        )
        first = links.first
        arrow = first.locator('[data-slot="nav-arrow"]')
        allocated.get_by_text("By resource").hover()
        expect(arrow).to_have_css("opacity", "0")
        first.hover()
        expect(arrow).to_have_css("opacity", "1")
        name = first.locator('[data-slot="nav-arrow"]').evaluate(
            "node => node.previousElementSibling.textContent"
        )
        first.click()
        expect(page).to_have_url(re.compile(r"/resource/[^/?]+\?env=production$"))
        expect(page.get_by_role("heading", name=name, exact=True)).to_be_visible()
        # Going back refetches the dashboard, and the page scrolls as the responses render.
        with (
            expect_rpc(page, "ListWorkspaceResources", lambda body: True),
            expect_rpc(page, "ListDeployments", lambda body: True),
            expect_rpc(page, "ListRegions", lambda body: True),
        ):
            page.go_back()
        expect(allocated).to_be_visible()
        assert allocated.evaluate("node => getComputedStyle(node).cursor") != "pointer"
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
        f"UPDATE placements SET ready_replicas=48 FROM resources WHERE placements.resource_id=resources.id AND resources.workspace_id='{workspace}' AND resources.slug='dashboard-0';",
    )
    page.reload()
    ready = page.get_by_label("us-ca-1: 48/48", exact=True)
    expect(ready).to_be_visible()
    ready.focus()
    expect(page.locator('[data-slot="tooltip-content"]')).to_have_text("us-ca-1: 48/48")
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
    with expect_rpc(
        page,
        "ListWorkspaceResources",
        lambda body: (
            body.get("query") == "dashboard-1"
            and body.get("environmentScoped") is True
            and body.get("includeFacets") is True
        ),
    ):
        section.get_by_role("textbox", name="Search resources", exact=True).fill(
            "dashboard-1"
        )
    expect(count).to_have_text("1")
    expect(everything).to_have_text(re.compile(r"^All\s*1$"))
    expect(section.get_by_role("link", name="dashboard-1", exact=True)).to_be_visible()


def traffic(page):
    global traffic_empty
    totals = page.get_by_role("region", name="Totals", exact=True)
    for empty in (False, True):
        traffic_empty = empty
        page.goto(state["ui_url"] + base + "?env=production")
        # 3 resources × 3 points × 10/s × 1440 s buckets (24h range) = 129.6K requests, 13K 5xx.
        requests, errors = ("—", "—") if empty else ("129.6K", "13K")
        for label, value in (("Requests", requests), ("Errors", errors)):
            card = totals.get_by_role("group", name=f"{label}: {value},")
            # The prior day's stub equals this day's.
            comparison = "No prior 24h data to compare" if empty else "0% vs prior 24h"
            expect(card.get_by_text(comparison, exact=True)).to_be_visible()
        if not empty:
            card = totals.get_by_role("group", name="Requests: 129.6K,")
            card.hover()
            expect(
                card.get_by_role("link", name=re.compile("^controls-pilot"))
            ).to_be_visible()
            markers = card.locator('[data-slot="deploy-marker"]')
            expect(markers.first).to_have_attribute(
                "aria-label", re.compile(r"deployed|deploys")
            )
            expect(
                card.locator('[data-slot="deploy-marker"][data-failed]')
            ).to_have_count(0)
            markers.first.hover()
            expect(page.get_by_text(re.compile(r"\d+ deploys"))).to_be_visible()
            for name in ("controls-retired (deleted)", "Deleted resource"):
                expect(card.get_by_text(name, exact=True)).to_be_visible()
                expect(
                    card.get_by_role("link", name=re.compile(re.escape(name)))
                ).to_have_count(0)
    traffic_empty = False
    sql(
        state,
        f"UPDATE deployments SET status='failed' WHERE resource_id IN (SELECT id FROM resources WHERE workspace_id='{workspace}');",
    )
    page.goto(state["ui_url"] + base + "?env=production")
    failed = totals.get_by_role("group", name="Requests: 129.6K,").locator(
        '[data-slot="deploy-marker"][data-failed]'
    )
    expect(failed.first).to_have_attribute("aria-label", re.compile(r"failed"))


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
        before_start = (
            datetime.fromisoformat(body["startTime"].replace("Z", "+00:00"))
            - timedelta(seconds=30)
        ).isoformat()
        series = [
            {
                "resourceId": rid,
                "points": [
                    {
                        "timestamp": stamp(now, timedelta(minutes=minute)),
                        "value": 0.0004 if i == 0 else (i + 1) / 20 * scale,
                    }
                    for minute, scale in ((50, 0.6), (30, 1.0), (10, 0.8))
                ]
                + [{"timestamp": before_start, "value": 0.1}],
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
    first_label = chart.locator(".recharts-xAxis-tick-labels text").first
    plot_left = chart.locator(".recharts-cartesian-grid line").first
    gap = first_label.bounding_box()["x"] - plot_left.bounding_box()["x"]
    assert abs(gap) <= 3, gap
    box = chart.bounding_box()
    chart.hover(position={"x": box["width"] * 0.5, "y": 60})
    rows = page.locator("[data-tooltip-row]")
    expect(rows).to_have_count(8)
    more = page.get_by_text(f"+{count - 8} more", exact=True)
    expect(more).to_be_visible()
    footer_gap = more.evaluate(
        "el => el.getBoundingClientRect().top - el.previousElementSibling.getBoundingClientRect().bottom"
    )
    assert footer_gap >= 6, footer_gap
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
    page.goto(state["ui_url"] + "/tokens?owner=user")
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
        f"INSERT INTO api_tokens (display_suffix,token_hash,name,entity_type,entity_id,scopes,created_by,expires_at) SELECT 'test','limit-display-' || n,'limit-display-' || n,'user','{user_id}','[]','{user_id}',NOW() + INTERVAL '1 day' FROM generate_series(1,9) n;",
    )
    limit = 11
    config_url = "**/loco.config.v1.ConfigService/GetConfig*"

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
            page.goto(state["ui_url"] + "/tokens?owner=user")
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
                expect(page).to_have_url(re.compile(r"[?&]scope=org(?:&[^#]*)?#s-lim$"))
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
        page.unroute_all(behavior="wait")
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
        with expect_rpc(
            page,
            "ListWorkspaceResources",
            lambda body: (
                body.get("orderBy") == "RESOURCE_ORDER_NAME"
                and body.get("descending") is True
            ),
        ):
            name.press("Enter")
        if route:
            expect(page).to_have_url(re.compile(r"[?&]sort=name\.desc"))
            page.reload()
            expect(table.get_by_role("row").nth(1)).to_contain_text("controls-zeta")
            name.press("Enter")
            expect(table.get_by_role("row").nth(1)).to_contain_text("controls-alpha")
            first_click_sorts_descending(table, "Size", "controls-zeta")
            table.get_by_role("button", name="Add a column", exact=True).click()
            created = page.get_by_role("menuitemcheckbox", name="Created")
            created.click()
            expect(created).to_have_attribute("aria-checked", "true")
            page.keyboard.press("Escape")
            first_click_sorts_descending(table, "Created", "controls-zeta")


def title_tooltip(page):
    page.goto(state["ui_url"] + base + "/resources?env=production")
    tip = page.get_by_role("tooltip")
    columns = page.get_by_role("button", name="Add a column", exact=True)
    started = time.monotonic()
    columns.hover()
    expect(tip).to_have_text("Add a column")
    assert time.monotonic() - started >= 0.3
    expect(columns).not_to_have_attribute("title", re.compile(".+"))
    expect(columns).to_have_attribute("aria-describedby", re.compile(r"\bloco-tip\b"))
    page.keyboard.press("Escape")
    expect(tip).to_have_count(0)
    expect(columns).not_to_have_attribute("aria-describedby", re.compile(".+"))
    page.mouse.move(0, 0)
    columns.focus()
    expect(tip).to_have_text("Add a column")
    expect(columns).to_have_attribute("title", "Add a column")
    columns.blur()
    expect(tip).to_have_count(0)
    page.evaluate(
        """() => {
            const fits = document.createElement("span");
            fits.id = "tip-fits";
            fits.title = fits.textContent = "Fits as it is";
            const cut = document.createElement("span");
            cut.id = "tip-cut";
            cut.style.cssText = "display:block;width:40px;overflow:hidden;white-space:nowrap;text-overflow:ellipsis";
            cut.textContent = "Cut short by its width";
            document.querySelector("main").prepend(fits, cut);
        }"""
    )
    page.locator("#tip-fits").hover()
    page.wait_for_timeout(700)
    expect(tip).to_have_count(0)
    page.locator("#tip-cut").hover()
    expect(tip).to_have_text("Cut short by its width")


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
    production = next(e["id"] for e in environments if e["slug"] == "production")
    sql(
        state,
        f"UPDATE resources SET name='Controls Pilot' WHERE id='{resource}';"
        f"UPDATE resources SET name='Controls Alpha' WHERE id='{sorted_resources[0]}';",
    )
    # One ready deploy at each metric point, so a click on any point offers one.
    deploys = [
        {
            "id": f"01a1c8b3-0000-7000-8000-0000000de90{i}",
            "resourceId": resource,
            "placements": [{"region": "us-ca-1"}],
            "environmentId": production,
            "createdAt": point["timestamp"],
            "startedAt": point["timestamp"],
            "spec": {
                "service": {
                    "build": {"image": "ghcr.io/acme/pilot:1.27"},
                    "regions": {"us-ca-1": {"cpu": "100m", "memory": "128Mi"}},
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
                if route.request.post_data_json.get("workspaceId") == workspace
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
    pilot_row = by_region.get_by_role("row").filter(has_text="Controls Pilot")
    expect(pilot_row).to_contain_text("0.035 vCPU 35%")
    # A live deployment sizes its region with its own limit, not the resource's.
    cluster, deployment = (
        "01a1c8b3-0000-7000-8000-0000000c1a51",
        "01a1c8b3-0000-7000-8000-0000000de9aa",
    )
    sql(state, evidence.cluster_row(cluster, "us-east-1"))
    try:
        evidence.seed_deployments(
            state,
            resource,
            [
                {
                    "id": deployment,
                    "environment": "production",
                    "spec": {
                        "build": {"image": "ghcr.io/acme/pilot:1.27"},
                        "regions": {
                            "us-east-1": {
                                "cpu": "500m",
                                "memory": "512Mi",
                                "minReplicas": 1,
                                "maxReplicas": 1,
                            }
                        },
                    },
                    "placements": {"us-east-1": {"cluster": cluster}},
                    # Started before the chart, so every point ran with its limit.
                    "age": "2 hours",
                }
            ],
        )
        page.reload()
        expect(pilot_row).to_contain_text("0.175 vCPU 35%")
    finally:
        sql(
            state,
            f"DELETE FROM placements WHERE deployment_id='{deployment}';"
            f"DELETE FROM deployments WHERE id='{deployment}';"
            f"DELETE FROM clusters WHERE id='{cluster}';",
        )
    page.reload()
    expect(pilot_row).to_contain_text("0.035 vCPU 35%")
    viewport = page.viewport_size
    page.set_viewport_size({"width": 1280, "height": viewport["height"]})
    region_cell = (
        by_region.get_by_role("row")
        .filter(has_text="Controls Pilot")
        .get_by_role("cell")
        .nth(1)
    )
    expect(region_cell).to_have_text("us-ca-1")
    text_lines = region_cell.evaluate(
        """node => {
            const range = document.createRange();
            range.selectNodeContents(node);
            return new Set([...range.getClientRects()].map(r => Math.round(r.top))).size;
        }"""
    )
    assert text_lines == 1, text_lines
    page.set_viewport_size(viewport)
    expect(
        by_region.get_by_role("columnheader", name="CPU (latest)", exact=True)
    ).to_have_attribute("aria-sort", "descending")
    by_region.get_by_role("button", name="Resource", exact=True).click()
    expect(
        by_region.get_by_role("columnheader", name="Resource", exact=True)
    ).to_have_attribute("aria-sort", "ascending")
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
    expect(menu.get_by_role("menuitem").first).to_contain_text("Logs around")
    expect(
        menu.get_by_role("menuitem", name=re.compile(r"^Events around"))
    ).to_contain_text("deploys, restarts, scaling")
    expect(menu.get_by_role("separator")).to_have_count(1)
    y_labels = chart.locator(
        ".recharts-yAxis-tick-labels .recharts-cartesian-axis-tick-value"
    ).all_text_contents()
    # The mocked CPU peaks at 55% of the limit, so the axis tops out at 120.
    assert y_labels == ["30", "60", "90"], y_labels
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
    page.set_viewport_size({"width": 1280, "height": 800})
    for root_font in ("16px", "20px"):
        page.evaluate(f"document.documentElement.style.fontSize = '{root_font}'")
        for target in (chart, memory):
            for fraction in (0.3, 0.95):
                box = target.bounding_box()
                click_x = box["x"] + box["width"] * fraction
                click_y = box["y"] + 60
                page.mouse.click(click_x, click_y)
                expect(menu).to_be_visible()
                page.wait_for_timeout(300)
                shown = menu.bounding_box()
                assert 0 <= shown["x"] and shown["x"] + shown["width"] <= 1280, shown
                near_left = abs(shown["x"] - click_x) <= 24
                near_right = abs(shown["x"] + shown["width"] - click_x) <= 24
                assert near_left or near_right, (root_font, fraction, shown, click_x)
                assert abs(shown["y"] - click_y) <= 24, (shown, click_y)
                page.keyboard.press("Escape")
                expect(menu).not_to_be_visible()
    page.evaluate("document.documentElement.style.fontSize = ''")
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
        f"UPDATE resources SET name=slug WHERE id IN ('{resource}','{sorted_resources[0]}');",
    )


out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with session(out) as browser:
    context, page, errors = open_page(browser, state=state, timezone_id="UTC")
    mock_telemetry(context)
    time_preferences(browser)
    volume_error(browser)
    expired_session(browser)
    metrics_error(browser)
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
    expect(indicator).to_be_visible()
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
    expect(personal).to_have_attribute("aria-pressed", "true")
    expect(personal.locator('[data-slot="segmented-indicator"]')).to_be_visible()
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
    name = page.get_by_role("textbox", name="Name", exact=True)
    expect(name).to_have_value("shared")
    # Scrolls until the page has moved.
    page.wait_for_function(
        "() => { window.scrollTo(0, document.documentElement.scrollHeight); return window.scrollY > 0; }"
    )
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
        typed_range_error,
        pin_needs_applied_query,
        metrics,
        query_enter,
        query_suggestions,
        volume,
        volume_axis,
        access_picker,
        resources,
        title_tooltip,
        resource_view_history,
        token_empty,
        limit_display,
        dashboard,
        dashboard_counts,
        traffic,
        metrics_many,
    ):
        check(page)
    assert not errors, errors
    context.close()
print("PASS: shared controls", flush=True)
