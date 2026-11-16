import argparse
import json
import re
import urllib.parse
import uuid
from datetime import UTC, datetime, timedelta
from pathlib import Path

from playwright.sync_api import expect
from harness import wait_for_query, evidence, load_state, open_page, repo, session

parser = argparse.ArgumentParser(description="Check compact controls, segmented motion and settings navigation")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/shared-controls"))
args = parser.parse_args()
state = load_state(args, 'shared-controls', 'tests/ui/fixtures/settings-labels/scenario.json')
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]
org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';")
resource = state.get("shared_controls_resource")
if resource is None:
    resource = evidence.rpc(state["api_url"], owner["token"], "loco.resource.v1.ResourceService/CreateResource", {"workspaceId": workspace, "name": "controls-pilot", "type": "RESOURCE_TYPE_SERVICE", "spec": {"service": {"routing": {"port": 8080}, "regions": {"us-east-1": {"enabled": True, "cpu": "100m", "memory": "128Mi", "minReplicas": 1, "maxReplicas": 1}}}}})["resourceId"]
    state["shared_controls_resource"] = resource
    evidence.write_json(Path(args.state), state)
sorted_resources = state.get("controls_resources")
if sorted_resources is None:
    sorted_resources = [
        evidence.rpc(state["api_url"], owner["token"], "loco.resource.v1.ResourceService/CreateResource", {
            "workspaceId": workspace, "name": name, "type": "RESOURCE_TYPE_SERVICE",
            "spec": {"service": {"routing": {"port": 8080}, "regions": {"us-east-1": {"enabled": True, "cpu": cpu, "memory": "128Mi", "minReplicas": 1, "maxReplicas": 1}}}},
        })["resourceId"] for name, cpu in (("controls-alpha", "100m"), ("controls-zeta", "200m"))
    ]
    state["controls_resources"] = sorted_resources
    evidence.write_json(Path(args.state), state)
base = f"/org/{org}/wks/{workspace}"


def stamp(delta):
    return (datetime.now(UTC) - delta).isoformat().replace("+00:00", "Z")


telemetry_points = []


def mock_telemetry(context):
    logs = [{"timestamp": stamp(timedelta(seconds=i * 13 + 1)), "severity": ("INFO", "WARN", "ERROR", "DEBUG")[i % 4], "body": f"Request completed ({i + 1})", "resourceId": resource, "resourceAttributes": {"cloud.region": "us-east-1"}} for i in range(18)]
    points = [{"timestamp": stamp(timedelta(minutes=minute)), "value": value} for minute, value in ((50, 0.25), (30, 0.55), (10, 0.35))]
    telemetry_points[:] = points
    context.route("**/loco.observability.v1.ObservabilityService/QueryLogs", lambda route: route.fulfill(json={"entries": logs, "totalMatched": str(len(logs))}))
    context.route("**/loco.observability.v1.ObservabilityService/QueryMetrics", lambda route: route.fulfill(json={"series": [{"resourceId": resource, "points": points}]}))


def log_levels(page):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs&q=" + urllib.parse.quote("@level:warn"))
    levels = page.get_by_role("group", name="Log levels")
    warn = levels.get_by_role("button", name=re.compile(r"^Warn"))
    error = levels.get_by_role("button", name=re.compile(r"^Error"))
    expect(warn).to_have_attribute("aria-pressed", "true")
    error.click()
    expect(error).to_have_attribute("aria-pressed", "true")
    wait_for_query(page, lambda query: query.get("q") == ["@level:warn @level:error"])
    warn.focus()
    page.keyboard.press("Space")
    expect(warn).to_have_attribute("aria-pressed", "false")
    wait_for_query(page, lambda query: query.get("q") == ["@level:error"])


def live_focus(page):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    page.wait_for_load_state("networkidle")
    live = page.get_by_role("button", name=re.compile(r"^(Live|Starting…)"))
    expect(live).to_have_attribute("aria-pressed", "false")
    # Hold the latest-logs request so the button stays in its pending state.
    held = []
    page.route("**/loco.observability.v1.ObservabilityService/QueryLogs", lambda route: held.append(route))
    live.focus()
    page.keyboard.press("Enter")
    expect(live).to_have_text("Starting…")
    expect(live).to_have_attribute("aria-busy", "true")
    page.keyboard.press("Enter")
    # Chromium moves focus off a disabled button on its next rendering update, not at once.
    page.evaluate("() => new Promise(requestAnimationFrame)")
    expect(live).to_be_focused()
    assert len(held) == 1, len(held)
    # fallback() to the context mock fails with "Route is already handled", so answer here.
    held[0].fulfill(json={"entries": [], "totalMatched": "0"})
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogs")
    expect(live).to_have_attribute("aria-pressed", "true")
    expect(live).to_be_focused()
    page.keyboard.press("Enter")
    expect(live).to_have_attribute("aria-pressed", "false")


def log_paging(page):
    many = [{"timestamp": stamp(timedelta(seconds=i * 7 + 1)), "severity": "INFO", "body": f"Paged line {i + 1}", "resourceId": resource, "resourceAttributes": {"cloud.region": "us-east-1"}, "logAttributes": {"http.request.method": "GET"}} for i in range(120)]
    page.route("**/loco.observability.v1.ObservabilityService/QueryLogs", lambda route: route.fulfill(json={"entries": many, "totalMatched": "0"}))
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs&size=25")
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
    page.get_by_role("group", name="Rows per page").get_by_role("button", name="250", exact=True).click()
    expect(pager_label).to_have_text("1–120 of 120 loaded")
    wait_for_query(page, lambda query: query.get("size") == ["250"])
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs&size=25&page=2")
    expect(pager_label).to_have_text("26–50 of 120 loaded")
    page.get_by_role("button", name=re.compile(r"Paged line 30$")).click()
    wait_for_query(page, lambda query: "log" in query)
    page.reload()
    detail = page.get_by_role("complementary", name="Log line")
    expect(detail).to_contain_text("Paged line 30")
    detail.get_by_role("button", name="More for this line", exact=True).click()
    expect(page.get_by_role("menuitem", name=re.compile(r"^View controls-pilot"))).to_be_visible()
    expect(page.get_by_role("menuitem", name=re.compile(r"^View trace"))).to_have_count(0)
    page.keyboard.press("Escape")
    page.get_by_role("button", name="Add a column", exact=True).click()
    page.get_by_role("menuitemcheckbox", name="Method", exact=True).click()
    page.keyboard.press("Escape")
    wait_for_query(page, lambda query: "method" in query.get("cols", [""])[0])
    expect(page.get_by_role("button", name=re.compile(r"GET Paged line 30$"))).to_have_count(1)
    page.get_by_role("separator", name="Resize column Resource").focus()
    page.keyboard.press("ArrowRight")
    expect(page).to_have_url(re.compile(r"widths=resource:156"))
    page.unroute("**/loco.observability.v1.ObservabilityService/QueryLogs")


def time_presets(page):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    page.get_by_role("button", name=re.compile(r"^Past ")).click()
    time_range = page.get_by_role("dialog", name="Time range")
    active = time_range.get_by_role("group", name="Presets").locator('[aria-pressed="true"]')
    expect(active).to_have_count(1)
    active.click()
    expect(time_range).not_to_be_visible()


def time_preferences(browser):
    context, page, errors = open_page(browser, state=state, timezone_id="America/New_York", storage={"loco-clock": "12"})
    mock_telemetry(context)
    start = int(datetime(2026, 11, 1, 6, 30, 12, 123000, tzinfo=UTC).timestamp() * 1000)
    end = int(datetime(2026, 11, 1, 6, 45, 23, 456000, tzinfo=UTC).timestamp() * 1000)
    page.goto(state["ui_url"] + base + f"/observability?view=logs&from={start}&to={end}")
    page.get_by_role("button", name=re.compile(r" (AM|PM).* (EST|EDT)")).click()
    picker = page.get_by_role("dialog", name="Time range")
    start_input = picker.get_by_role("textbox", name="From time", exact=True)
    expect(start_input).to_have_value(re.compile(r"\d+:\d{2} (AM|PM)"))
    picker.get_by_role("group", name="Time zone", exact=True).get_by_role("button", name="UTC", exact=True).click()
    expect(start_input).to_have_value("6:30 AM")
    zone = picker.get_by_role("group", name="Time zone", exact=True)
    zone.get_by_role("button", name=re.compile(r"^Local")).click()
    expect(start_input).to_have_value("1:30 AM")
    zone.get_by_role("button", name="UTC", exact=True).click()
    picker.get_by_role("button", name="Apply", exact=True).click()
    wait_for_query(page, lambda query: query.get("from") == [str(start)] and query.get("to") == [str(end)])
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
    popup.get_by_role("grid").get_by_role("button", name=re.compile(r"selected", re.I)).focus()
    page.keyboard.press("ArrowLeft")
    page.keyboard.press("Enter")
    expect(popup).not_to_be_visible()
    expect(time_range).to_be_visible()
    expect(date_trigger).not_to_have_text(before)


def query_enter(page):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs&q=" + urllib.parse.quote("@level:warn"))
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
    search.fill("@zz")
    expect(page.get_by_text("No matches", exact=True)).to_be_visible()
    search.press("Escape")
    expect(search).to_have_attribute("aria-expanded", "false")


def volume(page):
    end = int(datetime.now(UTC).timestamp() * 1000)
    page.goto(state["ui_url"] + base + f"/observability?env=production&view=logs&from={end - 3_600_000}&to={end}")
    slider = page.get_by_role("slider", name=re.compile(r"^Log volume"))
    expect(slider).to_be_visible()
    expect(page.get_by_text("Request completed (1)", exact=True)).to_be_visible()
    area = page.get_by_role("region", name="Log volume").locator('[data-slot="slider"]').locator("..")
    bars = area.locator("> div").first.locator("> div")
    count = bars.count()
    assert bars.first.locator("span").nth(1).evaluate("node => getComputedStyle(node).boxShadow") != "none"
    box = area.bounding_box()
    target = count - 5
    point = {"x": box["width"] * (target + 0.1) / count, "y": box["height"] / 2}
    area.hover(position=point)
    expect(area).to_have_attribute("title", re.compile(r"loaded"))
    area.click(position=point)
    expect(page).not_to_have_url(re.compile(f"from={end - 3_600_000}"))
    clicked = urllib.parse.parse_qs(urllib.parse.urlparse(page.url).query)["from"][0]
    page.goto(state["ui_url"] + base + f"/observability?env=production&view=logs&from={end - 3_600_000}&to={end}")
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
    assert urllib.parse.parse_qs(urllib.parse.urlparse(page.url).query)["from"][0] == clicked


def dashboard(page):
    page.set_viewport_size(evidence.DESKTOP)
    if not state.get("dashboard_resources"):
        environment = next(e["id"] for e in evidence.rpc(state["api_url"], owner["token"], "loco.environment.v1.EnvironmentService/ListEnvironments", {"workspaceId": workspace})["environments"] if e["name"] == "production")
        cluster = str(uuid.uuid4())
        sql = [f"INSERT INTO clusters(id,name,region,provider,is_active,is_default,health_status,last_heartbeat,tier) VALUES ('{cluster}','dashboard-pilot','us-east-1','fixture',true,false,'healthy',NOW(),'production');"]
        for index in range(7):
            resource_id = evidence.rpc(state["api_url"], owner["token"], "loco.resource.v1.ResourceService/CreateResource", {"workspaceId": workspace, "name": f"dashboard-{index}", "type": "RESOURCE_TYPE_SERVICE", "spec": {"service": {"routing": {"port": 8080}, "regions": {"us-east-1": {"enabled": True, "cpu": "100m", "memory": "128Mi"}}}}})["resourceId"]
            deployment = str(uuid.uuid4())
            runtime = evidence.sql_literal(json.dumps({"build": {"type": "image", "image": "example.invalid/dashboard:1"}, "cpu": f"{(index + 1) * 100}m", "memory": "128Mi", "minReplicas": 1, "maxReplicas": 1, "port": 8080}))
            sql.append(f"INSERT INTO deployments(actor_type,actor_id,method,id,resource_id,cluster_id,region,replicas,status,is_active,message,environment_id,spec,spec_version,started_at) VALUES ('user','{owner['id']}','cli','{deployment}','{resource_id}','{cluster}','us-east-1',1,'running',true,'Dashboard pilot','{environment}',{runtime},1,NOW());")
            sql.append(f"INSERT INTO placements(resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,ready,ready_replicas,status_phase,status_updated_at) VALUES ('{resource_id}','{cluster}','us-east-1','{deployment}','{environment}','{{}}',true,1,'Ready',NOW());")
        evidence.psql(state["container"], "\n".join(sql))
        state["dashboard_resources"] = True
        evidence.write_json(Path(args.state), state)
    evidence.psql(state["container"], f"UPDATE deployments SET replicas=CASE WHEN resources.name='dashboard-0' THEN 48 ELSE 12 END FROM resources WHERE deployments.resource_id=resources.id AND resources.workspace_id='{workspace}' AND resources.name IN ('dashboard-0','dashboard-1'); UPDATE placements SET ready_replicas=CASE WHEN resources.name='dashboard-0' THEN 47 ELSE 12 END, ready=resources.name<>'dashboard-0', status_phase=CASE WHEN resources.name='dashboard-0' THEN 'Failed' ELSE 'Ready' END FROM resources WHERE placements.resource_id=resources.id AND resources.workspace_id='{workspace}' AND resources.name IN ('dashboard-0','dashboard-1');")
    for color, mode in ((color, mode) for color in ("loco", "catppuccin", "neobrutal") for mode in ("light", "dark")):
        page.evaluate("color => localStorage.setItem('loco:color:v1', color)", color)
        page.goto(state["ui_url"] + base + "?env=production")
        # The authentication init script starts every navigation in light mode.
        if mode == "dark":
            page.get_by_role("button").filter(has_text="Olivia Owner").click()
            page.get_by_role("menuitem", name="Dark mode", exact=True).click()
            page.keyboard.press("Escape")
            expect(page.locator("html")).to_have_class(re.compile(r"\bdark\b"))
        else:
            expect(page.locator("html")).not_to_have_class(re.compile(r"\bdark\b"))
        replicas = page.get_by_label("47 of 48 ready · 1 starting\nus-east-1: 47/48", exact=True)
        expect(replicas).to_be_visible()
        expect(replicas).to_contain_text("47/48")
        full_row = page.get_by_role("region", name="Resources", exact=True).get_by_role("row").filter(has=page.get_by_role("link", name="dashboard-1", exact=True))
        expect(full_row).to_contain_text("12/12")
        bounds = full_row.evaluate("""row => {
            const count = [...row.querySelectorAll('span')].find(n => n.children.length === 0 && n.textContent === '12/12');
            const cell = count.closest('td');
            return {countRight: count.getBoundingClientRect().right, cellRight: cell.getBoundingClientRect().right,
                trafficLeft: cell.nextElementSibling.getBoundingClientRect().left};
        }""")
        assert bounds["countRight"] + 10 <= bounds["cellRight"] <= bounds["trafficLeft"], bounds
        replicas.focus()
        expect(page.locator('[data-slot="tooltip-content"]')).to_contain_text("us-east-1: 47/48")
        page.keyboard.press("Escape")
        totals = page.get_by_role("region", name="Totals", exact=True)
        allocated = page.get_by_role("group", name=re.compile("^Allocated:"))
        expect(allocated).to_be_visible()
        cards = totals.locator(":scope > div")
        heights = cards.evaluate_all("nodes => nodes.map(n => n.getBoundingClientRect().height)")
        assert max(heights) - min(heights) <= 1, heights
        allocated.hover()
        strip = allocated.locator('[data-slot="allocation-strip"]')
        expect(strip.locator(":scope > span")).to_have_count(6)
        expect(strip.locator("[data-rest]")).to_have_count(1)
        bars = allocated.locator('[data-slot="allocation-bar"] > span')
        expect(bars).to_have_count(5)
        if color == "neobrutal":
            expect(strip).to_have_css("border-top-width", "2px")
            expect(allocated.locator('[data-slot="allocation-bar"]').first).to_have_css("border-top-width", "2px")
        colors = strip.locator(":scope > span:not([data-rest])").evaluate_all("nodes => nodes.map(n => getComputedStyle(n).backgroundColor)")
        assert len(set(colors)) == 5, colors
        assert bars.evaluate_all("nodes => nodes.map(n => getComputedStyle(n).backgroundColor)") == colors
        expect(page.get_by_label("Spend: not available yet")).to_contain_text("soon")
        page.get_by_role("button").filter(has_text="Olivia Owner").click()
        next_mode = "Dark" if mode == "light" else "System"
        action = page.get_by_role("menuitem", name=next_mode + " mode", exact=True)
        icon = action.locator(".theme-icon")
        ink = icon.evaluate("node => getComputedStyle(node.parentElement).color")
        expect(icon).to_have_css("color", ink)
        if next_mode == "Dark":
            assert ink in icon.evaluate("node => getComputedStyle(node).boxShadow")
        else:
            expect(icon).to_have_css("border-top-color", ink)
        action.click()
        assert page.evaluate("localStorage.getItem('loco:mode:v1')") == ("dark" if next_mode == "Dark" else None)
        page.keyboard.press("Escape")
    evidence.psql(state["container"], f"UPDATE placements SET ready_replicas=48, ready=true, status_phase='Ready' FROM resources WHERE placements.resource_id=resources.id AND resources.workspace_id='{workspace}' AND resources.name='dashboard-0';")
    page.reload()
    ready = page.get_by_label("48 of 48 ready\nus-east-1: 48/48", exact=True)
    expect(ready).to_be_visible()
    ready.focus()
    expect(page.locator('[data-slot="tooltip-content"]')).to_have_text("48 of 48 ready us-east-1: 48/48")
    page.keyboard.press("Escape")
    page.evaluate("localStorage.setItem('loco:color:v1', 'loco'); localStorage.setItem('loco:mode:v1', 'light')")


def limit_display(page):
    user_id = owner["id"]
    evidence.psql(state["container"], f"INSERT INTO api_tokens (display_suffix,token_hash,name,entity_type,entity_id,scopes,created_by,expires_at) SELECT 'test','limit-display-' || n,'limit-display-' || n,'user','{user_id}','[]','{user_id}',NOW() - INTERVAL '1 day' FROM generate_series(1,9) n;")
    limit = 11
    config_url = "**/loco.config.v1.ConfigService/GetConfig"

    def config(route):
        response = route.fetch()
        data = response.json()
        data["countLimits"]["tokensPerEntity"] = limit
        route.fulfill(response=response, json=data)

    page.route(config_url, config)
    try:
        for limit, label in ((11, None), (10, "9 / 10"), (9, "9 / 9")):
            page.goto(state["ui_url"] + "/tokens?owner=personal")
            heading = page.get_by_role("heading", name=re.compile("^Tokens"))
            expect(page.get_by_text("limit-display-1", exact=True)).to_be_visible()
            badge = heading.locator('[data-slot="badge"]')
            if label is None:
                expect(badge).to_have_count(0)
            else:
                expect(badge).to_have_text(label)
                expect(page.get_by_text(label, exact=True)).to_have_count(1)
            create = page.get_by_role("button", name="New token", exact=True).first
            if limit == 9:
                expect(create).to_be_disabled()
                create.hover(force=True)
                expect(page.get_by_text("You've reached the limit of 9 personal tokens. Delete one, or ask a Loco admin to raise it.", exact=True)).to_be_visible()
            else:
                expect(create).to_be_enabled()
    finally:
        page.unroute(config_url, config)
        evidence.psql(state["container"], f"DELETE FROM api_tokens WHERE entity_id='{user_id}' AND name LIKE 'limit-display-%';")


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
    page.locator(f'[id={json.dumps(search.get_attribute("aria-activedescendant"))}]').hover()
    page.mouse.move(0, 0)
    expect(search).not_to_have_attribute("aria-activedescendant", re.compile(r".+"))
    search.press("Enter")
    expect(page.get_by_role("group", name=re.compile(r"^Level for controls-"))).to_have_count(1)
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


def dropdown_cursors(page):
    page.goto(state["ui_url"] + base + "/resources?env=production")
    trigger = page.get_by_role("button", name="New resource", exact=True)
    trigger.hover()
    expect(trigger).to_have_css("cursor", "pointer")
    trigger.click()
    menu = page.get_by_role("menu")
    enabled = menu.get_by_role("menuitem", name="Service", exact=True)
    disabled = menu.get_by_role("menuitem", name=re.compile(r"^Database"))
    enabled.hover()
    expect(enabled).to_have_css("cursor", "pointer")
    disabled.hover()
    expect(disabled).to_be_disabled()
    expect(disabled).to_have_css("cursor", "not-allowed")
    page.keyboard.press("Escape")


def resources(page):
    for route in ("/resources", ""):
        page.goto(state["ui_url"] + base + route + "?env=production")
        table = page.get_by_role("table").filter(has=page.get_by_role("columnheader", name="Resource", exact=True)).first
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


def metrics(page):
    environments = evidence.rpc(state["api_url"], owner["token"], "loco.environment.v1.EnvironmentService/ListEnvironments", {"workspaceId": workspace})["environments"]
    production = next(e["id"] for e in environments if e["name"] == "production")
    # One deploy at each metric point, so a click on any point offers one.
    deploys = [{"id": f"01a1c8b3-0000-7000-8000-0000000de90{i}", "resourceId": resource, "region": "us-east-1", "environmentId": production, "createdAt": point["timestamp"], "spec": {"service": {"build": {"image": "ghcr.io/acme/pilot:1.27"}}}} for i, point in enumerate(telemetry_points)]
    page.route("**/loco.deployment.v1.DeploymentService/ListDeployments", lambda route: route.fulfill(json={"deployments": deploys if route.request.post_data_json.get("resourceId") == resource else []}))
    page.goto(state["ui_url"] + base + "/observability?env=production&view=metrics")
    chart = page.locator('[aria-label="CPU over time"]')
    expect(chart.locator(".recharts-line")).to_be_visible()
    expect(chart.get_by_text("controls-pilot 1.27").first).to_be_visible()
    memory = page.locator('[aria-label="Memory over time"]')
    chart.hover(position={"x": chart.bounding_box()["width"] * 0.8, "y": 60})
    expect(memory.locator(".recharts-tooltip-cursor")).to_have_count(1)
    by_region = page.get_by_role("region", name="By resource and region")
    expect(by_region.get_by_role("columnheader", name="CPU ↓")).to_have_attribute("aria-sort", "descending")
    by_region.get_by_role("button", name="Resource", exact=True).click()
    expect(by_region.get_by_role("columnheader", name="Resource ↑")).to_have_attribute("aria-sort", "ascending")
    by_region.get_by_role("row").filter(has_text="controls-alpha").get_by_role("cell").nth(1).click()
    expect(page).to_have_url(re.compile(r"resource:controls-alpha"))
    page.goto(state["ui_url"] + base + "/observability?env=production&view=metrics")
    expect(chart.locator(".recharts-line")).to_be_visible()
    chart.click(position={"x": chart.bounding_box()["width"] / 2, "y": 60})
    menu = page.get_by_role("menu", name=re.compile(r"^Around"))
    expect(menu).to_be_visible()
    expect(menu.get_by_role("menuitem", name=re.compile(r"^Deploy 1\.27")).first).to_be_visible()
    first, second = menu.get_by_role("menuitem").nth(0), menu.get_by_role("menuitem").nth(1)
    first.focus()
    page.keyboard.press("ArrowDown")
    expect(second).to_be_focused()
    page.keyboard.press("Escape")
    expect(menu).not_to_be_visible()
    chart.click(position={"x": chart.bounding_box()["width"] / 2, "y": 60})
    expect(menu).to_be_visible()
    assert page.evaluate("getComputedStyle(document.documentElement).overflow + getComputedStyle(document.body).overflow").count("hidden") == 0
    page.get_by_role("button", name="Earlier", exact=True).click()
    expect(menu).not_to_be_visible()
    expect(page).to_have_url(re.compile(r"[?&]from="))
    page.unroute("**/loco.deployment.v1.DeploymentService/ListDeployments")

out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
results = []
with session(out) as browser:
    try:
        context, page, errors = open_page(browser, state=state, timezone_id="UTC")
        mock_telemetry(context)
        time_preferences(browser)
        try:
            page.goto(state["ui_url"] + base + "/resource/" + resource + "?env=production")
            dropdown_cursors(page)
            page.goto(state["ui_url"] + base + "/settings?scope=workspace")
            group = page.get_by_role("group", name="Settings for", exact=True)
            workspace_button = group.get_by_role("button", name="Workspace", exact=True)
            personal = group.get_by_role("button", name="Personal", exact=True)
            expect(workspace_button).to_have_attribute("aria-pressed", "true")
            indicator = group.locator('[data-slot="segmented-indicator"]').first
            assert abs(indicator.bounding_box()["x"] - workspace_button.bounding_box()["x"]) <= 1
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
            page.wait_for_function("window.indicatorSampleDone")
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
            page.wait_for_function("window.scrollY > 0")
            name = page.get_by_role("textbox", name="Name", exact=True)
            name.focus()
            expect(name).to_be_focused()
            name.evaluate("node => node.scrollIntoView({block: 'nearest'})")
            box = name.bounding_box()
            assert 0 <= box["y"] and box["y"] + box["height"] <= page.viewport_size["height"]
            for check in (log_levels, log_paging, live_focus, time_presets, calendar, metrics, query_enter, query_suggestions, volume, access_picker, resources, limit_display, dashboard):
                check(page)
            assert not errors, errors
            results.append({"passed": True})
        except BaseException:
            page.screenshot(path=str(out / "failure.png"), full_page=True)
            raise
        finally:
            context.close()
    finally:
        browser.close()
(out / "results.json").write_text(json.dumps(results, indent=2) + "\n")
print(json.dumps(results, indent=2))
