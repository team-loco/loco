import argparse
import atexit
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

repo = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description="Check compact controls, segmented motion and pinned scope navigation")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--capture-only", action="store_true")
parser.add_argument("--label", default="after")
parser.add_argument("--out", default=str(repo / "e2e/out/shared-controls"))
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="shared-controls", scenario=str(repo / "e2e/fixtures/settings-labels/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    print("Preview state:", args.state, flush=True)
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]
org = evidence.rpc(state["api_url"], owner["token"], "loco.org.v1.OrgService/ListUserOrgs", {"userId": owner["id"]})["orgs"][0]["id"]
resource = state.get("shared_controls_resource")
if resource is None:
    resource = evidence.rpc(state["api_url"], owner["token"], "loco.resource.v1.ResourceService/CreateResource", {"workspaceId": workspace, "name": "controls-pilot", "type": "RESOURCE_TYPE_SERVICE", "spec": {"service": {"regions": {"us-east-1": {"enabled": True, "cpu": "100m", "memory": "128Mi", "minReplicas": 1, "maxReplicas": 1}}}}})["resourceId"]
    state["shared_controls_resource"] = resource
    evidence.write_json(Path(args.state), state)
base = f"/org/{org}/wks/{workspace}"
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
results = []
with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    try:
        for theme in ("light", "dark"):
            for narrow in (False, True):
                context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme)
                auth = {"idToken": owner["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
                context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.THEME_KEY)},{json.dumps(theme)});")
                page = context.new_page()
                errors = []
                page.on("pageerror", lambda error: errors.append(str(error)))
                try:
                    page.goto(state["ui_url"] + base + "/resource/" + resource + "?env=production")
                    overview = page.get_by_role("button", name="Overview", exact=True)
                    variables = page.get_by_role("button", name="Variables", exact=True)
                    settings = page.get_by_role("button", name="Settings", exact=True)
                    expect(overview).to_have_attribute("aria-pressed", "true")
                    if not args.capture_only:
                        assert overview.bounding_box()["height"] == 28
                        assert page.get_by_role("button", name="Redeploy", exact=True).bounding_box()["height"] == 28
                        assert page.get_by_role("link", name="Logs", exact=True).bounding_box()["height"] == 28
                        indicator = page.locator('[data-slot="segmented-indicator"]').first
                        assert abs(indicator.bounding_box()["x"] - overview.bounding_box()["x"]) <= 1
                        variables.click()
                        expect(variables).to_have_attribute("aria-pressed", "true")
                        page.wait_for_timeout(250)
                        assert abs(indicator.bounding_box()["x"] - variables.bounding_box()["x"]) <= 1
                        variables.focus()
                        page.keyboard.press("ArrowRight")
                        expect(settings).to_be_focused()
                        page.keyboard.press("Space")
                        expect(settings).to_have_attribute("aria-pressed", "true")
                        page.emulate_media(reduced_motion="reduce")
                        overview.click()
                        page.wait_for_timeout(50)
                        assert abs(indicator.bounding_box()["x"] - overview.bounding_box()["x"]) <= 1
                        assert indicator.evaluate("node => getComputedStyle(node).transform") == "none"
                    overview.click()
                    page.wait_for_timeout(300)
                    suffix = f"{args.label}-{theme}{'-narrow' if narrow else ''}"
                    page.screenshot(path=str(out / f"resource-controls-{suffix}.png"), full_page=True)
                    page.goto(state["ui_url"] + base + "/settings?tab=ws")
                    page.get_by_role("textbox", name="Name", exact=True).wait_for()
                    page.evaluate("window.scrollTo(0, document.documentElement.scrollHeight)")
                    page.wait_for_timeout(300)
                    if not args.capture_only:
                        assert page.evaluate("window.scrollY") > 0
                        assert abs(page.locator('[data-slot="top-bar"]').bounding_box()["y"]) <= 1
                    page.screenshot(path=str(out / f"pinned-navigation-{suffix}.png"))
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
