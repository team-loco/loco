import argparse
import re
from pathlib import Path

from harness import expect_rpc, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check the organization settings workspace list and limits"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-org"))
args = parser.parse_args()
state = load_state(
    args, "settings-org", "tests/ui/fixtures/settings-labels/scenario.json"
)
out = Path(args.out)
org_id = state["orgs"]["acme"]["id"]
shared = state["workspaces"]["acme/shared"]
sibling = state["workspaces"]["acme/sibling"]
base = f"{state['ui_url']}/org/{org_id}/wks"

with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    page.goto(f"{base}/{shared}/settings?scope=org")
    limits = page.get_by_role("region", name="Limits", exact=True)

    def in_use(label):
        return (
            limits.get_by_role("row")
            .filter(has=page.get_by_role("cell", name=label, exact=True))
            .get_by_role("cell")
            .nth(2)
        )

    expect(in_use("Resources")).to_have_text("0 in shared")
    history = [
        "Deployments",
        "Builds",
        "Build images",
        "Events and webhook deliveries",
        "Logs",
        "Traces",
        "Metrics",
        "Expired API tokens",
    ]
    labels = limits.get_by_role("row").locator("td:first-child").all_inner_texts()
    assert labels[-len(history) :] == history, labels

    workspaces = page.get_by_role("region", name="Workspaces", exact=True)
    with expect_rpc(page, "ListOrgWorkspaces", lambda body: body.get("query") == "SIB"):
        workspaces.get_by_placeholder("Search workspaces", exact=True).fill("SIB")
    expect(workspaces.get_by_role("row").filter(has_text="shared")).to_have_count(0)
    expect(workspaces.get_by_text("1–1 of 1", exact=True)).to_be_visible()
    row = workspaces.get_by_role("row").filter(has_text="sibling")
    expect(row.get_by_role("cell").last).to_have_text(
        re.compile(r"^[A-Z][a-z]{2} \d{1,2}, \d{4}$")
    )

    page.evaluate("window.scrollTo(0, document.body.scrollHeight)")
    assert page.evaluate("window.scrollY") > 0
    row.get_by_role("link", name="sibling", exact=True).click()
    page.wait_for_url(re.compile(rf"/wks/{sibling}/settings\?scope=workspace$"))
    expect(page.get_by_role("textbox", name="Name", exact=True)).to_have_value(
        "sibling"
    )
    assert page.evaluate("window.scrollY") == 0
    assert not errors, errors
    context.close()
print("Organization settings workspace list and limits checks passed")
