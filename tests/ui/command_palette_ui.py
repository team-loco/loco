import argparse
import re
from functools import partial
from pathlib import Path

from harness import copied_text, load_state, open_page as new_page, repo, session
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
catalog = state["resources"]["acme/shared/product-catalog"]

open_page = partial(new_page, state=state)

with session(out) as browser:
    context, page, errors = open_page(browser, "owner")
    page.goto(f"{ui}/org/{org}/wks/{shared}/resources")
    page.get_by_role("button", name="Search and run commands").click()
    palette = page.get_by_role("dialog", name="Command palette")
    results = palette.get_by_role("listbox", name="Results")
    query = palette.get_by_role("combobox", name="Search resources, pages and actions")

    query.fill("log")
    expect(results.get_by_role("option", name="Logs for api")).to_be_visible()
    expect(results.get_by_role("option").first).to_have_accessible_name(
        re.compile("^Logs")
    )
    expect(results.get_by_role("group").first).to_have_accessible_name("Go to")
    expect(
        results.get_by_role("option", name="product-catalog", exact=True)
    ).to_have_count(0)

    query.fill("cat")
    expect(
        results.get_by_role("option", name="product-catalog", exact=True)
    ).to_have_count(1)

    query.fill("bulk")
    expect(
        results.get_by_role("group", name="Resources").get_by_role("option")
    ).to_have_count(6)

    query.fill("no such command")
    expect(palette.get_by_text("Nothing matches.", exact=True)).to_be_visible()
    expect(
        palette.get_by_text(re.compile("^Try a page, resource or action"))
    ).to_be_visible()

    query.fill("")
    copy_group = results.get_by_role("group", name="Copy")
    copy_org = copy_group.get_by_role(
        "option", name=re.compile("^Copy acme organization ID")
    )
    expect(copy_org).to_contain_text(org[-8:])
    expect(
        copy_group.get_by_role("option", name=re.compile("^Copy shared workspace ID"))
    ).to_be_visible()
    expect(
        copy_group.get_by_role("option", name=re.compile(r"^Copy \S+ environment ID"))
    ).to_be_visible()
    copy_org.click()
    expect(palette).to_have_count(0)
    assert copied_text(page) == org
    expect(page.get_by_text("Copied acme organization ID", exact=True)).to_be_visible()
    expect(page.get_by_text(org, exact=True)).to_be_visible()

    page.get_by_role("button", name="Search and run commands").click()
    query.fill("catalog id")
    results.get_by_role(
        "option", name=re.compile("^Copy product-catalog resource ID")
    ).click()
    assert copied_text(page) == catalog

    page.get_by_role("button", name="Search and run commands").click()
    query.fill("cat")
    results.get_by_role("option", name="product-catalog", exact=True).click()
    actions = palette.get_by_role(
        "combobox", name="What do you want to do with product-catalog?"
    )
    expect(actions).to_be_focused()
    expect(palette.get_by_role("button", name="Back to all commands")).to_have_text(
        "product-catalog"
    )
    resource_group = results.get_by_role("group", name="Resource")
    for option in ("Open product-catalog", "Logs", "Metrics", "Traces", "Settings"):
        expect(
            resource_group.get_by_role("option", name=option, exact=True)
        ).to_be_visible()
    expect(
        resource_group.get_by_role("option", name=re.compile("^Copy resource ID"))
    ).to_be_visible()
    page.keyboard.press("Backspace")
    expect(query).to_be_focused()
    expect(query).to_have_value("")
    query.fill("cat")
    results.get_by_role("option", name="product-catalog", exact=True).click()
    page.keyboard.press("Escape")
    expect(query).to_be_focused()
    query.fill("cat")
    results.get_by_role("option", name="product-catalog", exact=True).click()
    resource_group.get_by_role(
        "option", name="Open product-catalog", exact=True
    ).click()
    expect(palette).to_have_count(0)
    expect(page).to_have_url(re.compile(f"/resource/{catalog}(\\?|$)"))

    page.get_by_role("button", name="Search and run commands").click()
    this_page = results.get_by_role("group", name="This page")
    for option in ("Redeploy product-catalog", "Logs for product-catalog"):
        expect(this_page.get_by_role("option", name=option, exact=True)).to_be_visible()
    for option in ("View spec", "Open events", "Settings"):
        expect(
            this_page.get_by_role("option", name=re.compile(f"^{option}"))
        ).to_be_visible()
    expect(
        results.get_by_role("group", name="Copy").get_by_role("option").first
    ).to_have_accessible_name(re.compile("^Copy product-catalog resource ID"))
    this_page.get_by_role("option", name="Redeploy product-catalog", exact=True).click()
    expect(palette).to_have_count(0)
    expect(
        page.get_by_role("dialog", name="Redeploy product-catalog?", exact=True)
    ).to_be_visible()
    page.keyboard.press("Escape")
    expect(
        page.get_by_role("dialog", name="Redeploy product-catalog?", exact=True)
    ).to_have_count(0)

    page.get_by_role("button", name="Search and run commands").click()
    query.fill("logs")
    go_to_logs = results.get_by_role("group", name="Go to").get_by_role(
        "option", name="Logs", exact=True
    )
    for _ in range(results.get_by_role("option").count()):
        if go_to_logs.get_attribute("aria-selected") == "true":
            break
        page.keyboard.press("ArrowDown")
    expect(go_to_logs).to_have_attribute("aria-selected", "true")
    page.keyboard.press("Enter")
    expect(palette).to_have_count(0)
    expect(page).to_have_url(re.compile(r"/observability\?(.*&)?view=logs(&|$)"))
    expect(page).not_to_have_url(re.compile(r"[?&]r="))
    assert not errors, errors
    context.close()
print("PASS: command palette")
