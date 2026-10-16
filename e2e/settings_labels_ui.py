import argparse
import atexit
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path

from playwright.sync_api import sync_playwright

repo = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description="Check settings field names, focus and descriptions in the production UI")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "e2e/out/settings-labels"))
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="settings-labels", scenario=str(repo / "e2e/fixtures/settings-labels/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    print("Preview state:", args.state, flush=True)
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
orgs = evidence.rpc(state["api_url"], state["users"]["owner"]["token"], "loco.org.v1.OrgService/ListUserOrgs", {"userId": state["users"]["owner"]["id"]})
org_id = orgs["orgs"][0]["id"]
workspace_id = state["workspaces"]["acme/shared"]
base = f"/org/{org_id}/wks/{workspace_id}/settings"
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
results = []


def descriptions(field):
    return field.evaluate("(node) => (node.getAttribute('aria-describedby') || '').split(/\\s+/).filter(Boolean).map(id => { const hint = document.getElementById(id); if (!hint) throw new Error('Missing describedby target'); return hint.textContent; })")


with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    try:
        for user in ("owner", "member"):
            for theme in ("light", "dark"):
                for narrow in (False, True):
                    context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme)
                    auth = {"idToken": state["users"][user]["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
                    context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)}, JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.THEME_KEY)}, {json.dumps(theme)});")
                    page = context.new_page()
                    errors = []
                    page.on("pageerror", lambda error: errors.append(str(error)))
                    for tab in ("ws", "org"):
                        try:
                            page.goto(state["ui_url"] + base + "?tab=" + tab)
                            page.get_by_placeholder("storefront" if tab == "ws" else "team-loco").wait_for()
                            name = page.get_by_role("textbox", name="Name", exact=True)
                            assert name.count() == 1
                            assert page.get_by_text("Created", exact=True).evaluate("node => node.tagName") == "SPAN"
                            assert name.evaluate("node => node.labels.length") == 1
                            assert descriptions(name) and "unique" in " ".join(descriptions(name))
                            if user == "member":
                                assert name.is_disabled()
                            else:
                                original = name.input_value()
                                page.get_by_text("Name", exact=True).click()
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
                                    page.get_by_text("Description", exact=True).click()
                                    assert description.evaluate("node => node === document.activeElement")
                                    name.focus()
                                    page.keyboard.press("Tab")
                                    assert description.evaluate("node => node === document.activeElement")
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
