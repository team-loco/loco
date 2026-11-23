import argparse
from pathlib import Path

from harness import load_state, open_page, repo, session, sql, wait_for_query
from playwright.sync_api import expect
from settings_fixture import seed_settings_resource

parser = argparse.ArgumentParser(
    description="Check the unsaved changes guard in resource settings"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-unsaved"))
args = parser.parse_args()
state = load_state(
    args, "settings-unsaved", "tests/ui/fixtures/deployment-history/scenario.json"
)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
name, resource, base = seed_settings_resource(state, "unsaved")
display_name = "Unsaved Shop"
sql(state, f"UPDATE resources SET name='{display_name}' WHERE id='{resource}';")

with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    dialogs = []
    page.on("dialog", lambda dialog: (dialogs.append(dialog.type), dialog.accept()))
    page.goto(base.rsplit("/resource/", 1)[0] + "/resources")
    page.get_by_role("link", name=display_name, exact=True).first.click()
    page.get_by_role("button", name="Resource settings", exact=True).click()
    wait_for_query(page, lambda query: query.get("tab") == ["settings"])
    path = page.locator("#set-health input").first
    guard = page.get_by_role("alertdialog", name="Discard unsaved changes?")

    page.get_by_role("link", name="Dashboard", exact=True).click()
    expect(page.get_by_role("button", name="Overview", exact=True)).to_have_count(0)
    assert dialogs == [], "a clean page asks nothing"
    page.go_back()
    expect(page.get_by_role("button", name="Overview", exact=True)).to_be_visible()

    path.fill("/ready")
    expect(page.get_by_text("1 unsaved change", exact=True)).to_be_visible()
    expect(page.get_by_text("Health check path", exact=True)).to_be_visible()
    page.locator("#set-health input").nth(1).fill("45")
    expect(page.get_by_text("2 unsaved changes", exact=True)).to_be_visible()
    expect(
        page.get_by_text("Health check path, Health check interval", exact=True)
    ).to_be_visible()
    page.locator("#set-health input").nth(1).fill("30")
    page.get_by_role("link", name="Dashboard", exact=True).click()
    expect(guard).to_be_visible()
    expect(guard).to_contain_text(f"settings of {display_name} are not saved")
    guard.get_by_role("button", name="Keep editing", exact=True).click()
    expect(guard).to_have_count(0)
    expect(path).to_have_value("/ready")

    page.get_by_role("button", name="Overview", exact=True).click()
    page.go_back()
    expect(path).to_have_value("/ready")
    expect(guard).to_have_count(0)

    page.go_back()
    page.go_back()
    expect(guard).to_be_visible()
    guard.get_by_role("button", name="Keep editing", exact=True).click()
    page.get_by_role("button", name="Resource settings", exact=True).click()
    expect(path).to_have_value("/ready")

    page.get_by_role("link", name="Dashboard", exact=True).click()
    guard.get_by_role("button", name="Discard changes", exact=True).click()
    expect(page.get_by_role("button", name="Overview", exact=True)).to_have_count(0)
    page.get_by_role("link", name="Resources", exact=True).click()
    page.get_by_role("link", name=display_name, exact=True).first.click()
    page.get_by_role("button", name="Resource settings", exact=True).click()
    expect(page.locator("#set-health input").first).to_have_value("/health")
    expect(page.get_by_text("unsaved change")).to_have_count(0)

    page.locator("#set-health input").first.fill("/again")
    expect(page.get_by_text("1 unsaved change", exact=True)).to_be_visible()
    page.reload()
    assert dialogs == ["beforeunload"], dialogs

    page.locator("#set-health input").first.fill("/deleted")
    page.get_by_role("button", name="Delete resource", exact=True).click()
    delete = page.get_by_role("alertdialog", name=f"Delete {display_name}?", exact=True)
    delete.get_by_role("textbox").fill(display_name)
    delete.get_by_role("button", name=f"Delete {display_name}", exact=True).click()
    expect(page).to_have_url(
        base.rsplit("/resource/", 1)[0] + "/resources?env=production"
    )
    expect(guard).to_have_count(0)
    assert not errors, errors
    context.close()
    browser.close()
print("PASS: unsaved settings ask before navigation and reload")
