import argparse
import re

from playwright.sync_api import expect
from harness import evidence, load_state, open_page, repo, session

parser = argparse.ArgumentParser(description="Check account deletion impact and confirmation in the production UI")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "account-deletion", "tests/ui/fixtures/account-deletion/scenario.json")


def call(user, method, body):
    return evidence.rpc(state["api_url"], state["users"][user]["token"], method, body)


def role(caller, org_id, user, value):
    call(caller, "loco.org.v1.OrgService/SetOrgMemberRole", {
        "orgId": org_id, "userId": state["users"][user]["id"], "role": value,
    })


def token(name, workspace):
    return call("departing", "loco.token.v1.TokenService/CreateToken", {
        "name": name, "entityType": "ENTITY_TYPE_WORKSPACE", "entityId": workspace,
        "expiresInSec": "3600", "scopes": [{"entityType": "ENTITY_TYPE_WORKSPACE", "entityId": workspace, "scope": "SCOPE_READ"}],
    })["token"]


acme = call("owner", "loco.org.v1.OrgService/ListUserOrgs", {"userId": state["users"]["owner"]["id"]})["orgs"][0]["id"]
private = call("other", "loco.org.v1.OrgService/ListUserOrgs", {"userId": state["users"]["other"]["id"]})["orgs"][0]["id"]
shared = state["workspaces"]["acme/shared"]
private_workspace = state["workspaces"]["private-owner/private-workspace"]
role("owner", acme, "departing", "ORG_ROLE_ADMIN")
role("other", private, "departing", "ORG_ROLE_ADMIN")
for index in range(12):
    surviving = token(f"automation-{index:02d}", shared)
token("private-credential", private_workspace)
call("other", "loco.org.v1.OrgService/RemoveOrgMember", {"orgId": private, "userId": state["users"]["departing"]["id"]})

with session(repo / "tests/ui/out/account-deletion") as browser:
    # A pending/error preview cannot accidentally enable an irreversible action.
    context, page, errors = open_page(browser, "departing", state=state)
    preview = "**/loco.user.v1.UserService/GetAccountDeletionImpact"
    context.route(preview, lambda route: route.fulfill(status=503, json={"code": "unavailable", "message": "Account check unavailable"}))
    page.goto(state["ui_url"] + "/profile")
    expect(page.get_by_text("Account check unavailable.", exact=True)).to_be_visible()
    expect(page.get_by_role("button", name="Delete account", exact=True)).to_be_disabled()
    context.unroute(preview)
    page.get_by_role("button", name="Retry", exact=True).click()
    delete = page.get_by_role("button", name="Delete account", exact=True)
    expect(delete).to_be_enabled()
    expect(page.get_by_text("You also issued tokens for owners you can no longer access. Their administrators can review and revoke them.", exact=True)).to_be_visible()
    expect(page.get_by_text("private-credential", exact=True)).to_have_count(0)
    expect(page.get_by_text("private-owner", exact=True)).to_have_count(0)
    tokens = page.get_by_role("list", name="Surviving automation tokens", exact=True)
    expect(tokens.get_by_role("listitem")).to_have_count(10)
    page.get_by_role("button", name="Next", exact=True).click()
    expect(tokens.get_by_role("listitem")).to_have_count(2)
    page.get_by_role("button", name="Previous", exact=True).click()
    expect(tokens.get_by_role("listitem")).to_have_count(10)
    row = tokens.get_by_role("listitem").filter(has=page.get_by_text("automation-10", exact=True))
    row.get_by_role("button", name="Revoke", exact=True).click()
    revoke = page.get_by_role("alertdialog", name="Revoke automation-10?", exact=True)
    revoke.get_by_role("button", name="Cancel", exact=True).click()
    expect(row.get_by_role("button", name="Revoke", exact=True)).to_be_focused()
    row.get_by_role("button", name="Revoke", exact=True).click()
    revoke.get_by_role("button", name="Revoke", exact=True).click()
    expect(revoke).to_be_hidden()
    expect(tokens.get_by_text("automation-10", exact=True)).to_have_count(0)

    # A valid preview can become stale; the final backend decision remains authoritative.
    delete.click()
    dialog = page.get_by_role("alertdialog", name="Delete your account", exact=True)
    expect(dialog.get_by_text(re.compile("tokens you issued keep working"))).to_be_visible()
    confirm = dialog.get_by_role("button", name="Delete account", exact=True)
    expect(confirm).to_be_disabled()
    dialog.get_by_role("textbox").fill(state["users"]["departing"]["email"])
    role("owner", acme, "owner", "ORG_ROLE_MEMBER")
    confirm.click()
    expect(dialog).to_be_hidden()
    expect(page.get_by_text(re.compile('organization "acme" must keep at least one active admin'))).to_be_visible()
    expect(delete).to_be_disabled()
    expect(page.get_by_role("link", name="acme", exact=True)).to_be_visible()
    role("departing", acme, "owner", "ORG_ROLE_ADMIN")
    page.get_by_role("button", name="Check again", exact=True).click()
    expect(delete).to_be_enabled()
    delete.click()
    dialog.get_by_role("button", name="Cancel", exact=True).click()
    expect(delete).to_be_focused()
    delete.click()
    dialog.get_by_role("textbox").fill(state["users"]["departing"]["email"])
    confirm.click()
    expect(page).to_have_url(re.compile(r"/login(?:\?.*)?$"))
    assert errors == [], errors
    context.close()

# The UI promised that automation and shared workspaces survive the human account.
evidence.rpc(state["api_url"], surviving, "loco.workspace.v1.WorkspaceService/GetWorkspace", {"workspaceId": shared})
call("owner", "loco.workspace.v1.WorkspaceService/GetWorkspace", {"workspaceId": shared})
print("Account deletion: filtered review, pagination, revoke, stale preview, typed confirmation and surviving automation passed")
