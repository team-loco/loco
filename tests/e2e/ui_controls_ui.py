import argparse
import atexit
import contextlib
import importlib.util
import io
import json
import os
import re
from datetime import UTC, datetime, timedelta
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description="Check observability controls, permission suggestions and resource sorting in Chromium")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--capture-only", action="store_true")
parser.add_argument("--label", default="after")
parser.add_argument("--out", default=str(repo / "tests/e2e/out/ui-controls"))
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="after", scenario=str(repo / "tests/e2e/fixtures/settings-labels/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
    print("Preview state:", args.state, flush=True)
state = evidence.load_state(args.state)
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]
org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';")
base = state["ui_url"] + f"/org/{org}/wks/{workspace}"
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
resource_ids = state.get("controls_resources")
if resource_ids is None:
    resource_ids = [
        evidence.rpc(state["api_url"], owner["token"], "loco.resource.v1.ResourceService/CreateResource", {
            "workspaceId": workspace, "name": name, "type": "RESOURCE_TYPE_SERVICE",
            "spec": {"service": {"regions": {"us-east-1": {"enabled": True, "cpu": "100m", "memory": "128Mi", "minReplicas": 1, "maxReplicas": 1}}}},
        })["resourceId"] for name in ("controls-alpha", "controls-zeta")
    ]
    state["controls_resources"] = resource_ids
    evidence.write_json(Path(args.state), state)


def log_entries():
    now = datetime.now(UTC)
    return [
        {"timestamp": (now - timedelta(seconds=i * 13 + 1)).isoformat().replace("+00:00", "Z"),
         "severity": ("INFO", "WARN", "ERROR", "DEBUG")[i % 4], "body": f"Request completed ({i + 1})",
         "resourceId": resource_ids[i % 2], "resourceAttributes": {"cloud.region": "us-east-1"}}
        for i in range(18)
    ]


def screenshot(locator, name, suffix):
    locator.screenshot(path=out / f"{name}-{suffix}.png", animations="disabled")


def observability(page, suffix):
    page.goto(base + "/observability?env=production&view=logs")
    search = page.locator('input[aria-label="Search and filter"]')
    search.fill("@lev")
    option = page.get_by_role("option", name=re.compile(r"^@level"))
    expect(option).to_be_visible()
    screenshot(page.get_by_role("listbox", name="Suggestions"), "log-query", suffix)
    if not args.capture_only:
        expect(search).to_have_attribute("aria-expanded", "true")
        search.press("Enter")
        expect(search).to_have_value("@level:")
        search.fill("@level:warn")
        search.press("Tab")
        expect(search).to_have_value("")
        search.press("Escape")
        levels = page.get_by_role("group", name="Log levels")
        warn = levels.get_by_role("button", name=re.compile(r"^Warn"))
        expect(warn).to_have_attribute("aria-pressed", "true")
        warn.focus()
        page.keyboard.press("ArrowLeft")
        error = levels.get_by_role("button", name=re.compile(r"^Error"))
        expect(error).to_be_focused()
        page.keyboard.press("Space")
        expect(error).to_have_attribute("aria-pressed", "true")
        expect(warn).to_have_attribute("aria-pressed", "true")
        slider = page.get_by_role("slider", name=re.compile(r"^Log volume"))
        slider.focus()
        page.keyboard.press("Home")
        expect(slider).to_have_attribute("aria-valuenow", "0")
        page.keyboard.press("ArrowRight")
        expect(slider).to_have_attribute("aria-valuenow", "1")
        assert slider.get_attribute("aria-valuetext")
        page.keyboard.press("Enter")
        expect(page).to_have_url(re.compile(r"[?&]from="))
        page.goto(base + "/observability?env=production&view=logs")
    page.get_by_role("button", name=re.compile(r"^Past ")).click()
    time_range = page.get_by_role("dialog", name="Time range")
    screenshot(time_range, "time-range", suffix)
    if not args.capture_only:
        date_trigger = time_range.get_by_role("button", name="From date")
        date_trigger.press("Space")
        calendar = page.get_by_role("dialog", name="From calendar")
        expect(calendar.get_by_role("grid")).to_be_visible()
        screenshot(calendar, "calendar", suffix)
        page.keyboard.press("Escape")
        expect(calendar).not_to_be_visible()
        expect(date_trigger).to_be_focused()
        expect(time_range).to_be_visible()
        date_trigger.click()
        calendar.get_by_role("grid").get_by_role("button", name=re.compile(r"selected", re.I)).focus()
        page.keyboard.press("ArrowLeft")
        page.keyboard.press("Enter")
        expect(calendar).not_to_be_visible()
        expect(time_range).to_be_visible()


def metrics(page, suffix):
    page.goto(base + "/observability?env=production&view=metrics")
    chart = page.locator('[aria-label="CPU over time"]')
    expect(chart.locator(".recharts-line")).to_be_visible()
    chart.click(position={"x": chart.bounding_box()["width"] / 2, "y": 60})
    menu = page.get_by_role("menu", name=re.compile(r"^Around"))
    expect(menu).to_be_visible()
    screenshot(menu, "metric-menu", suffix)
    if not args.capture_only:
        first, second = menu.get_by_role("menuitem").nth(0), menu.get_by_role("menuitem").nth(1)
        first.focus()
        page.keyboard.press("ArrowDown")
        expect(second).to_be_focused()
        page.keyboard.press("Escape")
        expect(menu).not_to_be_visible()


def tokens(page, suffix):
    page.goto(state["ui_url"] + "/tokens")
    page.get_by_role("button", name="New token", exact=True).first.click()
    search = page.locator('input[aria-label="Add a workspace or resource"]')
    search.fill("controls-")
    expect(page.get_by_text("controls-zeta", exact=True)).to_be_visible()
    screenshot(page.get_by_role("dialog"), "token-permissions", suffix)
    if not args.capture_only:
        expect(search).to_have_attribute("aria-expanded", "true")
        search.press("ArrowDown")
        search.press("Enter")
        expect(page.get_by_role("group", name="Level for controls-zeta")).to_be_visible()
        expect(search).to_have_value("")
        search.fill("no matching scope")
        expect(page.get_by_text("Nothing matches “no matching scope”.")).to_be_visible()
        search.press("Escape")
        expect(search).to_have_attribute("aria-expanded", "false")
        expect(page.get_by_role("dialog")).to_be_visible()


def resources(page, suffix):
    for route in ("/resources", ""):
        page.goto(base + route + "?env=production")
        table = page.get_by_role("table").filter(has=page.get_by_role("columnheader", name=re.compile(r"^Resource(?: [▲▼])?$"))).first
        name = table.get_by_role("button", name=re.compile(r"^Resource(?: [▲▼])?$"))
        name.press("Enter")
        expect(table.locator('th[aria-sort="ascending"]')).to_have_count(1)
        expect(table.get_by_role("columnheader", name=re.compile(r"^Resource(?: [▲▼])?$"))).to_have_attribute("aria-sort", "ascending")
        expect(table.get_by_role("row").nth(1)).to_contain_text("controls-alpha")
        name.press("Enter")
        expect(table.get_by_role("columnheader", name=re.compile(r"^Resource(?: [▲▼])?$"))).to_have_attribute("aria-sort", "descending")
        expect(table.get_by_role("row").nth(1)).to_contain_text("controls-zeta")
        if route:
            page.reload()
            expect(table.get_by_role("row").nth(1)).to_contain_text("controls-zeta")
            screenshot(table.locator(".."), "resource-sorting", suffix)


with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    try:
        for theme in ("light", "dark"):
            for narrow in (False, True):
                suffix = f"{args.label}-{theme}{'-narrow' if narrow else ''}"
                context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme, timezone_id="UTC")
                auth = {"idToken": owner["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
                context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.MODE_KEY)},{json.dumps(theme)});")
                logs = log_entries()
                context.route("**/loco.observability.v1.ObservabilityProxyService/QueryLogs", lambda route: route.fulfill(json={"entries": logs, "totalMatched": str(len(logs))}))
                points = [
                    {"timestamp": (datetime.now(UTC) - timedelta(minutes=minute)).isoformat().replace("+00:00", "Z"),
                     "value": value}
                    for minute, value in ((50, 0.25), (30, 0.55), (10, 0.35))
                ]
                context.route("**/loco.observability.v1.ObservabilityProxyService/QueryMetrics", lambda route: route.fulfill(json={"series": [{"resourceId": resource_ids[0], "points": points}]}))
                page = context.new_page()
                errors = []
                page.on("pageerror", lambda error: errors.append(str(error)))
                try:
                    observability(page, suffix)
                    metrics(page, suffix)
                    tokens(page, suffix)
                    resources(page, suffix)
                    assert not errors, errors
                except BaseException:
                    page.screenshot(path=out / f"failure-{suffix}.png", animations="disabled")
                    raise
                finally:
                    context.close()
    finally:
        browser.close()
print("Observability, permission suggestions and resource sorting passed in light and dark at desktop and narrow widths.")
