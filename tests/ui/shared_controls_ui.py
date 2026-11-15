import argparse
import json
import re
import urllib.parse
from datetime import UTC, datetime, timedelta
from pathlib import Path

from playwright.sync_api import expect
from harness import wait_for_query, authenticate, evidence, load_state, repo, session

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


def log_levels(page, suffix):
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


def live_focus(page, suffix):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
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


def log_paging(page, suffix):
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


def time_presets(page, suffix):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    page.get_by_role("button", name=re.compile(r"^Past ")).click()
    time_range = page.get_by_role("dialog", name="Time range")
    active = time_range.get_by_role("group", name="Presets").locator('[aria-pressed="true"]')
    expect(active).to_have_count(1)
    active.click()
    expect(time_range).not_to_be_visible()


def calendar(page, suffix):
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


def query_enter(page, suffix):
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


def query_suggestions(page, suffix):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs")
    search = page.locator('input[aria-label="Search and filter"]')
    search.fill("@lev")
    expect(page.get_by_role("option", name=re.compile(r"^@level"))).to_be_visible()
    suggestions = page.get_by_role("listbox", name="Suggestions")
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


def volume(page, suffix):
    end = int(datetime.now(UTC).timestamp() * 1000)
    page.goto(state["ui_url"] + base + f"/observability?env=production&view=logs&from={end - 3_600_000}&to={end}")
    slider = page.get_by_role("slider", name=re.compile(r"^Log volume"))
    expect(slider).to_be_visible()
    expect(page.get_by_text("Request completed (1)", exact=True)).to_be_visible()
    area = page.get_by_role("region", name="Log volume").locator('[data-slot="slider"]').locator("..")
    bars = area.locator("> div").first.locator("> div")
    count = bars.count()
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


def access_picker(page, suffix):
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


def dropdown_cursors(page, suffix):
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


def resources(page, suffix):
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


def metrics(page, suffix):
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
        theme = "light"
        narrow = False
        context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme, timezone_id="UTC")
        mock_telemetry(context)
        authenticate(context, owner['token'], theme)
        page = context.new_page()
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        try:
            page.goto(state["ui_url"] + base + "/resource/" + resource + "?env=production")
            suffix = f"{theme}{'-narrow' if narrow else ''}"
            dropdown_cursors(page, suffix)
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
            modes.get_by_role("radio", name="Light" if theme == "dark" else "Dark", exact=True).click()
            (expect(html).not_to_have_class if theme == "dark" else expect(html).to_have_class)(dark_class)
            modes.get_by_role("radio", name="System", exact=True).click()
            (expect(html).to_have_class if theme == "dark" else expect(html).not_to_have_class)(dark_class)
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
            for check in (log_levels, log_paging, live_focus, time_presets, calendar, metrics, query_enter, query_suggestions, volume, access_picker, resources):
                check(page, suffix)
            assert not errors, errors
            results.append({"theme": theme, "narrow": narrow, "passed": True})
        except BaseException:
            page.screenshot(path=str(out / f"failure-{theme}-{narrow}.png"), full_page=True)
            raise
        finally:
            context.close()
    finally:
        browser.close()
(out / "results.json").write_text(json.dumps(results, indent=2) + "\n")
print(json.dumps(results, indent=2))
