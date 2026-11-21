import argparse
import re
from pathlib import Path

from harness import load_state, open_page, repo, session, sql
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Test the Activity drawer: paging through deployments and degraded and recovered events"
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
owner = state["users"]["owner"]["id"]
environment = sql(
    state,
    f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND slug='production'",
)
sql(
    state,
    f"""INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,actor_type,actor_id,subject_type,request_id,data,created_at)
SELECT kind,'{org}','{workspace}','{resource}','{environment}','user','{owner}','resource','req-activity-drawer',
jsonb_build_object('region','us-east-1','environmentId','{environment}','reason','out_of_memory'),NOW()-n*INTERVAL '1 minute'
FROM (VALUES(1,'resource.recovered'),(2,'resource.degraded')) AS records(n,kind);""",
)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    drawer_reads = []

    def record_drawer_read(request):
        if request.url.endswith("/DeploymentService/ListDeployments"):
            body = request.post_data_json
            if "since" in body:
                drawer_reads.append(body)

    page.on("request", record_drawer_read)
    page.goto(f"{state['ui_url']}/org/{org}/wks/{workspace}?env=production")
    activity = page.get_by_role("button", name=re.compile(r"^Workspace activity, "))
    expect(page.get_by_role("link", name="svc-1", exact=True).first).to_be_visible()
    page.wait_for_timeout(1000)
    assert drawer_reads == [], drawer_reads
    activity.click()
    drawer = page.get_by_role("dialog", name="Workspace activity", exact=True)
    expect(drawer.get_by_text("Degraded in us-east-1", exact=True)).to_be_visible()
    expect(drawer.get_by_text("Recovered in us-east-1", exact=True)).to_be_visible()
    deployed = drawer.get_by_text("Deployed", exact=True)
    expect(deployed).to_have_count(6)
    drawer.get_by_role("button", name="Show 2 more", exact=True).click()
    expect(deployed).to_have_count(8)
    expect(drawer.get_by_role("button", name="Show 2 more")).to_have_count(0)
    assert drawer_reads
    assert not errors, errors
    context.close()
print("PASS: activity drawer")
