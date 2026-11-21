import argparse
import re
from functools import partial
from pathlib import Path

from harness import load_state, open_page as new_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Test how the command palette ranks and matches pages and resources"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/command-palette"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "command_palette_ui", "tests/ui/fixtures/command-palette/scenario.json"
)
ui = state["ui_url"]
org = state["orgs"]["acme"]["id"]
shared = state["workspaces"]["acme/shared"]

open_page = partial(new_page, state=state)

with session(out) as browser:
    context, page, errors = open_page(browser, "owner")
    page.goto(f"{ui}/org/{org}/wks/{shared}/resources")
    page.get_by_role("button", name="Search and run commands").click()
    palette = page.get_by_role("dialog", name="Command palette")
    results = palette.get_by_role("listbox", name="Results")
    query = palette.get_by_role("combobox", name="Search resources, pages and actions")

    # Before any typing the palette offers the organization's other workspaces.
    expect(
        results.get_by_role("group", name="Workspaces").get_by_role(
            "option", name=re.compile("^Switch to sibling")
        )
    ).to_be_visible()

    query.fill("log")
    expect(results.get_by_role("option", name="Logs for api")).to_be_visible()
    expect(results.get_by_role("option").first).to_have_accessible_name(
        re.compile("^Logs")
    )
    expect(results.get_by_role("group").first).to_have_accessible_name("Pages")
    expect(
        results.get_by_role("option", name="product-catalog", exact=True)
    ).to_have_count(0)

    query.fill("cat")
    expect(
        results.get_by_role("option", name="product-catalog", exact=True)
    ).to_have_count(1)

    query.fill("no such command")
    expect(palette.get_by_text("Nothing matches.", exact=True)).to_be_visible()
    expect(
        palette.get_by_text(re.compile("^Try a page, resource or action"))
    ).to_be_visible()
    assert not errors, errors
    context.close()
print("PASS: command palette")
