import argparse
import atexit
import contextlib
import importlib.util
import io
import json
import os
import re
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description="Check settings field names, focus and descriptions in the production UI")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/e2e/out/settings-labels"))
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="settings-labels", scenario=str(repo / "tests/e2e/fixtures/settings-labels/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    print("Preview state:", args.state, flush=True)
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
orgs = evidence.rpc(state["api_url"], state["users"]["owner"]["token"], "loco.org.v1.OrgService/ListUserOrgs", {"userId": state["users"]["owner"]["id"]})
org_ids = {org["name"]: org["id"] for org in orgs["orgs"]}
org_id = org_ids["acme"]
workspace_id = state["workspaces"]["acme/shared"]
base = f"/org/{org_id}/wks/{workspace_id}/settings"
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
results = []


def descriptions(field):
    return field.evaluate("(node) => (node.getAttribute('aria-describedby') || '').split(/\\s+/).filter(Boolean).map(id => { const hint = document.getElementById(id); if (!hint) throw new Error('Missing describedby target'); return hint.textContent; })")


def check_webhooks(page):
    new_webhook = page.get_by_role("button", name="New webhook", exact=True)
    new_webhook.click()
    page.keyboard.press("Escape")
    expect(new_webhook).to_be_focused()
    page.keyboard.press("Enter")
    page.get_by_role("dialog").get_by_role("button", name="Cancel", exact=True).click()
    expect(new_webhook).to_be_focused()
    new_webhook.click()
    url = page.get_by_role("textbox", name="Endpoint URL", exact=True)
    assert url.evaluate("node => node.labels.length") == 1
    expect(url).to_be_focused()
    assert descriptions(url) == []
    create = page.get_by_role("button", name="Create webhook", exact=True)
    url.fill("https://user:secret@hooks.invalid/loco")
    create.click()
    rejection = "Use an https URL with a host name."
    page.get_by_text(rejection, exact=True).wait_for()
    assert url.get_attribute("aria-invalid") == "true"
    assert rejection in descriptions(url)
    endpoint = "https://hooks.invalid/loco"
    url.fill(endpoint)
    assert url.get_attribute("aria-invalid") == "false"
    assert rejection not in descriptions(url)
    page.get_by_role("button", name="Choose events", exact=True).click()
    assert create.is_disabled()
    groups = page.get_by_role("group", name="Event groups", exact=True)
    builds = groups.get_by_role("button", name=re.compile(r"^Builds "))
    deployments = groups.get_by_role("button", name=re.compile(r"^Deployments "))
    expect(builds).to_have_attribute("aria-pressed", "false")
    builds.click()
    expect(builds).to_have_attribute("aria-pressed", "true")
    expect(builds).to_have_accessible_name("Builds all")
    expect(builds.get_by_text("all", exact=True)).to_be_visible()
    build_events = page.get_by_role("group", name="Builds events", exact=True)
    failed = build_events.get_by_role("button", name="build.failed", exact=True)
    expect(failed).to_have_attribute("aria-pressed", "true")
    failed.click()
    expect(failed).to_have_attribute("aria-pressed", "false")
    expect(builds).to_have_attribute("aria-pressed", "true")
    expect(builds).to_have_accessible_name(re.compile(r"^Builds \d+/\d+$"))
    chosen, total = builds.get_attribute("aria-label").removeprefix("Builds ").split("/")
    assert int(chosen) == int(total) - 1
    expect(builds.get_by_text(f"{chosen}/{total}", exact=True)).to_be_visible()
    deployments.click()
    expect(deployments).to_have_attribute("aria-pressed", "true")
    page.get_by_role("group", name="Deployments events", exact=True).wait_for()
    expect(build_events).to_be_hidden()
    deployments.click()
    expect(deployments).to_have_attribute("aria-pressed", "false")
    expect(page.get_by_role("group", name="Deployments events", exact=True)).to_be_hidden()
    expect(builds).to_have_attribute("aria-pressed", "true")
    create.click()
    page.get_by_role("dialog", name="Signing secret").wait_for()
    page.get_by_role("button", name="Copy signing secret", exact=True).wait_for()
    page.get_by_role("button", name="Done", exact=True).click()
    expect(new_webhook).to_be_focused()
    page.get_by_role("button", name=f"Deliveries for {endpoint}", exact=True).click()
    drawer = page.get_by_role("dialog", name=endpoint)
    drawer.get_by_text("build.created", exact=True).wait_for()
    drawer.get_by_role("button", name="Failing", exact=False).wait_for()
    page.keyboard.press("Escape")
    drawer.wait_for(state="hidden")
    page.get_by_role("button", name=f"Actions for {endpoint}", exact=True).click()
    page.get_by_role("menuitem", name="Delete", exact=True).click()
    page.get_by_role("alertdialog", name="Delete webhook?").get_by_role("button", name="Delete webhook", exact=True).click()
    page.get_by_text("No webhooks yet.", exact=True).wait_for()


def check_create_dialogs(page):
    for path, button, field in (("/organizations", "New organization", "Name"), (base + "?tab=org", "New workspace", "Name"), ("/tokens", "New token", "Name")):
        page.goto(state["ui_url"] + path)
        opener = page.get_by_role("button", name=button, exact=True).first
        opener.click()
        expect(page.get_by_role("dialog").get_by_role("textbox", name=field, exact=True)).to_be_focused()
        if button == "New workspace":
            page.get_by_role("dialog", name="New workspace in acme", exact=True).get_by_text("A workspace is a project inside acme.", exact=False).wait_for()
        page.keyboard.press("Escape")
        expect(opener).to_be_focused()


with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    try:
        for user in ("owner", "member"):
            for theme in ("light", "dark"):
                for narrow in (False, True):
                    context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme)
                    auth = {"idToken": state["users"][user]["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
                    context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)}, JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.MODE_KEY)}, {json.dumps(theme)});")
                    page = context.new_page()
                    errors = []
                    page.on("pageerror", lambda error: errors.append(str(error)))
                    if user == "owner" and theme == "light" and not narrow:
                        context.add_init_script(
                            f"localStorage.setItem('loco:active-org:v1', {json.dumps(org_id)});"
                            f"localStorage.setItem('loco:active-workspace:v1', {json.dumps(workspace_id)});"
                        )
                        beta_id = org_ids["beta"]
                        beta_workspace = state["workspaces"]["beta/beta-workspace"]
                        org_list_rpc = "**/loco.org.v1.OrgService/ListUserOrgs"
                        page.route(org_list_rpc, lambda route: route.abort())
                        page.goto(f"{state['ui_url']}/org/{beta_id}/settings")
                        page.wait_for_url(f"{state['ui_url']}/org/{beta_id}/wks/{beta_workspace}/settings?scope=org")
                        assert page.get_by_role("textbox", name="Name", exact=True).input_value() == "beta"
                        page.unroute(org_list_rpc)
                        page.goto(f"{state['ui_url']}/org/{org_ids['empty']}/settings")
                        page.wait_for_url(f"{state['ui_url']}/organizations")
                    for tab in ("ws", "org"):
                        try:
                            page.goto(state["ui_url"] + base + "?tab=" + tab)
                            page.get_by_placeholder("storefront" if tab == "ws" else "team-loco").wait_for()
                            name = page.get_by_role("textbox", name="Name", exact=True)
                            assert name.count() == 1
                            assert page.get_by_text("Created", exact=True).first.evaluate("node => node.tagName") == "DT"
                            assert name.evaluate("node => node.labels.length") == 1
                            assert descriptions(name) == []
                            if user == "member":
                                assert name.is_disabled()
                            else:
                                original = name.input_value()
                                name.locator("xpath=ancestor::label[1]").get_by_text("Name", exact=True).click()
                                assert name.evaluate("node => node === document.activeElement")
                                name.fill("")
                                page.get_by_text("Name required", exact=True).wait_for()
                                assert name.get_attribute("aria-invalid") == "true"
                                assert "Name required" in descriptions(name)
                                page.get_by_role("button", name="Discard", exact=True).click()
                                assert name.input_value() == original
                                assert name.get_attribute("aria-invalid") == "false"
                                assert "Name required" not in descriptions(name)
                            if tab == "ws":
                                description = page.get_by_role("textbox", name="Description", exact=True)
                                assert description.count() == 1 and description.evaluate("node => node.labels.length") == 1
                                assert "256" in " ".join(descriptions(description))
                                assert description.get_attribute("id") != name.get_attribute("id")
                                if user == "member":
                                    assert description.is_disabled()
                                else:
                                    description.locator("xpath=ancestor::label[1]").get_by_text("Description", exact=True).click()
                                    assert description.evaluate("node => node === document.activeElement")
                                    name.focus()
                                    page.keyboard.press("Tab")
                                    assert description.evaluate("node => node === document.activeElement")
                            if tab == "ws" and user == "owner" and theme == "light" and not narrow:
                                check_webhooks(page)
                                check_create_dialogs(page)
                            suffix = f"{user}-{tab}-{theme}{'-narrow' if narrow else ''}"
                            page.screenshot(path=out / f"settings-labels-{suffix}.png", animations="disabled")
                            assert not errors, errors
                            results.append({"user": user, "tab": tab, "theme": theme, "narrow": narrow, "errors": list(errors)})
                        except BaseException as error:
                            suffix = f"{user}-{tab}-{theme}{'-narrow' if narrow else ''}"
                            with contextlib.suppress(Exception):
                                page.screenshot(path=out / f"failure-{suffix}.png", animations="disabled")
                            (out / "failure.json").write_text(json.dumps({"user": user, "tab": tab, "theme": theme, "narrow": narrow, "error": repr(error), "errors": errors, "completed": results}, indent=2))
                            raise
                    context.close()
    finally:
        browser.close()
(out / "results.json").write_text(json.dumps(results, indent=2))
print(f"PASS: {len(results)} settings field-label cases", flush=True)
