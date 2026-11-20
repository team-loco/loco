import argparse
from pathlib import Path

from harness import load_state, open_page, repo, session, sql
from playwright.sync_api import expect
from settings_fixture import seed_settings_resource

parser = argparse.ArgumentParser(
    description="Check the confirmation before a public resource becomes internal"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-public"))
args = parser.parse_args()
state = load_state(
    args, "settings-public", "tests/ui/fixtures/deployment-history/scenario.json"
)
workspace = state["workspaces"]["acme/shared"]
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
name, resource, base = seed_settings_resource(state, "public")
domain = f"{name}.apps.example.test"
sql(
    state,
    f"""INSERT INTO resource_domains(resource_id, environment_id, domain, domain_source, subdomain_label, platform_domain, is_primary)
SELECT '{resource}', id, '{domain}', 'platform_provided', '{name}', 'apps.example.test', true
FROM environments WHERE workspace_id='{workspace}' AND name='production';""",
)

display_name = "Public Shop"
sql(state, f"UPDATE resources SET name='{display_name}' WHERE id='{resource}';")

with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    removals = []

    def removal(route):
        removals.append(route.request.post_data_json)
        route.abort()

    context.route("**/loco.domain.v1.DomainService/DeleteResourceDomain", removal)
    context.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment",
        lambda route: route.abort(),
    )
    page.goto(base + "?env=production&tab=settings")
    switch = page.get_by_role("switch", name="Public", exact=True)
    expect(switch).to_be_checked()
    switch.click()
    save = page.get_by_role("button", name="Save & redeploy", exact=True)
    save.click()
    dialog = page.get_by_role("alertdialog", name=f"Make {display_name} internal?")
    expect(dialog).to_contain_text(domain)
    assert removals == [], removals
    dialog.get_by_role("button", name="Cancel", exact=True).click()
    expect(dialog).to_have_count(0)
    assert removals == [], removals
    expect(page.get_by_text("1 unsaved change", exact=True)).to_be_visible()
    save.click()
    with page.expect_request(
        lambda request: request.url.endswith("/DeleteResourceDomain")
    ):
        dialog.get_by_role("button", name="Remove domains and save", exact=True).click()
    assert len(removals) == 1, removals
    assert not errors, errors
    context.close()
    browser.close()
print("PASS: making a public resource internal asks before removing its domains")
