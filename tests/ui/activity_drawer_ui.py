import argparse
import re
from pathlib import Path

from harness import load_state, open_page, repo, session, sql
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Test the Activity drawer: paging through deployments, degraded and recovered events, and deploy toasts"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/activity-drawer"))
args = parser.parse_args()
state = load_state(
    args, "activity_drawer_ui", "tests/ui/fixtures/activity-drawer/scenario.json"
)
org = state["orgs"]["acme"]["id"]
workspace = state["workspaces"]["acme/shared"]
resource = state["resources"]["acme/shared/svc-1"]
other = state["resources"]["acme/shared/svc-2"]
old = state["resources"]["acme/shared/svc-3"]
owner = state["users"]["owner"]["id"]
environment = sql(
    state,
    f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND slug='production'",
)
sql(
    state,
    f"""INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,actor_type,actor_id,subject_type,request_id,data,created_at)
SELECT kind,'{org}','{workspace}','{resource}','{environment}','user','{owner}','resource','req-activity-drawer',
jsonb_build_object('region','us-ca-1','environmentId','{environment}','reason','out_of_memory'),NOW()-n*INTERVAL '1 minute'
FROM (VALUES(1,'resource.recovered'),(2,'resource.degraded')) AS records(n,kind);""",
)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    # A fake clock lets the test move time forward to check which toasts dismiss themselves.
    page.clock.install()
    drawer_reads = []

    def record_drawer_read(request):
        if request.url.endswith(".DeploymentService/ListDeployments"):
            body = request.post_data_json
            # The dashboard's Deployments card lists 10 rows of the last day; the drawer lists more.
            if "startTime" in body and body.get("pageSize") != 10:
                drawer_reads.append(body)

    page.on("request", record_drawer_read)
    page.goto(f"{state['ui_url']}/org/{org}/wks/{workspace}?env=production")
    activity = page.get_by_role("button", name=re.compile(r"^Workspace activity, "))
    expect(page.get_by_role("link", name="svc-1", exact=True).first).to_be_visible()
    page.wait_for_timeout(1000)
    assert drawer_reads == [], drawer_reads
    # A deployment event while the drawer is closed toasts what changed, with a link to the resource.
    sql(
        state,
        f"""UPDATE deployments SET created_at=NOW(),updated_at=NOW() WHERE id=(SELECT id FROM deployments WHERE resource_id='{resource}' ORDER BY created_at DESC LIMIT 1);
INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,actor_type,actor_id,subject_type,request_id,data,created_at)
VALUES('deployment.ready','{org}','{workspace}','{resource}','{environment}','user','{owner}','deployment','req-activity-toast',
jsonb_build_object('environmentId','{environment}'),NOW());""",
    )
    toast = page.locator("[data-sonner-toast]").filter(
        has_text="svc-1 is live on fixture"
    )
    expect(toast).to_be_visible()
    toast.get_by_role("button", name="View", exact=True).click()
    expect(page).to_have_url(re.compile(rf"/resource/{resource}"))
    expect(toast).to_have_count(0)
    activity.click()
    drawer = page.get_by_role("dialog", name="Workspace activity", exact=True)
    expect(drawer.get_by_text("Degraded in us-ca-1", exact=True)).to_be_visible()
    expect(drawer.get_by_text("Recovered in us-ca-1", exact=True)).to_be_visible()
    deployed = drawer.get_by_text("Deployed", exact=True)
    expect(deployed).to_have_count(6)
    # A deploy card says what the deployment runs, not its commit message.
    expect(drawer.get_by_text("Deploy fixture", exact=True).first).to_be_visible()
    drawer.get_by_role("button", name="Show 2 more", exact=True).click()
    expect(deployed).to_have_count(8)
    expect(drawer.get_by_role("button", name="Show 2 more")).to_have_count(0)
    assert drawer_reads
    # The Warnings level keeps the degraded event and drops the recovered one; the resource filter scopes the drawer and its title.
    drawer.get_by_role("button", name="Warnings", exact=True).click()
    expect(page).to_have_url(re.compile(r"[?&]activity-level=warn"))
    expect(drawer.get_by_text("Recovered in us-ca-1", exact=True)).to_have_count(0)
    expect(drawer.get_by_text("Degraded in us-ca-1", exact=True)).to_be_visible()
    drawer.get_by_role("button", name="Resource: All resources", exact=True).click()
    page.get_by_role("menuitemradio", name="svc-2", exact=True).click()
    expect(page).to_have_url(re.compile(r"[?&]activity=svc-2"))
    scoped = page.get_by_role("dialog", name="svc-2 activity", exact=True)
    expect(scoped).to_be_visible()
    expect(scoped.get_by_text("Degraded in us-ca-1", exact=True)).to_have_count(0)
    expect(scoped.get_by_text("No warnings.", exact=True)).to_be_visible()
    scoped.get_by_role("button", name="All", exact=True).click()
    page.get_by_role("button", name="Close workspace activity", exact=True).click()
    # A deploy that started before the page loaded and fails after it still toasts, until dismissed.
    sql(
        state,
        f"""UPDATE deployments SET status='failed',updated_at=NOW() WHERE id=(SELECT id FROM deployments WHERE resource_id='{other}' ORDER BY created_at DESC LIMIT 1);
INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,actor_type,actor_id,subject_type,request_id,data,created_at)
VALUES('deployment.failed','{org}','{workspace}','{other}','{environment}','user','{owner}','deployment','req-activity-failed',
jsonb_build_object('environmentId','{environment}'),NOW());""",
    )
    failed = page.locator("[data-sonner-toast]").filter(has_text="svc-2 deploy failed")
    expect(failed).to_be_visible()
    # A deployment created an hour before the page loaded that runs out of memory toasts from its degraded event.
    sql(
        state,
        f"""UPDATE deployments SET created_at=NOW()-INTERVAL '1 hour',updated_at=NOW()-INTERVAL '1 hour' WHERE resource_id='{old}';
INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,deployment_id,actor_type,actor_id,subject_type,request_id,data,created_at)
SELECT 'resource.degraded','{org}','{workspace}','{old}','{environment}',id,'agent','{owner}','resource','req-activity-degraded',
jsonb_build_object('region','us-ca-1','environmentId','{environment}','reason','out_of_memory','message','The service ran out of memory.'),NOW()
FROM deployments WHERE resource_id='{old}' ORDER BY created_at DESC LIMIT 1;""",
    )
    degraded = page.locator("[data-sonner-toast]").filter(
        has_text="svc-3: out of memory"
    )
    expect(degraded).to_contain_text("The service ran out of memory.")
    # Warnings dismiss themselves after 9 seconds; a failed deploy stays until dismissed.
    page.mouse.move(0, 0)
    page.clock.fast_forward(20_000)
    expect(degraded).to_have_count(0)
    expect(failed).to_be_visible()
    assert not errors, errors
    context.close()
print("PASS: activity drawer")
