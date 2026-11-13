import argparse
import atexit
import contextlib
import importlib.util
import io
import json
import os
import uuid
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description="Exercise resource autoscaling and failed-save recovery")
parser.add_argument("--state")
parser.add_argument("--before", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-scaling"))
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="settings-scaling", scenario=str(repo / "tests/ui/fixtures/deployment-history/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]
org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';")
production = evidence.psql(state["container"], f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND name='production';")
defaults = evidence.rpc(state["api_url"], owner["token"], "loco.config.v1.ConfigService/GetConfig", {})["serviceDefaults"]
regions = ("us-east-1", "eu-west-1")
source_commit = {"sha": "a" * 40, "dirty": True}
clusters = {region: str(uuid.uuid4()) for region in regions}
for region, cluster in clusters.items():
    evidence.psql(state["container"], f"INSERT INTO clusters(id,name,region,provider,is_active,is_default,health_status,last_heartbeat) VALUES('{cluster}','scaling-{cluster}','{region}','fixture',true,false,'healthy',NOW());")
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)


def resource_fixture():
    name = "scaling-" + uuid.uuid4().hex[:8]
    resource = evidence.rpc(state["api_url"], owner["token"], "loco.resource.v1.ResourceService/CreateResource", {
        "workspaceId": workspace, "name": name, "type": "RESOURCE_TYPE_SERVICE",
        "spec": {"service": {"regions": {region: {"enabled": True, "cpu": "100m", "memory": "128Mi", "minReplicas": 1, "maxReplicas": 1} for region in regions}}},
    })["resourceId"]
    runtime = json.dumps({"build": {"type": "image", "image": "registry.example.com/app:1"}, "cpu": "100m", "memory": "128Mi", "minReplicas": 1, "maxReplicas": 1, "port": 8080})
    for region in regions:
        deployment = str(uuid.uuid4())
        cluster = clusters[region]
        evidence.psql(state["container"], f"""INSERT INTO deployments(actor_type,actor_id,method,commit_sha,commit_dirty,id,resource_id,cluster_id,region,replicas,status,is_active,message,environment_id,spec,spec_version,started_at)
VALUES('user','{owner['id']}','web','{source_commit['sha']}',true,'{deployment}','{resource}','{cluster}','{region}',1,'running',true,'','{production}',{evidence.sql_literal(runtime)},1,NOW());
INSERT INTO placements(resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,ready,ready_replicas,status_phase,status_updated_at)
VALUES('{resource}','{cluster}','{region}','{deployment}','{production}','{{"app_spec":{{}}}}',true,1,'Ready',NOW());""")
    return resource


with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    try:
        for theme in ("light", "dark"):
            for narrow in (False, True):
                resource = resource_fixture()
                context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme)
                auth = {"idToken": owner["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
                context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.MODE_KEY)},{json.dumps(theme)});")
                page = context.new_page()
                errors = []
                page.on("pageerror", lambda error: errors.append(str(error)))
                calls = []
                failing = [True]

                def update(route):
                    calls.append(route.request.post_data_json)
                    if failing[0]:
                        route.fulfill(status=503, json={"code": "unavailable", "message": "fixture rejected scaling"})
                        return
                    route.continue_()

                context.route("**/loco.resource.v1.ResourceService/UpdateResource", update)
                context.route("**/loco.deployment.v1.DeploymentService/CreateDeployment", lambda route: route.abort())
                case = f"{theme}{'-narrow' if narrow else ''}"
                side = "before" if args.before else "after"
                try:
                    page.goto(state["ui_url"] + f"/org/{org}/wks/{workspace}/resource/{resource}?env=production&tab=settings")
                    section = page.locator("#set-scaling")
                    expect(section).to_be_visible()
                    if not args.before:
                        for region in regions:
                            for _ in range(2):
                                section.get_by_role("button", name=f"Increase maximum replicas in {region}", exact=True).click()
                            section.get_by_role("button", name=f"Decrease cPU target (%) in {region}", exact=True).click()
                    section.evaluate("node => node.scrollIntoView({block: 'start'})")
                    evidence.settle(page, wait_for_network=False)
                    page.wait_for_function("dark => document.documentElement.classList.contains('dark') === dark", arg=theme == "dark")
                    section.screenshot(path=out / f"settings-scaling-{side}-{case}.png", animations="disabled")
                    if args.before:
                        continue

                    def assert_edits(region, target):
                        expect(section.get_by_role("group", name=f"Maximum replicas in {region}", exact=True)).to_contain_text("3")
                        expect(section.get_by_role("group", name=f"CPU target (%) in {region}", exact=True)).to_contain_text(str(target))

                    def stored_regions():
                        return json.loads(evidence.psql(state["container"], f"SELECT spec FROM resources WHERE id='{resource}';"))["regions"]

                    def save_and_settle():
                        calls.clear()
                        save.click()
                        expect(page.get_by_role("button", name="Discard", exact=True)).to_have_count(0)
                        expect(page.get_by_text("Saved scaling-", exact=False)).to_be_visible()
                        # The API leaves new deployments pending without a worker; finish them so the
                        # next save is not refused as a rollout in progress.
                        evidence.psql(state["container"], f"UPDATE deployments SET status='running' WHERE resource_id='{resource}' AND is_active;")
                        evidence.settle(page)

                    target = defaults["cpuTarget"] - 1
                    save = page.get_by_role("button", name="Save & redeploy", exact=True)
                    save.click()
                    expect(page.get_by_text("2 unsaved changes", exact=True)).to_be_visible()
                    expect(page.get_by_text("fixture rejected scaling", exact=False)).to_be_visible()
                    for region in regions:
                        assert_edits(region, target)
                    assert len(calls) == 1, calls
                    for region in regions:
                        sent = calls[0]["regions"]["targets"][region]
                        assert (sent["minReplicas"], sent["maxReplicas"]) == (1, 3), sent
                        assert sent["scalers"] == {"enabled": True, "cpuTarget": target}, sent

                    failing[0] = False
                    save_and_settle()
                    assert len(calls) == 1, calls
                    for region in regions:
                        assert stored_regions()[region]["scalers"] == {"enabled": True, "cpuTarget": target}
                        assert_edits(region, target)
                    commits = evidence.psql(state["container"], f"SELECT DISTINCT commit_sha || ':' || commit_dirty FROM deployments WHERE resource_id='{resource}' AND is_active;")
                    assert commits == f"{source_commit['sha']}:true", commits

                    section.get_by_role("button", name=f"Decrease cPU target (%) in {regions[0]}", exact=True).click()
                    save_and_settle()
                    assert stored_regions()[regions[0]]["scalers"]["cpuTarget"] == target - 1

                    for _ in range(2):
                        section.get_by_role("button", name=f"Decrease maximum replicas in {regions[0]}", exact=True).click()
                    save_and_settle()
                    fixed = stored_regions()[regions[0]]
                    assert fixed["minReplicas"] == fixed["maxReplicas"] == 1, fixed
                    assert not fixed.get("scalers", {}).get("enabled", False), fixed
                    section.get_by_role("button", name=f"Increase maximum replicas in {regions[0]}", exact=True).click()
                    section.get_by_role("button", name=f"Decrease cPU target (%) in {regions[0]}", exact=True).click()
                    section.get_by_role("button", name=f"Decrease maximum replicas in {regions[0]}", exact=True).click()
                    expect(page.get_by_role("button", name="Discard", exact=True)).to_have_count(0)
                    assert not errors, errors
                except BaseException:
                    page.screenshot(path=out / f"failure-{side}-{case}.png", animations="disabled")
                    raise
                finally:
                    context.close()
    finally:
        browser.close()
print("PASS: Settings scaling before screenshots" if args.before else "PASS: Settings scaling through region map saves, with failed-save recovery")
