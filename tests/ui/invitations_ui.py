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
parser = argparse.ArgumentParser(description="Check invitation links in the production UI")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="invitations", scenario=str(repo / "tests/ui/fixtures/org-roles/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    print("Preview state:", args.state, flush=True)
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
org = evidence.rpc(state["api_url"], state["users"]["owner"]["token"], "loco.org.v1.OrgService/ListUserOrgs", {"userId": state["users"]["owner"]["id"]})["orgs"][0]
path = state["ui_url"] + f"/org/{org['id']}/team"

with sync_playwright() as p:
    browser = p.chromium.launch()
    for theme in ("light", "dark"):
        for width, viewport in (("desktop", evidence.DESKTOP), ("narrow", evidence.NARROW)):
            context = browser.new_context(viewport=viewport, color_scheme=theme)
            context.grant_permissions(["clipboard-read", "clipboard-write"], origin=state["ui_url"])
            owner = state["users"]["owner"]
            auth = {"idToken": owner["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
            context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)}, JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.MODE_KEY)}, {json.dumps(theme)});")
            page = context.new_page()
            page.goto(path + "?invite=1")
            email = f"invite-{theme}-{width}@acme.test"
            page.get_by_role("textbox", name="Emails").fill(email)
            page.get_by_role("button", name="Send invite", exact=True).click()
            expect(page.get_by_role("button", name="Copy link", exact=True)).to_be_visible()
            page.get_by_role("button", name="Copy link", exact=True).click()
            invitation = evidence.rpc(state["api_url"], owner["token"], "loco.org.v1.OrgService/ListInvitations", {"orgId": org["id"]})["invitations"][0]
            assert page.evaluate("navigator.clipboard.readText()") == invitation["link"]
            page.get_by_role("button", name="Done", exact=True).click()
            row = page.get_by_role("row").filter(has_text=email)
            expect(row.get_by_text("Invited", exact=True)).to_be_visible()
            evidence.psql(state["container"], f"UPDATE invitations SET expires_at = NOW() - INTERVAL '1 hour' WHERE id = {evidence.sql_literal(invitation['id'])};")
            page.reload()
            expect(row.get_by_text("Expired", exact=True)).to_be_visible()
            row.get_by_role("button", name=f"Actions for the invitation to {email}", exact=True).click()
            page.get_by_role("menuitem", name="Send a new invite", exact=True).click()
            expect(row.get_by_text("Invited", exact=True)).to_be_visible()
            row.get_by_role("button", name=f"Actions for the invitation to {email}", exact=True).click()
            page.get_by_role("menuitem", name="Copy invite link", exact=True).click()
            assert page.evaluate("navigator.clipboard.readText()") == invitation["link"]
            context.close()
    browser.close()
print("Invitation create, copy, expiry and resend passed in light/dark at desktop/narrow widths")
