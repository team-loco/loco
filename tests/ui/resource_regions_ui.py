from functools import partial
import argparse
import json
import uuid
from pathlib import Path

from playwright.sync_api import expect, sync_playwright
from harness import authenticate, evidence, load_state, repo, rpc as api_rpc, launch

parser = argparse.ArgumentParser(description="Check editing regions in resource settings")
parser.add_argument("--state")
parser.add_argument("--out", default=str(repo / "tests/ui/out/resource-regions"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(args, 'resource-regions', 'tests/ui/fixtures/deployment-history/scenario.json')
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]


rpc = partial(api_rpc, state)


org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';")
production = next(env["id"] for env in rpc("environment", "ListEnvironments", {"workspaceId": workspace})["environments"] if env["name"] == "production")
runtime = json.dumps({"build": {"type": "image", "image": "registry.example.com/shop:latest"}, "cpu": "500m", "memory": "512Mi", "minReplicas": 2, "maxReplicas": 2, "port": 8080})
with sync_playwright() as playwright:
    browser = launch(playwright, out)
    try:
        theme = "light"
        resource = rpc("resource", "CreateResource", {"workspaceId": workspace, "name": "regions-" + uuid.uuid4().hex[:8], "type": "RESOURCE_TYPE_SERVICE", "spec": {"service": {"routing": {"port": 8080}, "regions": {"us-east-1": {"enabled": True, "cpu": "500m", "memory": "512Mi", "minReplicas": 2, "maxReplicas": 2}}}}})["resourceId"]
        cluster, deployment = str(uuid.uuid4()), str(uuid.uuid4())
        evidence.psql(state["container"], f"""
INSERT INTO clusters(id,name,region,provider,is_active,is_default,health_status,last_heartbeat) VALUES
('{cluster}','east-{resource}','us-east-1','fixture',true,false,'healthy',NOW()),
('{uuid.uuid4()}','west-{resource}','eu-west-1','fixture',true,false,'healthy',NOW());
INSERT INTO deployments(actor_type,actor_id,method,id,resource_id,cluster_id,region,replicas,status,is_active,message,environment_id,spec,spec_version,started_at)
VALUES('user','{owner['id']}','web','{deployment}','{resource}','{cluster}','us-east-1',2,'running',true,'','{production}',{evidence.sql_literal(runtime)},1,NOW());
INSERT INTO placements(resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,ready,ready_replicas,status_phase,status_updated_at)
VALUES('{resource}','{cluster}','us-east-1','{deployment}','{production}','{{"app_spec":{{}}}}',true,2,'Ready',NOW());
""")
        context = browser.new_context(viewport=evidence.DESKTOP, color_scheme=theme)
        authenticate(context, owner['token'], theme)
        page = context.new_page()
        calls = []

        def record(route):
            calls.append((route.request.url.rsplit("/", 1)[-1], route.request.post_data_json))
            route.continue_()

        page.route("**/loco.resource.v1.ResourceService/UpdateResource", record)
        page.route("**/loco.deployment.v1.DeploymentService/CreateDeployment", record)

        def sql(query):
            return evidence.psql(state["container"], query)

        def stored():
            return json.loads(sql(f"SELECT spec FROM resources WHERE id='{resource}';"))["regions"]

        def save():
            calls.clear()
            page.get_by_role("button", name="Save & redeploy", exact=True).click()

        try:
            page.goto(state["ui_url"] + f"/org/{org}/wks/{workspace}/resource/{resource}?tab=settings")
            west = page.get_by_role("switch", name="Run in eu-west-1", exact=True)
            expect(west).to_be_visible()
            expect(west).to_be_enabled()
            if theme == "dark":
                page.set_viewport_size(evidence.NARROW)
            west.focus()
            page.keyboard.press("Space")
            expect(west).to_be_checked()
            revision = sql(f"SELECT revision FROM resources WHERE id='{resource}';")
            save()
            expect(page.get_by_text("Rolling out in eu-west-1.", exact=False)).to_be_visible()
            assert [call[0] for call in calls] == ["UpdateResource"], calls
            request = calls[0][1]
            assert request["revision"] == revision, request
            added = request["regions"]["targets"]["eu-west-1"]
            assert added["enabled"] and added["cpu"] == "500m" and added["minReplicas"] == 2, added
            assert stored()["eu-west-1"]["enabled"], stored()
            assert sql(f"SELECT count(*) FROM deployments WHERE resource_id='{resource}' AND region='eu-west-1' AND is_active;") == "1"
            # The API leaves new deployments pending without a worker; finish them so later
            # saves are not refused as a rollout in progress.
            sql(f"UPDATE deployments SET status='running' WHERE resource_id='{resource}' AND is_active;")
            expect(west).to_be_checked()

            sql(f"UPDATE resources SET revision=revision+1 WHERE id='{resource}';")
            west.click()
            expect(west).not_to_be_checked()
            save()
            expect(page.get_by_text("changed since you opened it", exact=False)).to_be_visible()
            expect(west).to_be_checked()
            assert stored()["eu-west-1"]["enabled"], stored()

            west.click()
            save()
            expect(page.get_by_text("Stopping in eu-west-1.", exact=False)).to_be_visible()
            assert not stored()["eu-west-1"].get("enabled", False), stored()
            assert sql(f"SELECT count(*) FROM deployments WHERE resource_id='{resource}' AND region='eu-west-1' AND is_active;") == "0"

            west.click()
            page.get_by_role("textbox", name="Tag or digest", exact=True).fill("next")
            save()
            expect(page.get_by_text("Save region, size and scaling changes separately", exact=False)).to_be_visible()
            assert calls == [], calls
            page.get_by_role("button", name="Discard", exact=True).click()

            sql(f"UPDATE resources SET partial='app' WHERE id='{resource}';")
            page.reload()
            expect(page.get_by_text("Managed by loco.yaml", exact=True).first).to_be_visible()
            expect(west).to_be_disabled()
            sql(f"UPDATE resources SET partial=NULL WHERE id='{resource}'; UPDATE deployments SET is_active=false WHERE resource_id='{resource}';")
            page.reload()
            expect(page.get_by_text("Deploy this resource before adding regions", exact=False).first).to_be_visible()
            expect(west).to_be_disabled()
        finally:
            context.close()
    finally:
        browser.close()
print("Region settings passed")
