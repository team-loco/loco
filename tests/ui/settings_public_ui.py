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
    f"""INSERT INTO resource_domains(resource_id, environment_id, domain, domain_source, subdomain_label, app_domain, is_primary)
SELECT '{resource}', id, '{domain}', 'app', '{name}', 'apps.example.test', true
FROM environments WHERE workspace_id='{workspace}' AND slug='production';""",
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
    expect(page.get_by_text("Operator-managed routing", exact=True)).to_have_count(2)
    # Synthetic publication records exercise the real resource API and browser rendering.
    sql(
        state,
        "INSERT INTO route_publishers(app_domain,bootstrapped,change_sequence) VALUES ('apps.example.test',true,1);",
    )
    page.reload()
    expect(page.get_by_text("Routing pending", exact=True)).to_have_count(2)
    sql(
        state,
        f"""INSERT INTO public_route_publications(publisher_id,hostname,resource_id,environment_id,cluster_ids,revision,change_sequence,message)
SELECT b.id,d.domain,d.resource_id,d.environment_id,ARRAY(SELECT cluster_id FROM placements WHERE resource_id=d.resource_id),1,1,'Routing provider temporarily unavailable'
FROM resource_domains d JOIN route_publishers b ON b.app_domain=d.app_domain WHERE d.resource_id='{resource}';""",
    )
    page.reload()
    expect(page.get_by_text("Routing error", exact=True)).to_have_count(2)
    sql(
        state,
        f"UPDATE public_route_publications SET acknowledged_revision=revision,message='',drain_until=NOW()+INTERVAL '1 day' WHERE resource_id='{resource}';",
    )
    page.reload()
    expect(page.get_by_text("Routing published", exact=True)).to_have_count(2)
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
