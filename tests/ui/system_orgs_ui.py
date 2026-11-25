import argparse
import re

from harness import call, expect_rpc, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check the System organizations pages: the list, the detail, limits and suspension"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "system-orgs", "tests/ui/fixtures/system-orgs/scenario.json")
ui = state["ui_url"]
ORG_SERVICE = "loco.org.v1.OrgService/"


def org_row(page, name):
    return page.get_by_role("row", name=f"Open {name}", exact=True)


def limit_row(page, label):
    return page.get_by_role("table", name="Limits", exact=True).get_by_role(
        "row", name=re.compile("^" + re.escape(label))
    )


def limit_menu(page, label, item):
    page.get_by_role("button", name=f"Actions for {label}", exact=True).click()
    page.get_by_role("menuitem", name=item, exact=True).click()


def limits_of(org):
    return call(state, ORG_SERVICE + "GetOrgLimits", {"orgId": org}, user="admin")[
        "limits"
    ]


ops = state["orgs"]["ops"]["id"]
labs = state["orgs"]["labs"]["id"]
MEMBERS = "Members per organization"

with session(repo / "tests/ui/out/system-orgs") as browser:
    # A system reader lists and searches the organizations and opens one, with no change controls.
    context, page, errors = open_page(browser, user="reader", state=state)
    page.goto(ui + "/system/orgs")
    expect(
        page.get_by_role("heading", name="Organizations", exact=True)
    ).to_be_visible()
    for name in ("acme", "ops", "labs"):
        expect(org_row(page, name)).to_be_visible()
    expect(org_row(page, "acme")).to_contain_text("Max Member")
    expect(org_row(page, "acme")).to_contain_text("Active")
    search = page.get_by_role("textbox", name="Search organizations", exact=True)
    search.fill("acme")
    expect(page).to_have_url(re.compile(r"[?&]q=acme"))
    expect(org_row(page, "acme")).to_be_visible()
    expect(org_row(page, "ops")).to_have_count(0)
    search.fill("admin@loco.test")
    expect(org_row(page, "ops")).to_be_visible()
    expect(org_row(page, "labs")).to_be_visible()
    expect(org_row(page, "acme")).to_have_count(0)
    search.fill("no-such-organization")
    expect(page.get_by_text("No organizations match", exact=True)).to_be_visible()
    search.fill("")
    expect(org_row(page, "acme")).to_be_visible()

    # Sorting by name orders the rows and keeps the order in the URL.
    page.get_by_role("columnheader", name="Organization", exact=True).get_by_role(
        "button"
    ).click()
    expect(page).to_have_url(re.compile(r"[?&]sort=name"))
    expect(page).to_have_url(re.compile(r"[?&]desc=false"))
    names = (
        page.get_by_role("row").filter(has_text=re.compile("Active")).all_inner_texts()
    )
    assert [n.split()[0] for n in names] == ["acme", "labs", "ops"], names

    org_row(page, "ops").click()
    expect(page).to_have_url(f"{ui}/system/orgs/{ops}")
    expect(page.get_by_role("heading", name=re.compile("^ops"))).to_be_visible()
    facts = page.locator("dl")
    expect(
        facts.locator("dt", has_text="Owners").locator("xpath=following-sibling::dd")
    ).to_contain_text("Ada Admin")
    expect(facts.get_by_text(ops, exact=True)).to_be_visible()
    members = page.get_by_role("table", name="Members", exact=True)
    expect(members.get_by_role("row", name=re.compile("Max Member"))).to_be_visible()
    expect(limit_row(page, MEMBERS)).to_contain_text("Install default")
    link = page.get_by_role("link", name="Open workspace", exact=True)
    expect(link).to_have_attribute("href", re.compile(rf"^/org/{ops}/wks/[\w-]+$"))
    expect(page.get_by_role("button", name=re.compile("^Actions for"))).to_have_count(0)
    for name in ("Suspend", "Resume"):
        expect(page.get_by_role("button", name=name, exact=True)).to_have_count(0)
    link.click()
    expect(page).to_have_url(re.compile(rf"/org/{ops}/wks/"))
    page.goto(ui + "/system/orgs/00000000-0000-0000-0000-000000000000")
    expect(
        page.get_by_role("heading", name="Nothing lives at this address", exact=True)
    ).to_be_visible()
    assert not [e for e in errors if e], errors
    context.close()

    # An admin overrides a limit with a reason, then returns it to the install default.
    context, page, errors = open_page(browser, user="admin", state=state)
    page.goto(f"{ui}/system/orgs/{ops}")
    default = limit_row(page, MEMBERS).inner_text()
    limit_menu(page, MEMBERS, "Override")
    dialog = page.get_by_role(
        "dialog", name="Override members per organization", exact=True
    )
    submit = dialog.get_by_role("button", name="Override limit", exact=True)
    expect(submit).to_be_disabled()
    dialog.get_by_role("spinbutton", name="New limit", exact=True).fill("40")
    expect(submit).to_be_disabled()
    dialog.get_by_role("textbox", name="Reason", exact=True).fill(
        "raised for onboarding"
    )
    with expect_rpc(
        page,
        ORG_SERVICE + "SetOrgLimits",
        lambda body: (
            body.get("reason") == "raised for onboarding"
            and body.get("limits") == {"membersPerOrg": 40}
            and body.get("updateMask") == "membersPerOrg"
        ),
    ):
        submit.click()
    expect(limit_row(page, MEMBERS)).to_contain_text("Override")
    expect(limit_row(page, MEMBERS)).to_contain_text("40")
    expect(page.get_by_text("Latest change: raised for onboarding")).to_be_visible()
    saved = limits_of(ops)
    assert saved["limits"]["membersPerOrg"] == 40, saved
    assert saved["overridden"] == ["members_per_org"], saved

    limit_menu(page, MEMBERS, "Return to install default")
    dialog = page.get_by_role(
        "dialog",
        name="Return members per organization to the install default",
        exact=True,
    )
    dialog.get_by_role("textbox", name="Reason", exact=True).fill("onboarding finished")
    with expect_rpc(
        page,
        ORG_SERVICE + "SetOrgLimits",
        lambda body: (
            body.get("reason") == "onboarding finished"
            and body.get("updateMask") == "membersPerOrg"
        ),
    ):
        dialog.get_by_role("button", name="Return to default", exact=True).click()
    expect(limit_row(page, MEMBERS)).to_contain_text("Install default")
    assert "overridden" not in limits_of(ops), limits_of(ops)

    # An admin suspends an organization with a reason, finds it under the Suspended filter and
    # resumes it.
    page.goto(f"{ui}/system/orgs/{labs}")
    page.get_by_role("button", name="Suspend", exact=True).click()
    dialog = page.get_by_role("dialog", name="Suspend labs", exact=True)
    expect(dialog).to_contain_text("removed from its cluster")
    expect(dialog).to_contain_text("builds are canceled")
    expect(dialog).to_contain_text("members can still read")
    confirm = dialog.get_by_role("button", name="Suspend organization", exact=True)
    expect(confirm).to_be_disabled()
    dialog.get_by_role("textbox", name="Reason", exact=True).fill("Unpaid invoice")
    with expect_rpc(
        page,
        ORG_SERVICE + "SuspendOrg",
        lambda body: (
            body.get("orgId") == labs and body.get("reason") == "Unpaid invoice"
        ),
    ):
        confirm.click()
    expect(page.get_by_role("heading", name=re.compile("^labs"))).to_contain_text(
        "Suspended"
    )
    expect(page.locator("p", has_text="Reason: Unpaid invoice")).to_be_visible()

    page.goto(ui + "/system/orgs")
    page.get_by_role("button", name=re.compile("^State:")).click()
    page.get_by_role("menuitemradio", name="Suspended", exact=True).click()
    expect(page).to_have_url(re.compile(r"[?&]state=suspended"))
    page.keyboard.press("Escape")
    expect(org_row(page, "labs")).to_contain_text("Suspended")
    expect(org_row(page, "labs").get_by_title("Unpaid invoice")).to_be_visible()
    expect(org_row(page, "ops")).to_have_count(0)

    org_row(page, "labs").click()
    page.get_by_role("button", name="Resume", exact=True).click()
    dialog = page.get_by_role("alertdialog")
    with expect_rpc(
        page, ORG_SERVICE + "ResumeOrg", lambda body: body.get("orgId") == labs
    ):
        dialog.get_by_role("button", name="Resume organization", exact=True).click()
    expect(page.get_by_role("button", name="Suspend", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", name=re.compile("^labs"))).to_contain_text(
        "Active"
    )
    assert not [e for e in errors if e], errors
    context.close()
