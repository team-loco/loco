import argparse
import json
from pathlib import Path

from harness import expect_rpc, load_state, open_page, repo, session, sql
from playwright.sync_api import expect
from settings_fixture import seed_settings_resource

parser = argparse.ArgumentParser(
    description="Check that renaming a resource in its settings keeps its slug"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-name"))
args = parser.parse_args()
state = load_state(
    args, "settings-name", "tests/ui/fixtures/deployment-history/scenario.json"
)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
slug, resource, base = seed_settings_resource(state, "rename")
taken, _, _ = seed_settings_resource(state, "taken")
sql(state, f"UPDATE resources SET name='Taken Name' WHERE slug='{taken}';")
label = "Renamed Shop"

with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    updates = []

    def record_update(route):
        updates.append(json.loads(route.request.post_data))
        route.continue_()

    context.route("**/loco.resource.v1.ResourceService/UpdateResource", record_update)
    page.goto(base + "?env=production&tab=settings")
    general = page.locator("#set-general")
    name_input = general.get_by_role("textbox", name="Name", exact=True)
    expect(name_input).to_have_value(slug)

    save = page.get_by_role("button", name="Save", exact=True)
    name_input.fill("TAKEN NAME")
    save.click()
    expect(
        general.get_by_text("TAKEN NAME already exists in this workspace", exact=True)
    ).to_be_visible()
    assert updates == [], updates

    name_input.fill(label)
    with expect_rpc(
        page, "UpdateResource", lambda body: body.get("name") == label
    ) as saved:
        save.click()
    assert saved.value.ok, saved.value.text()
    expect(page.get_by_text(f"Saved {label}", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", level=1)).to_have_text(label)
    assert updates == [{"resourceId": resource, "name": label}], updates
    stored = sql(
        state, f"SELECT slug || '|' || name FROM resources WHERE id='{resource}';"
    )
    assert stored == f"{slug}|{label}", stored
    assert not errors, errors
    context.close()
    browser.close()
print("PASS: renaming a resource saves only its name and keeps its slug")
