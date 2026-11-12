import argparse
import atexit
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description="Test the toast that offers a reload once a newer dashboard build is served")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/update-toast"))
args = parser.parse_args()
os.chdir(repo)
module_spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(module_spec)
module_spec.loader.exec_module(evidence)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="after", scenario=str(repo / "tests/ui/fixtures/deployment-history/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
ui = state["ui_url"]
TITLE = "Loco has been updated"


def open_page(browser, theme, narrow):
    context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme)
    auth = {"idToken": state["users"]["owner"]["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
    context.add_init_script(
        f"localStorage.setItem({json.dumps(evidence.MODE_KEY)},{json.dumps(theme)});"
        f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));"
    )
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    return context, page, errors


def serve(page, version):
    page.unroute("**/version.json")
    page.route("**/version.json", lambda route: route.fulfill(content_type="application/json", body=json.dumps({"version": version})))


def check(page):
    # Coming back to the tab is one of the moments the dashboard checks for a newer build.
    with page.expect_response("**/version.json"):
        page.evaluate("document.dispatchEvent(new Event('visibilitychange'))")


cases = []
with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    try:
        for theme in evidence.THEMES:
            for narrow in (False, True):
                case = f"{theme}{'-narrow' if narrow else ''}"
                context, page, errors = open_page(browser, theme, narrow)
                try:
                    page.goto(ui + "/dashboard")
                    expect(page.get_by_role("button", name="Search and run commands")).to_be_visible()
                    running = page.locator('meta[name="loco-version"]').get_attribute("content")
                    toast = page.locator("[data-sonner-toast]").filter(has_text=TITLE)
                    serve(page, running)
                    check(page)
                    expect(toast).to_have_count(0)
                    serve(page, "sha-newer")
                    check(page)
                    expect(toast).to_be_visible()
                    reload = toast.get_by_role("button", name="Reload")
                    expect(reload).to_be_visible()
                    page.screenshot(path=out / f"toast-{case}.png", animations="disabled")
                    check(page)
                    expect(toast).to_have_count(1)
                    # Dismissed, it stays away until a different build is served.
                    toast.get_by_role("button", name="Close toast").click()
                    expect(toast).to_have_count(0)
                    check(page)
                    expect(toast).to_have_count(0)
                    serve(page, "sha-newest")
                    check(page)
                    expect(toast).to_be_visible()
                    # A rollback to the build this tab runs takes the toast away.
                    serve(page, running)
                    check(page)
                    expect(toast).to_have_count(0)
                    serve(page, "sha-newer")
                    check(page)
                    page.evaluate("window.beforeReload = true")
                    with page.expect_navigation():
                        reload.click()
                    assert page.evaluate("window.beforeReload") is None
                    assert not errors, errors
                    cases.append(case)
                except BaseException:
                    page.screenshot(path=out / f"failure-{case}.png", animations="disabled")
                    raise
                finally:
                    context.close()
    finally:
        browser.close()
print(f"PASS: update-toast cases {', '.join(cases)}")
