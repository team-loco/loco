import argparse
import re

from playwright.sync_api import expect, sync_playwright
from harness import authenticate, evidence, load_state, launch, repo

parser = argparse.ArgumentParser(description="Check organization role changes in the production UI")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, 'org-roles', 'tests/ui/fixtures/org-roles/scenario.json')
org = evidence.rpc(state["api_url"], state["users"]["owner"]["token"], "loco.org.v1.OrgService/ListUserOrgs", {"userId": state["users"]["owner"]["id"]})["orgs"][0]
path = state["ui_url"] + f"/org/{org['id']}/team"

with sync_playwright() as p:
    browser = launch(p, repo / "tests/ui/out/org_roles")
    theme = "light"
    context = browser.new_context(viewport=evidence.DESKTOP, color_scheme=theme)
    owner = state["users"]["owner"]
    authenticate(context, owner['token'], theme)
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
    page.get_by_role("menuitem", name=re.compile(r"^Member")).click()
    page.get_by_role("button", name="Change role", exact=True).click()
    page.get_by_text("Organization must keep at least one admin.", exact=True).wait_for()
    expect(page.get_by_role("alertdialog")).to_be_visible()
    page.get_by_role("button", name="Cancel", exact=True).click()
    member_role.focus()
    page.keyboard.press("Enter")
    page.get_by_role("menuitem", name=re.compile(r"^Admin")).click()
    page.get_by_role("button", name="Cancel", exact=True).click()
    expect(member_role).to_have_text("Member")
    member_role.click()
    page.get_by_role("menuitem", name=re.compile(r"^Admin")).click()
    page.get_by_role("button", name="Change role", exact=True).click()
    expect(member_role).to_have_text("Admin")
    member_row = page.get_by_role("row").filter(has_text="Max Member")
    expect(member_row.get_by_text("All workspaces", exact=True)).to_be_visible()
    member_role.click()
    page.get_by_role("menuitem", name=re.compile(r"^Member")).click()
    page.get_by_role("button", name="Change role", exact=True).click()
    expect(member_role).to_have_text("Member")
    expect(member_row.get_by_text("shared", exact=True)).to_be_visible()
    if theme == "light":
        page.get_by_role("button", name="Workspace", exact=True).click()
        page.get_by_role("option", name=re.compile(r"^shared")).click()
        expect(page).to_have_url(re.compile(r"[?&]ws="))
        expect(page.get_by_role("row").filter(has_text="Lee Leaver")).to_have_count(0)
        expect(member_row).to_be_visible()
        page.get_by_role("button", name="Clear workspace filter", exact=True).click()
        expect(page.get_by_role("row").filter(has_text="Lee Leaver")).to_have_count(1)
        page.get_by_role("button", name="Actions for Max Member", exact=True).click()
        page.get_by_role("menuitem", name="Workspace access", exact=True).click()
        drawer = page.get_by_role("dialog", name="Max Member")
        expect(page).to_have_url(re.compile(r"[?&]access="))
        expect(drawer.get_by_role("group", name="Access to shared", exact=True)).to_be_visible()
        expect(drawer.get_by_role("group", name="Access to sibling", exact=True)).to_have_count(0)
        drawer.get_by_role("button", name=re.compile(r"^All workspaces")).click()
        expect(drawer.get_by_role("group", name="Access to sibling", exact=True)).to_be_visible()
        page.keyboard.press("Escape")
        expect(page).not_to_have_url(re.compile(r"[?&]access="))
    member_role.click()
    page.get_by_role("menuitem", name=re.compile(r"^Admin")).click()
    page.get_by_role("button", name="Change role", exact=True).click()
    expect(member_role).to_have_text("Admin")
    assert sum(url.endswith("/GetScopes") for url in requests) == scopes_before
    if theme == "dark":
        page.get_by_role("button", name="Actions for Lee Leaver", exact=True).click()
        page.get_by_role("menuitem", name="Remove from acme", exact=True).click()
        expect(page.get_by_role("alertdialog")).to_contain_text("Remove Lee Leaver from acme?")
        page.get_by_role("button", name="Remove", exact=True).click()
        expect(page.get_by_role("row").filter(has_text="Lee Leaver")).to_have_count(0)
    owner_role.click()
    page.get_by_role("menuitem", name=re.compile(r"^Member")).click()
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
    authenticate(context, member['token'])
    page = context.new_page()
    page.goto(path)
    expect(page.get_by_role("button", name="Change role for Max Member", exact=True)).to_be_disabled()
    context.close()
    browser.close()
print("Organization role changes and member removal passed; member controls disabled at narrow width")
