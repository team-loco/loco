import argparse

from harness import evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check invitation links in the production UI"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "invitations", "tests/ui/fixtures/org-roles/scenario.json")
org = evidence.rpc(
    state["api_url"],
    state["users"]["owner"]["token"],
    "loco.org.v1.OrgService/ListUserOrgs",
    {"userId": state["users"]["owner"]["id"]},
)["orgs"][0]
path = state["ui_url"] + f"/org/{org['id']}/team"

with session(repo / "tests/ui/out/invitations") as browser:
    context, page, errors = open_page(browser, state=state)
    context.grant_permissions(
        ["clipboard-read", "clipboard-write"], origin=state["ui_url"]
    )
    owner = state["users"]["owner"]
    page.goto(path + "?invite=1")
    email = "invite@acme.test"
    page.get_by_role("textbox", name="Emails").fill(email)
    page.get_by_role("button", name="Send invite", exact=True).click()
    expect(page.get_by_role("button", name="Copy link", exact=True)).to_be_visible()
    page.get_by_role("button", name="Copy link", exact=True).click()
    invitation = evidence.rpc(
        state["api_url"],
        owner["token"],
        "loco.org.v1.OrgService/ListInvitations",
        {"orgId": org["id"]},
    )["invitations"][0]
    assert page.evaluate("navigator.clipboard.readText()") == invitation["link"]
    page.get_by_role("button", name="Done", exact=True).click()
    row = page.get_by_role("row").filter(has_text=email)
    expect(row.get_by_text("Invited", exact=True)).to_be_visible()
    evidence.psql(
        state["container"],
        f"UPDATE invitations SET expires_at = NOW() - INTERVAL '1 hour' WHERE id = {evidence.sql_literal(invitation['id'])};",
    )
    page.reload()
    expect(row.get_by_text("Expired", exact=True)).to_be_visible()
    row.get_by_role(
        "button", name=f"Actions for the invitation to {email}", exact=True
    ).click()
    page.get_by_role("menuitem", name="Send a new invite", exact=True).click()
    expect(row.get_by_text("Invited", exact=True)).to_be_visible()
    row.get_by_role(
        "button", name=f"Actions for the invitation to {email}", exact=True
    ).click()
    page.get_by_role("menuitem", name="Copy invite link", exact=True).click()
    assert page.evaluate("navigator.clipboard.readText()") == invitation["link"]
    assert not errors, errors
    context.close()
    browser.close()
print("Invitation create, copy, expiry and resend passed")
