import argparse
import re

from harness import expect_rpc, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check the System Operators and Users pages: who sees controls, role grants and suspension"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(
    args, "system-people", "tests/ui/fixtures/system-people/scenario.json"
)
ui = state["ui_url"]

GRANT = "loco.admin.v1.SystemRoleService/GrantSystemRole"
REVOKE = "loco.admin.v1.SystemRoleService/RevokeSystemRole"
SET_STATUS = "loco.user.v1.UserService/SetUserStatus"


def row(page, label):
    return page.get_by_role("row", name=label, exact=True)


def holder(page, email):
    return (
        page.get_by_role("table", name="Operators", exact=True)
        .get_by_role("row")
        .filter(has_text=email)
    )


with session(repo / "tests/ui/out/system-people") as browser:
    # A system reader sees both lists and no control that changes anything.
    context, page, errors = open_page(browser, user="reader", state=state)
    page.goto(ui + "/system/operators")
    expect(page.get_by_role("heading", name="Operators", exact=True)).to_be_visible()
    operators = page.get_by_role("table", name="Operators", exact=True)
    expect(operators).to_contain_text("admin@loco.test")
    expect(operators).to_contain_text("Configuration and grant")
    waiting = page.get_by_role("table", name="Waiting to sign in", exact=True)
    expect(waiting).to_contain_text("ops@loco.test")
    expect(waiting).to_contain_text("audit@loco.test")
    expect(page.get_by_role("button", name="Grant role", exact=True)).to_have_count(0)
    expect(page.get_by_role("button", name=re.compile("^Actions for"))).to_have_count(0)
    page.goto(ui + "/system/users")
    users = page.get_by_role("table", name="Users", exact=True)
    row(page, "Open Gus Guest").click()
    expect(page).to_have_url(re.compile(r"/system/users\?.*user=[\w-]+"))
    drawer = page.get_by_role("dialog")
    expect(drawer).to_contain_text("guest@acme.test")
    expect(page.get_by_role("button", name="Suspend", exact=True)).to_have_count(0)
    expect(page.get_by_role("button", name="Reinstate", exact=True)).to_have_count(0)
    assert not [e for e in errors if e], errors
    context.close()

    # A system admin grants, changes and revokes a role, and learns why some changes are refused.
    context, page, errors = open_page(browser, user="admin", state=state)
    page.goto(ui + "/system/operators")
    operators = page.get_by_role("table", name="Operators", exact=True)
    page.get_by_role("textbox", name="Filter operators by email", exact=True).fill(
        "reader"
    )
    expect(page).to_have_url(re.compile(r"[?&]q=reader"))
    expect(holder(page, "second@loco.test")).to_have_count(0)
    page.get_by_role("textbox", name="Filter operators by email", exact=True).fill("")

    page.get_by_role("button", name="Grant role", exact=True).click()
    dialog = page.get_by_role("dialog", name="Grant an installation role", exact=True)
    submit = dialog.get_by_role("button", name="Grant read role", exact=True)
    expect(submit).to_be_disabled()
    dialog.get_by_role("textbox", name="Email", exact=True).fill("nobody@acme.test")
    expect(dialog).to_contain_text("No user has this email")
    dialog.get_by_role("textbox", name="Email", exact=True).fill("member@acme.test")
    expect(dialog).to_contain_text("Account found: Max Member")
    with expect_rpc(
        page, GRANT, lambda body: body.get("role") == "SYSTEM_ROLE_READ"
    ) as granted:
        submit.click()
    assert granted.value.ok
    member = holder(page, "member@acme.test")
    expect(member).to_contain_text("Read")
    expect(member).to_contain_text("Grant")

    page.get_by_role("button", name="Actions for member@acme.test", exact=True).click()
    page.get_by_role("menuitem", name="Make admin", exact=True).click()
    confirm = page.get_by_role("alertdialog")
    confirm.get_by_role("button", name="Cancel", exact=True).click()
    expect(member).to_contain_text("Read")
    page.get_by_role("button", name="Actions for member@acme.test", exact=True).click()
    page.get_by_role("menuitem", name="Make admin", exact=True).click()
    with expect_rpc(page, GRANT, lambda body: body.get("role") == "SYSTEM_ROLE_ADMIN"):
        confirm.get_by_role("button", name="Make admin", exact=True).click()
    expect(member).to_contain_text("Admin")

    # A configured admin cannot be lowered, and a configured role has no grant to revoke.
    page.get_by_role("button", name="Actions for admin@loco.test", exact=True).click()
    expect(
        page.get_by_role("menuitem", name="Make reader", exact=True)
    ).to_have_attribute("aria-disabled", "true")
    expect(page.get_by_role("menu")).to_contain_text("configured admin")
    expect(
        page.get_by_role("menuitem", name="Revoke grant", exact=True)
    ).to_have_attribute("aria-disabled", "true")
    expect(page.get_by_role("menu")).to_contain_text("API configuration")
    page.keyboard.press("Escape")

    page.get_by_role("button", name="Actions for member@acme.test", exact=True).click()
    page.get_by_role("menuitem", name="Revoke grant", exact=True).click()
    expect(confirm).to_contain_text("loses the role this grant gave")
    with expect_rpc(page, REVOKE, lambda body: True):
        confirm.get_by_role("button", name="Revoke grant", exact=True).click()
    expect(member).to_have_count(0)

    # A system admin suspends a user from the drawer after a confirm step.
    page.goto(ui + "/system/users")
    row(page, "Open Gus Guest").click()
    drawer = page.get_by_role("dialog")
    expect(drawer).to_contain_text("guest@acme.test")
    drawer.get_by_role("button", name="Suspend", exact=True).click()
    expect(confirm).to_contain_text("can no longer sign in")
    confirm.get_by_role("button", name="Cancel", exact=True).click()
    drawer.get_by_role("button", name="Suspend", exact=True).click()
    with expect_rpc(
        page,
        SET_STATUS,
        lambda body: (
            body.get("status") == "USER_STATUS_SUSPENDED"
            and body.get("userId") is not None
        ),
    ) as suspended:
        confirm.get_by_role("button", name="Suspend user", exact=True).click()
    assert suspended.value.ok
    expect(confirm).to_have_count(0)
    page.keyboard.press("Escape")
    expect(page).not_to_have_url(re.compile(r"user="))
    assert not [e for e in errors if e], errors
    context.close()
