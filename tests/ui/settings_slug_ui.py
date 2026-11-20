import argparse
import json
from pathlib import Path

from harness import expect_rpc, load_state, open_page, repo, session, sql
from playwright.sync_api import expect
from settings_fixture import seed_settings_resource

parser = argparse.ArgumentParser(
    description="Check the confirmation before a resource's slug changes"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-slug"))
args = parser.parse_args()
state = load_state(
    args, "settings-slug", "tests/ui/fixtures/deployment-history/scenario.json"
)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
slug, resource, base = seed_settings_resource(state, "slug")
taken, _, _ = seed_settings_resource(state, "taken")
label = "Slug Shop"
sql(state, f"UPDATE resources SET name='{label}' WHERE id='{resource}';")
sql(state, f"UPDATE resources SET name='Taken Name' WHERE slug='{taken}';")
renamed = f"{slug}-v2"
breaks = [
    "Other services that call its private hostname",
    "loco.yaml files that use it as a service key",
    "CLI scripts that pass it as an argument",
    "Saved log, metric and trace filters on @resource",
    "Every replica restarts to pick up the new slug",
]

with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    updates = []

    def record_update(route):
        updates.append(json.loads(route.request.post_data))
        route.continue_()

    context.route("**/loco.resource.v1.ResourceService/UpdateResource", record_update)
    page.goto(base + "?env=production&tab=settings")
    general = page.locator("#set-general")
    slug_input = general.get_by_role("textbox", name="Slug", exact=True)
    expect(slug_input).to_have_value(slug)
    expect(slug_input).to_be_editable()

    slug_input.fill("QA Echo")
    save = page.get_by_role("button", name="Save", exact=True)
    save.click()
    expect(
        general.get_by_text(
            "Use 1 to 63 lowercase letters, digits and hyphens, starting with a letter and ending with a letter or digit",
            exact=True,
        )
    ).to_be_visible()

    slug_input.fill(renamed)
    save.click()
    dialog = page.get_by_role("alertdialog", name=f"Change the slug of {label}?")
    expect(dialog).to_contain_text(f"Everything that still names it {slug}")
    expect(dialog).to_contain_text(f"once the slug is {renamed}")
    for line in breaks:
        expect(dialog.get_by_role("listitem").filter(has_text=line)).to_have_count(1)
    dialog.get_by_role("button", name="Cancel", exact=True).click()
    expect(dialog).to_have_count(0)
    assert updates == [], updates
    expect(page.get_by_text("1 unsaved change", exact=True)).to_be_visible()

    slug_input.fill(taken)
    expect(save).to_be_disabled()
    expect(
        general.get_by_text(f"{taken} already exists in this workspace", exact=True)
    ).to_be_visible()
    expect(dialog).to_have_count(0)
    name_input = general.get_by_role("textbox", name="Name", exact=True)
    name_input.fill("TAKEN NAME")
    expect(
        general.get_by_text("TAKEN NAME already exists in this workspace", exact=True)
    ).to_be_visible()
    name_input.fill(label)
    assert updates == [], updates

    slug_input.fill(renamed)
    save.click()
    with expect_rpc(
        page, "UpdateResource", lambda body: body.get("slug") == renamed
    ) as saved:
        dialog.get_by_role("button", name="Change slug and save", exact=True).click()
    assert saved.value.ok, saved.value.text()
    expect(page.get_by_text(f"Saved {label}", exact=True)).to_be_visible()
    expect(page.get_by_label(f"Slug {renamed}", exact=True)).to_have_text(renamed)
    expect(page.get_by_role("heading", level=1)).to_have_text(label)
    assert updates[-1] == {"resourceId": resource, "slug": renamed}, updates
    stored = sql(
        state, f"SELECT slug || '|' || name FROM resources WHERE id='{resource}';"
    )
    assert stored == f"{renamed}|{label}", stored
    assert not errors, errors
    context.close()
    browser.close()
print(
    "PASS: a slug change names what breaks, waits for confirmation and saves only the slug"
)
