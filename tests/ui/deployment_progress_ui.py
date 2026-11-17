import argparse
import json
import re
import uuid
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import expect
from harness import evidence, load_state, open_page, repo, session

parser = argparse.ArgumentParser(description="Check deployment progress, readiness polling and recovery")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "deployment-progress", "tests/ui/fixtures/deployment-history/scenario.json")
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]


def call(method, body):
    return evidence.rpc(state["api_url"], owner["token"], method, body)


def sql(statement):
    return evidence.psql(state["container"], statement)


if "progress" not in state:
    org = sql(f"SELECT org_id FROM workspaces WHERE id='{workspace}';")
    production = next(env["id"] for env in call("loco.environment.v1.EnvironmentService/ListEnvironments", {"workspaceId": workspace})["environments"] if env["name"] == "production")
    resource = call("loco.resource.v1.ResourceService/CreateResource", {"workspaceId": workspace, "name": "progress-ui", "type": "RESOURCE_TYPE_SERVICE", "spec": {"service": {"routing": {"port": 8080}, "regions": {"us-east-1": {"enabled": True, "cpu": "100m", "memory": "128Mi", "minReplicas": 2, "maxReplicas": 2}}}}})["resourceId"]
    cluster, previous, current, build = (str(uuid.uuid4()) for _ in range(4))
    old_image = "registry.example.test/progress:v1@sha256:" + "1" * 64
    image = "registry.example.test/progress:v2@sha256:" + "2" * 64
    old_spec = {"observability": {"injectOtel": True}, "build": {"type": "image", "image": old_image}, "cpu": "100m", "memory": "128Mi", "minReplicas": 2, "maxReplicas": 2, "port": 8080}
    spec = {**old_spec, "build": {"type": "dockerfile", "image": image, "buildId": build}, "healthCheck": {"path": "/health", "intervalSeconds": 10, "timeoutSeconds": 2, "failureThreshold": 3}}
    sql(f"""BEGIN;
    INSERT INTO clusters(id,name,region,provider,is_active,is_default,health_status,last_heartbeat) VALUES('{cluster}','progress-ui','us-east-1','fixture',true,false,'healthy',NOW());
    INSERT INTO builds(id,resource_id,status,source_type,source_key,source_size,dockerfile_path,image_repository,image_digest,created_by,started_at,finished_at,git_commit,git_branch,git_commit_message)
    VALUES('{build}','{resource}','succeeded','upload','progress-{build}',100,'Dockerfile','registry.example.test/progress','sha256:{'2' * 64}','{owner['id']}',NOW()-INTERVAL '2 minutes',NOW()-INTERVAL '52 seconds','{'a' * 40}','main','Show saved cards');
    INSERT INTO deployments(id,resource_id,cluster_id,region,replicas,status,is_active,environment_id,spec,spec_version,actor_type,actor_id,method,created_at,started_at,completed_at)
    VALUES('{previous}','{resource}','{cluster}','us-east-1',2,'running',true,'{production}',{evidence.sql_literal(json.dumps(old_spec))},1,'user','{owner['id']}','web',NOW()-INTERVAL '10 minutes',NOW()-INTERVAL '10 minutes',NOW()-INTERVAL '9 minutes');
    INSERT INTO deployments(id,resource_id,cluster_id,region,replicas,status,is_active,environment_id,spec,spec_version,actor_type,actor_id,method,created_at,started_at,commit_sha)
    VALUES('{current}','{resource}','{cluster}','us-east-1',2,'building',false,'{production}',{evidence.sql_literal(json.dumps(spec))},2,'user','{owner['id']}','web',NOW()-INTERVAL '45 seconds',NOW()-INTERVAL '45 seconds','{'a' * 40}');
    INSERT INTO placements(resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,ready,ready_replicas,status_phase,status_updated_at)
    VALUES('{resource}','{cluster}','us-east-1','{previous}','{production}','{{}}',true,2,'Ready',NOW());
    COMMIT;""")
    state["progress"] = {"org": org, "environment": production, "resource": resource, "cluster": cluster, "previous": previous, "current": current, "old_image": old_image}
    evidence.write_json(Path(args.state), state)

p = state["progress"]
path = f"/org/{p['org']}/wks/{workspace}/resource/{p['resource']}?env=production"


def phase(status):
    building = status in ("building", "canceled")
    message = {"building": "", "deploying": "Waiting for replicas to become ready", "failed": "The service keeps exiting soon after it starts. Check its logs for the error.", "canceled": "", "running": ""}[status]
    sql(f"""BEGIN;
    UPDATE clusters SET last_heartbeat=NOW() WHERE id='{p['cluster']}';
    UPDATE deployments SET is_active=false WHERE resource_id='{p['resource']}';
    UPDATE deployments SET is_active=true WHERE id='{p['previous'] if building else p['current']}';
    UPDATE deployments SET status='{status}',updated_at=NOW(),completed_at={'NULL' if status in ('building', 'deploying', 'running') else 'NOW()'} WHERE id='{p['current']}';
    UPDATE placements SET deployment_id='{p['previous'] if building else p['current']}',ready={'true' if building or status == 'running' else 'false'},ready_replicas={2 if building or status == 'running' else 1},status_phase='{'Ready' if status == 'running' or building else 'Deploying'}',status_message={evidence.sql_literal(message)},status_updated_at=NOW() WHERE resource_id='{p['resource']}';
    INSERT INTO deployment_rollouts(deployment_id,revision,snapshot)
    VALUES('{p['current']}',1,{evidence.sql_literal(json.dumps({'problem': {'reason': 'crash_loop'}} if status == 'failed' else {}))})
    ON CONFLICT(deployment_id,revision) DO UPDATE SET snapshot=EXCLUDED.snapshot;
    COMMIT;""")


with session(repo / "tests/ui/out/deployment-progress") as browser:
    phase("building")
    context, page, errors = open_page(browser, state=state, permissions=["clipboard-read", "clipboard-write"])
    page.goto(state["ui_url"] + path)
    panel = page.get_by_role("region", name="Deploying v2", exact=True)
    expect(panel).to_be_visible()
    expect(panel.get_by_text("Show saved cards", exact=True)).to_be_visible()
    expect(panel.get_by_text("main · 1m 08s · Succeeded", exact=True)).to_be_visible()
    expect(panel.get_by_role("listitem").filter(has_text="Queued")).to_contain_text("active")
    expect(panel.get_by_text(re.compile(r"of .* ready"))).to_have_count(0)
    panel.get_by_role("button", name="Copy deployment ID", exact=True).click()
    assert page.evaluate("navigator.clipboard.readText()") == p["current"]
    panel.get_by_role("button", name="What’s being deployed", exact=True).click()
    expect(panel.get_by_text("0.1 vCPU · 128 MiB", exact=True)).to_be_visible()
    expect(panel.get_by_text("/health every 10s", exact=True)).to_be_visible()
    phase("deploying")
    page.reload()
    panel = page.get_by_role("region", name="Deploying v2", exact=True)
    expect(panel.get_by_text("1 of 2 ready", exact=True)).to_be_visible()
    sql(f"UPDATE placements SET ready_replicas=2,status_updated_at=NOW() WHERE resource_id='{p['resource']}';")
    expect(panel.get_by_text("2 of 2 ready", exact=True)).to_be_visible(timeout=25000)
    phase("failed")
    page.reload()
    panel = page.get_by_role("region", name="Deployment of v2 failed", exact=True)
    expect(panel.get_by_text("The service keeps exiting soon after it starts. Check its logs for the error.", exact=True).first).to_be_visible()
    logs = panel.get_by_role("link", name="View logs", exact=True)
    query = parse_qs(urlparse(logs.get_attribute("href")).query)
    assert query.get("r") == ["progress-ui"] and "@resource:" not in query.get("q", [""])[0], query
    panel.get_by_role("button", name="Roll back to v1", exact=True).click()
    dialog = page.get_by_role("dialog", name="Roll back progress-ui in us-east-1", exact=True)
    expect(dialog).to_be_visible()
    mutations = []

    def rollback(route):
        mutations.append(route.request.post_data_json)
        route.fulfill(json={"deploymentId": str(uuid.uuid4())})

    context.route("**/loco.deployment.v1.DeploymentService/CreateDeployment", rollback)
    dialog.get_by_role("button", name="Roll back", exact=True).click()
    expect(dialog).to_be_hidden()
    assert len(mutations) == 1 and mutations[0]["spec"]["service"]["build"]["image"] == p["old_image"], mutations
    assert mutations[0]["environmentId"] == p["environment"] and mutations[0]["region"] == "us-east-1", mutations
    phase("canceled")
    page.reload()
    expect(page.get_by_role("region", name="Deployment of v2 canceled", exact=True)).to_be_visible()
    phase("running")
    page.reload()
    expect(page.get_by_role("heading", name="progress-ui", exact=True)).to_be_visible()
    expect(page.get_by_role("list", name="Deployment status", exact=True)).to_have_count(0)
    assert not errors, errors
    context.close()

    phase("failed")
    context, page, errors = open_page(browser, "member", narrow=True, state=state)
    page.goto(state["ui_url"] + path)
    panel = page.get_by_role("region", name="Deployment of v2 failed", exact=True)
    expect(panel.get_by_role("button", name="Roll back to v1", exact=True)).to_be_disabled()
    expect(panel.get_by_role("link", name="View logs", exact=True)).to_be_visible()
    assert panel.bounding_box()["width"] <= page.viewport_size["width"]
    assert not errors, errors
    context.close()

    phase("deploying")
    context, page, errors = open_page(browser, state=state, reduced_motion="reduce")
    page.goto(state["ui_url"] + path)
    panel = page.get_by_role("region", name="Deploying v2", exact=True)
    expect(panel.get_by_text("1 of 2 ready", exact=True)).to_be_visible()
    assert panel.evaluate("node => node.getAnimations({subtree:true}).filter(animation => animation.effect.getTiming().iterations === Infinity).length") == 0
    assert not errors, errors
    context.close()
    browser.close()
print("Deployment progress phases, readiness polling, logs, rollback and reduced motion passed")
