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
parser = argparse.ArgumentParser(description="Check organization role changes in the production UI")
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
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="org-roles", scenario=str(repo / "tests/ui/fixtures/org-roles/scenario.json")))
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
        context = browser.new_context(viewport=evidence.DESKTOP, color_scheme=theme)
        owner = state["users"]["owner"]
        auth = {"idToken": owner["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
        context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)}, JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.MODE_KEY)}, {json.dumps(theme)});")
        page = context.new_page()
        requests = []
        page.on("request", lambda request: requests.append(request.url))
        page.goto(path)
        page.wait_for_load_state("networkidle")
        invitations_before = sum(url.endswith("/ListInvitations") for url in requests)
        scopes_before = sum(url.endswith("/GetScopes") for url in requests)
        member_role = page.get_by_role("button", name="Change role for Max Member", exact=True)
        owner_role = page.get_by_role("button", name="Change role for Olivia Owner", exact=True)
        expect(member_role).to_have_text("Member")
        owner_role.click()
        page.get_by_role("menuitem", name="Member", exact=True).click()
        page.get_by_role("button", name="Change role", exact=True).click()
        page.get_by_text("Organization must keep at least one admin.", exact=True).wait_for()
        expect(page.get_by_role("alertdialog")).to_be_visible()
        page.get_by_role("button", name="Cancel", exact=True).click()
        member_role.focus()
        page.keyboard.press("Enter")
        page.get_by_role("menuitem", name="Admin", exact=True).click()
        page.get_by_role("button", name="Cancel", exact=True).click()
        expect(member_role).to_have_text("Member")
        member_role.click()
        page.get_by_role("menuitem", name="Admin", exact=True).click()
        page.get_by_role("button", name="Change role", exact=True).click()
        expect(member_role).to_have_text("Admin")
        member_role.click()
        page.get_by_role("menuitem", name="Member", exact=True).click()
        page.get_by_role("button", name="Change role", exact=True).click()
        expect(member_role).to_have_text("Member")
        member_role.click()
        page.get_by_role("menuitem", name="Admin", exact=True).click()
        page.get_by_role("button", name="Change role", exact=True).click()
        expect(member_role).to_have_text("Admin")
        assert sum(url.endswith("/GetScopes") for url in requests) == scopes_before
        owner_role.click()
        page.get_by_role("menuitem", name="Member", exact=True).click()
        page.get_by_role("button", name="Change role", exact=True).click()
        expect(owner_role).to_have_text("Member")
        expect(owner_role).to_be_disabled()
        expect(member_role).to_be_disabled()
        assert sum(url.endswith("/GetScopes") for url in requests) > scopes_before
        assert sum(url.endswith("/ListInvitations") for url in requests) == invitations_before
        member_token = state["users"]["member"]["token"]
        for user, role in (("owner", "ORG_ROLE_ADMIN"), ("member", "ORG_ROLE_MEMBER")):
            evidence.rpc(state["api_url"], member_token, "loco.org.v1.OrgService/SetOrgMemberRole", {
                "orgId": org["id"], "userId": state["users"][user]["id"], "role": role,
            })
        context.close()
    context = browser.new_context(viewport=evidence.NARROW)
    member = state["users"]["member"]
    auth = {"idToken": member["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
    context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)}, JSON.stringify({json.dumps(auth)}));")
    page = context.new_page()
    page.goto(path)
    expect(page.get_by_role("button", name="Change role for Max Member", exact=True)).to_be_disabled()
    context.close()
    browser.close()
print("Organization role changes passed in light/dark; member controls disabled at narrow width")
