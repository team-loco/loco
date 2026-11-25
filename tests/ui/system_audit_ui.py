import argparse
import re

from harness import load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check the System audit log: cross-organization and installation events, its filters, the Loco admin tag"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "system-audit", "tests/ui/fixtures/system-audit/scenario.json")
ui = state["ui_url"]
acme = state["orgs"]["acme"]["id"]

with session(repo / "tests/ui/out/system-audit") as browser:
    # A user without a system role cannot reach the page.
    context, page, errors = open_page(browser, user="member", state=state)
    page.goto(ui + "/system/audit")
    expect(
        page.get_by_role("heading", name="Nothing lives at this address", exact=True)
    ).to_be_visible()
    expect(page.get_by_role("table", name="Audit events")).to_have_count(0)
    context.close()

    # A system reader reads every organization's events and the installation's own.
    context, page, errors = open_page(browser, user="reader", state=state)
    page.goto(ui + "/dashboard")
    page.locator('[data-slot="account-button"]').click()
    page.get_by_role("menuitemcheckbox", name="System", exact=True).click()
    sidebar = page.locator('[data-slot="sidebar"]')
    sidebar.get_by_role("link", name="Audit log", exact=True).click()
    expect(page).to_have_url(re.compile(r"/system/audit"))
    expect(page.locator('[data-slot="system-tag"]')).to_have_text("System")
    table = page.get_by_role("table", name="Audit events")
    expect(table).to_be_visible()
    expect(table.get_by_role("columnheader", name="Organization")).to_be_visible()
    suspended = table.get_by_role("row").filter(has_text="Suspended the organization")
    expect(suspended).to_have_count(1)
    expect(suspended.locator('[data-slot="operator-tag"]')).to_have_text("Loco admin")
    updated = table.get_by_role("row").filter(has_text="Updated the organization")
    expect(updated).to_have_count(2)
    expect(updated.locator('[data-slot="operator-tag"]')).to_have_count(0)
    paused = table.get_by_role("row").filter(has_text="Paused a cluster")
    expect(paused).to_have_count(1)
    expect(paused).to_contain_text("Installation")
    expect(table.get_by_role("row").filter(has_text="Suspended a user")).to_have_count(
        1
    )

    # The organization filter narrows to one organization and keeps its state in the URL.
    page.get_by_role("button", name="Organization", exact=True).click()
    page.get_by_role(
        "option", name=re.compile(r"^acme|^" + re.escape(acme[-8:]))
    ).click()
    expect(page).to_have_url(re.compile(rf"[?&]org={acme}"))
    expect(updated).to_have_count(1)
    expect(paused).to_have_count(0)

    # Installation only keeps the events that belong to no organization.
    page.get_by_role("button", name="Installation only", exact=True).click()
    expect(page).to_have_url(re.compile(r"[?&]installation=true"))
    expect(page).not_to_have_url(re.compile(r"[?&]org="))
    expect(paused).to_have_count(1)
    expect(table.get_by_role("row").filter(has_text="Suspended a user")).to_have_count(
        1
    )
    expect(suspended).to_have_count(0)

    # The drawer opens from the URL and names the organization and the Loco admin.
    page.goto(ui + "/system/audit")
    suspended.click()
    expect(page).to_have_url(re.compile(r"[?&]event="))
    drawer = page.get_by_role("dialog")
    expect(
        drawer.get_by_role("heading", name="Suspended the organization", exact=True)
    ).to_be_visible()
    expect(drawer.locator('[data-slot="operator-tag"]')).to_have_text("Loco admin")
    expect(drawer.locator("dt", has_text=re.compile("^Organization$"))).to_be_visible()
    assert not [e for e in errors if e], errors
    context.close()

    # The organization's own log shows the tag, and its owner sees no installation events.
    context, page, errors = open_page(browser, user="member", state=state)
    page.goto(ui + f"/org/{acme}/audit")
    table = page.get_by_role("table", name="Audit events")
    suspended = table.get_by_role("row").filter(has_text="Suspended the organization")
    expect(suspended.locator('[data-slot="operator-tag"]')).to_have_text("Loco admin")
    expect(
        table.get_by_role("row")
        .filter(has_text="Updated the organization")
        .locator('[data-slot="operator-tag"]')
    ).to_have_count(0)
    expect(table.get_by_role("columnheader", name="Organization")).to_have_count(0)
    expect(table).not_to_contain_text("Paused a cluster")
    context.close()
