import argparse
import atexit
import contextlib
import importlib.util
import io
import json
import os
import re
import urllib.parse
from datetime import UTC, datetime, timedelta
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description="Check compact controls, segmented motion and settings navigation")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--capture-only", action="store_true")
parser.add_argument("--label", default="after")
parser.add_argument("--out", default=str(repo / "tests/e2e/out/shared-controls"))
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="shared-controls", scenario=str(repo / "tests/e2e/fixtures/settings-labels/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    print("Preview state:", args.state, flush=True)
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]
org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';")
resource = state.get("shared_controls_resource")
if resource is None:
    resource = evidence.rpc(state["api_url"], owner["token"], "loco.resource.v1.ResourceService/CreateResource", {"workspaceId": workspace, "name": "controls-pilot", "type": "RESOURCE_TYPE_SERVICE", "spec": {"service": {"regions": {"us-east-1": {"enabled": True, "cpu": "100m", "memory": "128Mi", "minReplicas": 1, "maxReplicas": 1}}}}})["resourceId"]
    state["shared_controls_resource"] = resource
    evidence.write_json(Path(args.state), state)
base = f"/org/{org}/wks/{workspace}"


def stamp(delta):
    return (datetime.now(UTC) - delta).isoformat().replace("+00:00", "Z")


def mock_telemetry(context):
    logs = [{"timestamp": stamp(timedelta(seconds=i * 13 + 1)), "severity": ("INFO", "WARN", "ERROR", "DEBUG")[i % 4], "body": f"Request completed ({i + 1})", "resourceId": resource, "resourceAttributes": {"cloud.region": "us-east-1"}} for i in range(18)]
    points = [{"timestamp": stamp(timedelta(minutes=minute)), "value": value} for minute, value in ((50, 0.25), (30, 0.55), (10, 0.35))]
    context.route("**/loco.observability.v1.ObservabilityProxyService/QueryLogs", lambda route: route.fulfill(json={"entries": logs, "totalMatched": str(len(logs))}))
    context.route("**/loco.observability.v1.ObservabilityProxyService/QueryMetrics", lambda route: route.fulfill(json={"series": [{"resourceId": resource, "points": points}]}))


def query_param(page):
    return urllib.parse.parse_qs(urllib.parse.urlparse(page.url).query).get("q", [""])[0]


def log_levels(page, suffix):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=logs&q=" + urllib.parse.quote("@level:warn"))
    levels = page.get_by_role("group", name="Log levels")
    warn = levels.get_by_role("button", name=re.compile(r"^Warn"))
    error = levels.get_by_role("button", name=re.compile(r"^Error"))
    expect(warn).to_have_attribute("aria-pressed", "true")
    error.click()
    expect(error).to_have_attribute("aria-pressed", "true")
    assert query_param(page) == "@level:warn @level:error", query_param(page)
    warn.focus()
    page.keyboard.press("Space")
    expect(warn).to_have_attribute("aria-pressed", "false")
    assert query_param(page) == "@level:error", query_param(page)
    page.screenshot(path=str(out / f"log-levels-{suffix}.png"))


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
    popup.screenshot(path=str(out / f"calendar-{suffix}.png"), animations="disabled")
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


def metrics(page, suffix):
    page.goto(state["ui_url"] + base + "/observability?env=production&view=metrics")
    chart = page.locator('[aria-label="CPU over time"]')
    expect(chart.locator(".recharts-line")).to_be_visible()
    chart.click(position={"x": chart.bounding_box()["width"] / 2, "y": 60})
    menu = page.get_by_role("menu", name=re.compile(r"^Around"))
    expect(menu).to_be_visible()
    menu.screenshot(path=str(out / f"metric-menu-{suffix}.png"), animations="disabled")
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

out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
results = []
with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    try:
        for theme in ("light", "dark"):
            for narrow in (False, True):
                context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme, timezone_id="UTC")
                mock_telemetry(context)
                auth = {"idToken": owner["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
                context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.MODE_KEY)},{json.dumps(theme)});")
                page = context.new_page()
                errors = []
                page.on("pageerror", lambda error: errors.append(str(error)))
                try:
                    page.goto(state["ui_url"] + base + "/resource/" + resource + "?env=production")
                    if not args.capture_only:
                        assert page.get_by_role("button", name="Events", exact=True).bounding_box()["height"] == 32
                        assert page.get_by_role("button", name="Redeploy", exact=True).bounding_box()["height"] == 32
                    suffix = f"{args.label}-{theme}{'-narrow' if narrow else ''}"
                    page.screenshot(path=str(out / f"resource-controls-{suffix}.png"), full_page=True)
                    page.goto(state["ui_url"] + base + "/settings?scope=workspace")
                    group = page.get_by_role("group", name="Settings for", exact=True)
                    workspace_button = group.get_by_role("button", name="Workspace", exact=True)
                    personal = group.get_by_role("button", name="Personal", exact=True)
                    expect(workspace_button).to_have_attribute("aria-pressed", "true")
                    if not args.capture_only:
                        assert workspace_button.bounding_box()["height"] == 28
                        indicator = group.locator('[data-slot="segmented-indicator"]').first
                        assert abs(indicator.bounding_box()["x"] - workspace_button.bounding_box()["x"]) <= 1
                        personal.evaluate("""node => {
                            window.indicatorPositions = [];
                            const group = node.parentElement;
                            const started = performance.now();
                            const sample = () => {
                                window.indicatorPositions.push(group.querySelector('[data-slot="segmented-indicator"]').getBoundingClientRect().x);
                                if (performance.now() - started < 220) requestAnimationFrame(sample);
                            };
                            node.click();
                            requestAnimationFrame(sample);
                        }""")
                        expect(personal).to_have_attribute("aria-pressed", "true")
                        page.wait_for_timeout(250)
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
                        page.wait_for_timeout(50)
                        assert abs(indicator.bounding_box()["x"] - personal.bounding_box()["x"]) <= 1
                        assert indicator.evaluate("node => getComputedStyle(node).transform") == "none"
                    if not args.capture_only:
                        personal.click()
                        colours = page.get_by_role("radiogroup", name="Colour theme")
                        loco = colours.get_by_role("radio", name="Loco", exact=True)
                        loco.focus()
                        page.keyboard.press("ArrowRight")
                        catppuccin = colours.get_by_role("radio", name="Catppuccin", exact=True)
                        expect(catppuccin).to_be_focused()
                        expect(catppuccin).to_be_checked()
                        radio_classes = catppuccin.get_attribute("class").split()
                        assert "rounded-card" in radio_classes and "rounded-full" not in radio_classes, radio_classes
                        page.keyboard.press("ArrowLeft")
                        expect(loco).to_be_checked()
                    workspace_button.click()
                    page.wait_for_timeout(300)
                    page.get_by_role("textbox", name="Name", exact=True).wait_for()
                    page.evaluate("window.scrollTo(0, document.documentElement.scrollHeight)")
                    page.wait_for_timeout(300)
                    if not args.capture_only:
                        assert page.evaluate("window.scrollY") > 0
                    page.screenshot(path=str(out / f"settings-navigation-{suffix}.png"))
                    if not args.capture_only:
                        name = page.get_by_role("textbox", name="Name", exact=True)
                        name.focus()
                        page.wait_for_timeout(300)
                        expect(name).to_be_focused()
                        box = name.bounding_box()
                        assert 0 <= box["y"] and box["y"] + box["height"] <= page.viewport_size["height"]
                    if not args.capture_only:
                        for check in (log_levels, time_presets, calendar, metrics):
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
