from functools import partial
import argparse
import datetime
import json
import re
import uuid
from pathlib import Path

from playwright.sync_api import expect
from harness import evidence, load_state, open_page, repo, rpc as api_rpc, session

parser = argparse.ArgumentParser(description="Check resource details against an isolated production dashboard")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/resource-details"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(args, 'resource-details', 'tests/ui/fixtures/deployment-history/scenario.json')
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]


rpc = partial(api_rpc, state)


org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';")
production = next(env["id"] for env in rpc("environment", "ListEnvironments", {"workspaceId": workspace})["environments"] if env["name"] == "production")
name = "details-" + uuid.uuid4().hex[:8]
resource = rpc("resource", "CreateResource", {"workspaceId": workspace, "name": name, "type": "RESOURCE_TYPE_SERVICE", "spec": {"service": {"routing": {"port": 8080}, "regions": {"us-east-1": {"enabled": True, "cpu": "500m", "memory": "512Mi", "minReplicas": 1, "maxReplicas": 1}}}}})["resourceId"]
cluster, deployment = str(uuid.uuid4()), str(uuid.uuid4())
image = f"registry.example.com/recommendation-{resource}:{deployment}"
runtime = json.dumps({"observability": {"injectOtel": True}, "build": {"type": "image", "image": image}, "cpu": "500m", "memory": "512Mi", "minReplicas": 1, "maxReplicas": 1, "port": 8080, "healthCheck": {"path": "/health", "initialDelaySeconds": 5, "intervalSeconds": 10, "timeoutSeconds": 2, "failureThreshold": 3}})
evidence.psql(state["container"], f"""BEGIN;
UPDATE resources SET partial='recommendation', description='Ranks products for each shopper' WHERE id='{resource}';
INSERT INTO clusters(id,name,region,provider,is_active,is_default,health_status,last_heartbeat) VALUES('{cluster}','resource-details','us-east-1','fixture',true,false,'healthy',NOW());
INSERT INTO deployments(actor_type,actor_id,method,id,resource_id,cluster_id,region,replicas,status,is_active,message,environment_id,spec,spec_version,started_at) VALUES('user','{owner['id']}','web','{deployment}','{resource}','{cluster}','us-east-1',1,'running',true,'','{production}',{evidence.sql_literal(runtime)},1,NOW());
INSERT INTO placements(resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,ready,ready_replicas,status_phase,status_updated_at) VALUES('{resource}','{cluster}','us-east-1','{deployment}','{production}','{{\"app_spec\":{{}}}}',true,1,'Ready',NOW());
INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,actor_type,data)
VALUES('resource.degraded','{org}','{workspace}','{resource}','{production}','agent',jsonb_build_object('reason','crash_looping','message','The service exited with code 1.','region','us-east-1','placementId',(SELECT id::text FROM placements WHERE resource_id='{resource}')));
UPDATE placements SET ready=false,ready_replicas=0,status_phase='Failed',status_message='The pod restarted' WHERE resource_id='{resource}';
COMMIT;""")
assert rpc("resource", "GetResourceEnv", {"resourceId": resource, "environmentId": production}) == {}
base = state["ui_url"] + f"/org/{org}/wks/{workspace}/resource/{resource}"
USAGE = {"min": 0.5, "p50": 0.6, "max": 0.9}
STAMP = datetime.datetime.now(datetime.UTC).replace(microsecond=0).isoformat()


def metrics(route):
    body = route.request.post_data_json
    value = USAGE[body["aggregation"]] if "cpu" in body["metricName"] else 0.25
    route.fulfill(json={"series": [{"resourceId": resource, "points": [{"timestamp": STAMP, "value": value}]}]})


results = []
with session(out) as browser:
    try:
        context, page, errors = open_page(browser, state=state, permissions=["clipboard-read", "clipboard-write"])
        context.route("**/loco.observability.v1.ObservabilityService/QueryMetrics", metrics)
        try:
            page.goto(base)
            expect(page.get_by_text("Last warning", exact=True)).to_be_visible()
            expect(page.get_by_text("The service exited with code 1.", exact=True)).to_be_visible()
            expect(page.get_by_text("limit 0.5 vCPU", exact=True)).to_be_visible()
            expect(page.get_by_text("0.45 vCPU", exact=True)).to_be_visible()
            expect(page.get_by_label("Above 85% of the limit")).to_have_count(1)
            expect(page.get_by_text("band shows lowest to highest of 1 replica", exact=True).first).to_be_visible()
            page.get_by_role("button", name=re.compile(r"^Events")).click()
            runtime_events = page.get_by_role("dialog", name="Events", exact=True)
            expect(runtime_events.get_by_text("crash looping", exact=True)).to_be_visible()
            expect(runtime_events.get_by_text("The service exited with code 1.", exact=True)).to_be_visible()
            runtime_events.get_by_role("button", name="Warnings", exact=True).click()
            expect(runtime_events.get_by_text("The service exited with code 1.", exact=True)).to_be_visible()
            page.keyboard.press("Escape")
            image_label = page.get_by_title(image, exact=True)
            expect(image_label).to_be_visible()
            assert len(image_label.inner_text()) < len(image)
            expect(page.get_by_text(re.compile(r"^deployed .+ by @Olivia Owner$"))).to_be_visible()
            page.get_by_role("button", name="Switch resource", exact=True).click()
            expect(page.get_by_role("menuitem", name=re.compile(rf"^{name}")).get_by_text("Ranks products for each shopper", exact=True)).to_be_visible()
            page.keyboard.press("Escape")
            more = page.get_by_role("button", name="More actions", exact=True)
            more.click()
            page.get_by_role("menuitem", name="Copy image reference", exact=True).click()
            assert page.evaluate("navigator.clipboard.readText()") == image
            expect(page.get_by_text("Managed by loco.yaml", exact=True).first).to_be_visible()
            more.click()
            restart = page.get_by_role("menuitem", name="Restart all replicas", exact=True)
            expect(restart).to_be_enabled()
            with page.expect_response(lambda response: response.url.endswith("/RestartResource")) as restarted:
                restart.click()
            assert restarted.value.ok, restarted.value.text()
            expect(page.get_by_text(f"Restarting {name}", exact=True)).to_be_visible()
            assert evidence.psql(state["container"], f"SELECT deployment_id FROM placements WHERE resource_id='{resource}';") == deployment
            assert evidence.psql(state["container"], f"SELECT COUNT(*) FROM events WHERE type='resource.restarted' AND resource_id='{resource}' AND environment_id='{production}';") == str(len(results) + 1)
            if not results:
                receipt = "01a1e000-1234-7000-8000-000000000123"
                failed_restart = "**/loco.resource.v1.ResourceService/RestartResource"
                context.route(failed_restart, lambda route: route.fulfill(status=500, headers={"x-loco-request-id": receipt, "access-control-expose-headers": "x-loco-request-id"}, json={"code": "internal", "message": "internal error"}))
                more.click()
                page.get_by_role("menuitem", name="Restart all replicas", exact=True).click()
                expect(page.get_by_text(f"Couldn’t restart {name}", exact=True)).to_be_visible()
                copy_receipt = page.get_by_title("Copy request ID", exact=True)
                expect(copy_receipt).to_contain_text(receipt)
                copy_receipt.click()
                assert page.evaluate("navigator.clipboard.readText()") == receipt
                page.wait_for_timeout(8000)
                expect(copy_receipt).to_be_visible()
                context.unroute(failed_restart)
            evidence.psql(state["container"], f"UPDATE deployments SET status='running' WHERE id='{deployment}';")
            more.click()
            page.get_by_role("menuitem", name="View spec", exact=True).click()
            dialog = page.get_by_role("dialog")
            expect(dialog).to_be_visible()
            dialog.evaluate("node => Promise.all(node.getAnimations({subtree: true}).map(animation => animation.finished))")
            geometry = dialog.locator(".overflow-auto").evaluate("e => ({height:e.clientHeight, content:e.scrollHeight, width:e.clientWidth})")
            assert geometry["height"] > 0
            # Long image references scroll within the spec body.
            assert geometry["width"] <= dialog.bounding_box()["width"], geometry
            assert geometry["content"] <= geometry["height"] + 1, geometry
            assert dialog.bounding_box()["width"] <= page.viewport_size["width"]
            page.set_viewport_size(evidence.NARROW)
            expect(dialog).to_be_visible()
            assert dialog.bounding_box()["width"] <= page.viewport_size["width"]
            page.set_viewport_size(evidence.DESKTOP)
            dialog.get_by_role("button", name="Close", exact=True).click()
            expect(more).to_be_focused()
            page.goto(base + "?tab=settings")
            expect(page.get_by_text("No secrets. Add them here or with", exact=False)).to_be_visible()
            count = page.locator("#set-env-title > span")
            expect(count).to_have_text(re.compile(r"^\d+$"))
            delete = page.get_by_role("button", name="Delete resource", exact=True)
            expect(delete).to_be_enabled()
            delete.click()
            dialog = page.get_by_role("alertdialog", name="Delete resource?", exact=True)
            expect(dialog.get_by_text("Deleting here causes drift", exact=False)).to_be_visible()
            expect(dialog.get_by_text("its active deployment in us-east-1, and no domains", exact=False)).to_be_visible()
            expect(dialog.get_by_role("textbox")).to_be_focused()
            expect(dialog.get_by_role("button", name="Delete resource", exact=True)).to_be_disabled()
            dialog.get_by_role("textbox").fill(name.upper())
            expect(dialog.get_by_role("button", name="Delete resource", exact=True)).to_be_disabled()
            dialog.get_by_role("textbox").fill(f" {name} ")
            expect(dialog.get_by_role("button", name="Delete resource", exact=True)).to_be_enabled()
            page.keyboard.press("Escape")
            expect(delete).to_be_focused()
            delete.click()
            dialog.get_by_role("textbox").fill(name)
            held_delete = []
            delete_rpc = "**/loco.resource.v1.ResourceService/DeleteResource"
            context.route(delete_rpc, lambda route: held_delete.append(route))
            confirm = dialog.get_by_role("button", name="Delete resource", exact=True)
            with page.expect_request(delete_rpc):
                confirm.click()
            expect(dialog.get_by_role("textbox")).to_be_disabled()
            expect(dialog.get_by_role("button", name="Cancel", exact=True)).to_be_disabled()
            page.keyboard.press("Escape")
            expect(dialog).to_be_visible()
            held_delete.pop().fulfill(status=503, json={"code": "unavailable", "message": "Try again"})
            expect(page.get_by_text("Try again.", exact=True)).to_be_visible()
            expect(dialog.get_by_role("textbox")).to_have_value(name)
            expect(confirm).to_be_enabled()
            context.unroute(delete_rpc)
            confirm.click()
            expect(page).to_have_url(state["ui_url"] + f"/org/{org}/wks/{workspace}?env=production")
            remaining = evidence.psql(state["container"], f"SELECT COUNT(*) FROM resources WHERE id='{resource}';")
            assert remaining == "0", remaining
            assert not errors, errors
            results.append({"spec": geometry})
        except BaseException:
            page.screenshot(path=out / "failure.png", animations="disabled")
            raise
        finally:
            context.close()
    finally:
        browser.close()
(out / "results.json").write_text(json.dumps(results, indent=2))
print(f"PASS: {len(results)} resource detail cases")
