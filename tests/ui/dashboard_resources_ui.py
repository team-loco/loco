import argparse
import json
import re
from pathlib import Path

from harness import (
    call,
    copied_text,
    expect_rpc,
    load_state,
    open_page,
    repo,
    session,
    sql,
)
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Test the dashboard resource table: row menus, times, filtered-out rows, load failures and an empty environment"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/dashboard-resources"))
args = parser.parse_args()
state = load_state(
    args, "dashboard_resources_ui", "tests/ui/fixtures/activity-drawer/scenario.json"
)
org = state["orgs"]["acme"]["id"]
workspace = state["workspaces"]["acme/shared"]
base = f"{state['ui_url']}/org/{org}/wks/{workspace}"
production = sql(
    state,
    f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND slug='production';",
)
call(
    state,
    "loco.environment.v1.EnvironmentService/CreateEnvironment",
    {"workspaceId": workspace, "name": "Staging", "slug": "staging"},
)
DRAFT = {
    "id": "00000000-0000-4000-8000-0000000000d1",
    "image": "example.invalid/draft:1",
    "name": "draft-api",
    "slug": "draft-api",
    "createdMs": 1_800_000_000_000,
    "edits": {},
}
RESOURCES = "**/loco.resource.v1.ResourceService/ListWorkspaceResources"
DEPLOYMENTS = "**/loco.deployment.v1.DeploymentService/ListDeployments"
failing = False
deployments_failing = False


def resources(route):
    if failing:
        route.fulfill(status=500, json={"code": "internal", "message": "boom"})
    else:
        route.fallback()


def deployments(route):
    if deployments_failing:
        route.fulfill(status=500, json={"code": "internal", "message": "boom"})
    else:
        route.fallback()


out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with session(out) as browser:
    context, page, errors = open_page(
        browser,
        state=state,
        storage={f"loco:drafts:v1:{org}:{workspace}:{production}": json.dumps([DRAFT])},
    )
    page.route(RESOURCES, resources)
    page.route(DEPLOYMENTS, deployments)
    listed = []
    page.on(
        "request",
        lambda request: (
            listed.append(request.post_data_json or {})
            if request.url.endswith("/ListDeployments")
            else None
        ),
    )
    with (
        expect_rpc(
            page,
            "QueryDeploymentBuckets",
            lambda body: (
                body.get("groupByResource") is True
                and len(body.get("resourceIds", [])) > 0
            ),
        ),
        expect_rpc(
            page, "QueryDeploymentBuckets", lambda body: body.get("bucketCount") == 1
        ),
    ):
        page.goto(f"{base}?env=production")
    table = page.get_by_role("table", name="Resources")
    row = table.get_by_role("row").filter(
        has=page.get_by_role("link", name="svc-1", exact=True)
    )
    expect(row).to_contain_text(re.compile(r"(just now|\d+[mhd] ago)$"))
    # The dashboard reads no environment-wide deployment list, and has no map.
    assert all(body.get("pageSize") == 10 for body in listed), listed
    expect(page.get_by_role("button", name="Map", exact=True)).to_have_count(0)

    row.get_by_role("button", name="Actions for svc-1", exact=True).click()
    menu = page.get_by_role("menu")
    expect(menu.get_by_text("svc-1", exact=True)).to_be_visible()
    expect(menu.get_by_role("separator")).to_have_count(2)
    expect(menu.get_by_role("menuitem")).to_have_text(
        [
            "View logs",
            "View events",
            re.compile(r"^Restart\s*1 replica$"),
            "Scale",
            "Settings",
            "Delete",
        ]
    )
    # View events opens the activity drawer on this resource.
    menu.get_by_role("menuitem", name="View events", exact=True).click()
    expect(page).to_have_url(re.compile(r"[?&]activity=svc-1"))
    expect(
        page.get_by_role("dialog", name="svc-1 activity", exact=True)
    ).to_be_visible()
    page.keyboard.press("Escape")
    expect(page).not_to_have_url(re.compile(r"[?&]activity="))
    # Restart asks first, then creates a redeploy of the running deployment.
    created = []

    def create_deployment(route):
        created.append(route.request.post_data_json)
        route.fulfill(json={})

    page.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment", create_deployment
    )
    row.get_by_role("button", name="Actions for svc-1", exact=True).click()
    menu.get_by_role("menuitem", name=re.compile(r"^Restart")).click()
    confirm = page.get_by_role("dialog", name="Restart svc-1", exact=True)
    expect(confirm).to_contain_text("Picks up changed secrets.")
    confirm.get_by_role("button", name="Cancel", exact=True).click()
    expect(confirm).to_have_count(0)
    assert created == [], created
    row.get_by_role("button", name="Actions for svc-1", exact=True).click()
    menu.get_by_role("menuitem", name=re.compile(r"^Restart")).click()
    confirm.get_by_role("button", name="Restart", exact=True).click()
    expect(page.get_by_text("Redeploying svc-1", exact=True)).to_be_visible()
    assert len(created) == 1, created
    assert created[0]["trigger"] == "DEPLOYMENT_TRIGGER_REDEPLOY", created
    page.unroute("**/loco.deployment.v1.DeploymentService/CreateDeployment")
    row.get_by_role("button", name="Actions for svc-1", exact=True).click()
    menu.get_by_role("menuitem", name="Scale", exact=True).click()
    expect(page).to_have_url(re.compile(r"[?&]tab=scaling"))

    page.goto(f"{base}?env=production")
    table.get_by_role("button", name="Actions for draft-api", exact=True).click()
    page.get_by_role("menuitem", name="Open draft", exact=True).click()
    expect(page).to_have_url(re.compile(r"[?&]draft=" + DRAFT["id"]))

    page.goto(f"{base}?env=production&q=zzz-none")
    expect(page.get_by_text("Nothing matches “zzz-none”.")).to_be_visible()
    page.get_by_role("button", name="Clear filters", exact=True).last.click()
    expect(row).to_be_visible()

    failing = True
    page.goto(f"{base}?env=production")
    alert = page.get_by_role("alert")
    expect(alert.get_by_role("heading")).to_have_text("Couldn’t load the dashboard")
    failing = False
    alert.get_by_role("button", name="Retry", exact=True).click()
    expect(row).to_be_visible()

    deployments_failing = True
    page.goto(f"{base}?env=production")
    card = page.get_by_role("region", name="Deployments", exact=True)
    failed = card.get_by_role("alert")
    expect(failed.get_by_role("heading")).to_have_text("Couldn’t load deployments")
    # The copied details name the organization, workspace and environment the failure happened in.
    failed.get_by_role("button", name="Copy details", exact=True).click()
    scope = sql(
        state,
        f"SELECT 'Org: ' || o.name || '  Workspace: ' || w.name || '  Env: ' || e.name FROM environments e "
        f"JOIN workspaces w ON w.id=e.workspace_id JOIN organizations o ON o.id=w.org_id WHERE e.id='{production}';",
    )
    assert scope in copied_text(page).splitlines(), scope
    deployments_failing = False
    failed.get_by_role("button", name="Retry", exact=True).click()
    expect(card.get_by_role("table", name="Deployments")).to_be_visible()

    page.goto(f"{base}?env=staging")
    # The empty environment is named by its name; its slug stays in the URL.
    expect(
        page.get_by_text("Nothing is deployed to Staging.", exact=False)
    ).to_be_visible()
    docs = page.get_by_role("link", name="Read the docs", exact=True)
    expect(docs).to_have_attribute("target", "_blank")
    assert not errors, errors
    context.close()
print("PASS: dashboard resources")
