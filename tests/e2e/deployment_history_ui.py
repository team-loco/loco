import argparse
import atexit
import contextlib
import importlib.util
import io
import json
import os
import re
import sys
import time
import uuid
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description="Exercise deployment history against an isolated production API and database")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/e2e/out/deployment-history"))
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)


def failure_hook(kind, error, traceback):
    (out / "exception.json").write_text(json.dumps({"type": kind.__name__, "error": str(error)}, indent=2))
    sys.__excepthook__(kind, error, traceback)


sys.excepthook = failure_hook
started = time.monotonic()
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="deployment-history", scenario=str(repo / "tests/e2e/fixtures/deployment-history/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    print("Preview state:", args.state, flush=True)
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
boot_seconds = time.monotonic() - started
owner = state["users"]["owner"]["token"]
workspace_id = state["workspaces"]["acme/shared"]


def rpc(service, method, body):
    return evidence.rpc(state["api_url"], owner, f"loco.{service}.v1.{service.title()}Service/{method}", body)


def fixture():
    existing = state.get("deployment_history")
    if existing is not None:
        return existing
    environments = rpc("environment", "ListEnvironments", {"workspaceId": workspace_id})["environments"]
    production = next(env["id"] for env in environments if env["name"] == "production")
    staging = rpc("environment", "CreateEnvironment", {"workspaceId": workspace_id, "name": "staging", "type": "ENVIRONMENT_TYPE_STAGING"})["environmentId"]
    resource = rpc("resource", "CreateResource", {"workspaceId": workspace_id, "name": "history-pilot", "type": "RESOURCE_TYPE_SERVICE", "spec": {"service": {"regions": {region: {"enabled": True, "cpu": "100m", "memory": "128Mi", "minReplicas": 1, "maxReplicas": 1} for region in ("us-east-1", "eu-west-1")}}}})["resourceId"]
    clusters = [str(uuid.uuid4()) for _ in range(3)]
    release_ids = [str(uuid.uuid4()) for _ in range(221)]
    staging_release = str(uuid.uuid4())
    sql = []
    for cluster, region, tier in zip(clusters, ("us-east-1", "eu-west-1", "us-east-1"), ("production", "production", "staging"), strict=True):
        sql.append(f"INSERT INTO clusters (id,name,region,provider,is_active,is_default,health_status,last_heartbeat,tier) VALUES ('{cluster}','browser-pilot','{region}','fixture',true,false,'healthy',NOW(),'{tier}');")
    for index, deployment in enumerate(release_ids, 1):
        region = "eu-west-1" if index == len(release_ids) else "us-east-1"
        cluster = clusters[1] if region == "eu-west-1" else clusters[0]
        active = index in (1, len(release_ids))
        status = "running" if active else "succeeded"
        runtime = json.dumps({"build": {"type": "image", "image": f"example.invalid/browser-pilot:release-{index:03}"}, "cpu": "100m", "memory": "128Mi", "minReplicas": 1, "maxReplicas": 1})
        sql.append(f"INSERT INTO deployments (id,resource_id,cluster_id,region,replicas,status,is_active,message,environment_id,spec,spec_version,created_at,started_at) VALUES ('{deployment}','{resource}','{cluster}','{region}',1,'{status}',{str(active).lower()},'Pilot release {index:03}','{production}',{evidence.sql_literal(runtime)},1,NOW()-INTERVAL '{index} minutes',NOW()-INTERVAL '{index} minutes');")
    runtime = json.dumps({"build": {"type": "image", "image": "example.invalid/browser-pilot:staging-current"}, "cpu": "100m", "memory": "128Mi", "minReplicas": 2, "maxReplicas": 2})
    sql.append(f"INSERT INTO deployments (id,resource_id,cluster_id,region,replicas,status,is_active,message,environment_id,spec,spec_version,started_at) VALUES ('{staging_release}','{resource}','{clusters[2]}','us-east-1',2,'running',true,'Staging current','{staging}',{evidence.sql_literal(runtime)},1,NOW());")
    for deployment, cluster, environment, region, replicas in ((release_ids[0], clusters[0], production, "us-east-1", 1), (release_ids[-1], clusters[1], production, "eu-west-1", 1), (staging_release, clusters[2], staging, "us-east-1", 2)):
        sql.append(f"INSERT INTO placements (resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,ready,ready_replicas,status_phase,status_updated_at) VALUES ('{resource}','{cluster}','{region}','{deployment}','{environment}','{{}}',true,{replicas},'Ready',NOW());")
    evidence.psql(state["container"], "BEGIN;" + "\n".join(sql) + "COMMIT;")
    data = {"resource": resource, "production": production, "staging": staging, "releases": release_ids, "staging_release": staging_release}
    state["deployment_history"] = data
    evidence.write_json(Path(args.state), state)
    return data


data = fixture()
first_page = rpc("deployment", "ListDeployments", {"resourceId": data["resource"], "environmentId": data["production"], "pageSize": 200})
assert len(first_page["deployments"]) == 200 and first_page["nextPageToken"]
assert data["releases"][-1] not in [row["id"] for row in first_page["deployments"]]
older = rpc("deployment", "ListDeployments", {"resourceId": data["resource"], "environmentId": data["production"], "pageSize": 200, "pageToken": first_page["nextPageToken"]})
assert len(older["deployments"]) == 21 and not older.get("nextPageToken")
assert rpc("deployment", "GetDeployment", {"deploymentId": data["releases"][-1]})["deployment"]["spec"]["service"]["build"]["image"].endswith("release-221")
org_id = evidence.rpc(state["api_url"], owner, "loco.org.v1.OrgService/ListUserOrgs", {"userId": state["users"]["owner"]["id"]})["orgs"][0]["id"]
base = f"/org/{org_id}/wks/{workspace_id}"
results = []
with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    try:
        for theme in ("light", "dark"):
            for narrow in (False, True):
                evidence.psql(state["container"], f"UPDATE clusters SET last_heartbeat=NOW() WHERE id IN (SELECT cluster_id FROM deployments WHERE resource_id='{data["resource"]}');")
                case = f"{theme}{'-narrow' if narrow else ''}"
                context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme)
                context.set_default_timeout(evidence.STEP_TIMEOUT_MS)
                auth = {"idToken": owner, "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
                context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.THEME_KEY)},{json.dumps(theme)});")
                page = context.new_page()
                errors = []
                requests = []
                page.on("pageerror", lambda error: errors.append(str(error)))
                page.on("request", lambda request: requests.append({"method": request.url.rsplit("/", 1)[-1], "body": request.post_data_json}) if ".DeploymentService/" in request.url else None)
                failed = []

                def history(route):
                    body = route.request.post_data_json
                    if body.get("pageToken") and not failed:
                        failed.append(body)
                        route.fulfill(status=503, json={"code": "unavailable", "message": "Pilot page failure"})
                    else:
                        route.continue_()

                context.route("**/loco.deployment.v1.DeploymentService/ListDeployments", history)
                context.tracing.start(screenshots=True, snapshots=True)
                active_page, active_context = page, context
                stage, user = "history", "owner"
                try:
                    page.goto(state["ui_url"] + base + "/resource/" + data["resource"] + "?env=production")
                    section = page.locator("section").filter(has=page.get_by_role("status").filter(has_text=re.compile(r"^\d+ (loaded|total)")))
                    page.get_by_text("Pilot release 221", exact=True).first.wait_for()
                    expect(section.get_by_role("status")).to_have_text("200 loaded")
                    assert any(request["method"] == "GetDeployment" and request["body"]["deploymentId"] == data["releases"][-1] for request in requests)
                    page.get_by_role("button", name="us-east-1", exact=True).or_(page.get_by_role("button", name="eu-west-1", exact=True)).click()
                    page.get_by_role("menuitemradio", name="eu-west-1", exact=False).click()
                    page.keyboard.press("Escape")
                    page.get_by_text("Pilot release 221", exact=True).first.wait_for()
                    page.screenshot(path=out / f"initial-{case}.png", animations="disabled")
                    stage = "older-page-retry"
                    section.get_by_role("button", name="50", exact=True).click()
                    next_button = section.get_by_role("button", name="Next", exact=True)
                    for _ in range(3):
                        next_button.click()
                    expect(section.get_by_text("Pilot release 151", exact=True)).to_be_visible()
                    next_button.click()
                    section.get_by_text("Pilot page failure", exact=False).wait_for()
                    expect(section.get_by_text("Pilot release 151", exact=True)).to_be_visible()
                    assert len(failed) == 1
                    page.get_by_text("Pilot release 221", exact=True).first.wait_for()
                    page.screenshot(path=out / f"page-error-{case}.png", animations="disabled")
                    next_button.focus()
                    page.keyboard.press("Enter")
                    expect(section.get_by_role("status")).to_have_text("221 total")
                    expect(section.get_by_text("Pilot release 221", exact=True)).to_be_visible()
                    older_requests = [request["body"] for request in requests if request["method"] == "ListDeployments" and request["body"].get("pageToken")]
                    assert len(older_requests) == 2 and older_requests[0] == older_requests[1] == failed[0]
                    page.screenshot(path=out / f"older-{case}.png", animations="disabled")
                    stage = "environment-switch"
                    page.get_by_title("Environment", exact=True).click()
                    page.get_by_role("menuitem", name="staging staging", exact=True).click()
                    expect(page).to_have_url(re.compile(r"[?&]env=staging"))
                    section = page.locator("section").filter(has=page.get_by_role("status").filter(has_text=re.compile(r"^\d+ (loaded|total)")))
                    expect(section.get_by_role("status")).to_have_text("1 total")
                    page.get_by_role("button", name="eu-west-1", exact=True).click()
                    page.get_by_role("menuitemradio", name="us-east-1", exact=False).click()
                    page.keyboard.press("Escape")
                    expect(page.get_by_text("Staging current", exact=True)).to_have_count(2)
                    expect(section.get_by_text("Staging current", exact=True)).to_be_visible()
                    assert not section.get_by_text("Pilot release 221", exact=True).count()
                    assert any(request["method"] == "ListDeployments" and request["body"].get("environmentId") == data["staging"] and not request["body"].get("pageToken") for request in requests)
                    expect(section.get_by_role("button", name="Next", exact=True)).to_be_disabled()
                    assert any(request["method"] == "GetDeployment" and request["body"]["deploymentId"] == data["staging_release"] for request in requests)
                    page.screenshot(path=out / f"staging-{case}.png", animations="disabled")
                    page.get_by_title("Environment", exact=True).click()
                    page.get_by_role("menuitem", name="production production", exact=True).click()
                    expect(section.get_by_text("Pilot release 001", exact=True)).to_be_visible()
                    expect(section.get_by_text("Pilot release 151", exact=True)).to_have_count(0)
                    stage = "owner-settings"
                    page.goto(state["ui_url"] + base + "/settings?tab=ws")
                    owner_name = page.get_by_placeholder("storefront", exact=True)
                    expect(owner_name).to_be_enabled()
                    owner_name.fill("shared-edited")
                    expect(page.get_by_role("button", name="Save", exact=True)).to_be_visible()
                    page.get_by_role("button", name="Discard", exact=True).click()
                    page.screenshot(path=out / f"owner-settings-{case}.png", animations="disabled")
                    member_context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme)
                    member_auth = dict(auth, idToken=state["users"]["member"]["token"])
                    member_context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(member_auth)}));localStorage.setItem({json.dumps(evidence.THEME_KEY)},{json.dumps(theme)});")
                    member_context.tracing.start(screenshots=True, snapshots=True)
                    member_page = member_context.new_page()
                    active_page, active_context = member_page, member_context
                    stage, user = "reader-settings", "member"
                    member_page.on("pageerror", lambda error: errors.append(str(error)))
                    member_page.goto(state["ui_url"] + base + "/settings?tab=ws")
                    expect(member_page.get_by_placeholder("storefront", exact=True)).to_be_disabled()
                    expect(member_page.get_by_role("button", name="Save", exact=True)).to_have_count(0)
                    member_page.screenshot(path=out / f"reader-settings-{case}.png", animations="disabled")
                    member_context.tracing.stop()
                    member_context.close()
                    active_page, active_context = page, context
                    assert not errors, errors
                    results.append({"theme": theme, "narrow": narrow, "errors": errors, "requests": requests, "injectedFailures": len(failed)})
                except BaseException as error:
                    with contextlib.suppress(Exception):
                        active_page.screenshot(path=out / f"failure-{case}.png", animations="disabled")
                    (out / "failure.json").write_text(json.dumps({"case": case, "stage": stage, "user": user, "error": repr(error), "requests": requests, "errors": errors, "completed": results}, indent=2))
                    with contextlib.suppress(Exception):
                        active_context.tracing.stop(path=out / f"failure-{case}.zip")
                    raise
                context.tracing.stop()
                context.close()
    finally:
        browser.close()
(out / "results.json").write_text(json.dumps({"bootSeconds": round(boot_seconds, 2), "totalSeconds": round(time.monotonic() - started, 2), "cases": results}, indent=2))
print(f"PASS: {len(results)} deployment-history cases", flush=True)
