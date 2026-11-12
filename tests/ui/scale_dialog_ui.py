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
parser = argparse.ArgumentParser(description="Check the bulk Scale dialog against an isolated production dashboard")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/scale-dialog"))
args = parser.parse_args()
os.chdir(repo)
module_spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(module_spec)
module_spec.loader.exec_module(evidence)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="scale-dialog", scenario=str(repo / "tests/ui/fixtures/deployment-history/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]


def rpc(service, method, body):
    return evidence.rpc(state["api_url"], owner["token"], f"loco.{service}.v1.{service.title()}Service/{method}", body)


org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';")
production = next(env["id"] for env in rpc("environment", "ListEnvironments", {"workspaceId": workspace})["environments"] if env["name"] == "production")
cluster = str(uuid.uuid4())
sql = [f"INSERT INTO clusters(id,name,region,provider,is_active,is_default,health_status,last_heartbeat) VALUES('{cluster}','scale-dialog','us-east-1','fixture',true,false,'healthy',NOW());"]
for name, cpu, memory, replicas in (("scale-small", "500m", "512Mi", 1), ("scale-large", "250m", "2Gi", 3)):
    size = {"cpu": cpu, "memory": memory, "minReplicas": replicas, "maxReplicas": replicas}
    resource = rpc("resource", "CreateResource", {"workspaceId": workspace, "name": name, "type": "RESOURCE_TYPE_SERVICE", "spec": {"service": {"regions": {"us-east-1": {"enabled": True, **size}}}}})["resourceId"]
    deployment = str(uuid.uuid4())
    runtime = json.dumps({"build": {"type": "image", "image": f"registry.example.com/{name}:1"}, "port": 8080, **size})
    sql.append(f"INSERT INTO deployments(actor_type,actor_id,method,id,resource_id,cluster_id,region,replicas,status,is_active,message,environment_id,spec,spec_version,started_at) VALUES('user','{owner['id']}','web','{deployment}','{resource}','{cluster}','us-east-1',{replicas},'running',true,'','{production}',{evidence.sql_literal(runtime)},1,NOW());")
    sql.append(f"INSERT INTO placements(resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,ready,ready_replicas,status_phase,status_updated_at) VALUES('{resource}','{cluster}','us-east-1','{deployment}','{production}','{{}}',true,{replicas},'Ready',NOW());")
evidence.psql(state["container"], "BEGIN;\n" + "\n".join(sql) + "\nCOMMIT;")
limits = evidence.rpc(state["api_url"], owner["token"], "loco.config.v1.ConfigService/GetConfig", {})["sizeLimits"]
low, high = limits["minReplicas"], limits["maxReplicas"]
base = state["ui_url"] + f"/org/{org}/wks/{workspace}/resources?env=production"
results = []
with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    try:
        for theme in ("light", "dark"):
            for narrow in (False, True):
                case = f"{theme}{'-narrow' if narrow else ''}"
                context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme)
                auth = {"idToken": owner["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
                context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.MODE_KEY)},{json.dumps(theme)});")
                sent = []

                def scale(route):
                    sent.append(json.loads(route.request.post_data))
                    route.fulfill(content_type="application/json", body="{}")

                context.route("**/loco.resource.v1.ResourceService/ScaleResource", scale)
                page = context.new_page()
                errors = []
                page.on("pageerror", lambda error: errors.append(str(error)))
                try:
                    page.goto(base)
                    page.get_by_role("checkbox", name="Select scale-small").check()
                    page.get_by_role("checkbox", name="Select scale-large").check()
                    page.get_by_role("button", name="Scale", exact=True).click()
                    dialog = page.get_by_role("dialog", name="Scale 2 resources in production")
                    expect(dialog.locator("dd")).to_have_text(["1–3", "0.25–0.5 vCPU", "512 MiB–2 GiB"])
                    replicas = dialog.get_by_role("textbox", name="Replicas per region")
                    cpu = dialog.get_by_role("group", name="CPU per replica", exact=True)
                    memory = dialog.get_by_role("group", name="Memory per replica", exact=True)
                    submit = dialog.get_by_role("button", name="Scale 2", exact=True)
                    expect(replicas).to_have_value("")
                    expect(replicas).to_have_attribute("inputmode", "numeric")
                    expect(dialog.locator('[aria-pressed="true"]')).to_have_count(0)
                    expect(dialog.get_by_text(f"{low} to {high}", exact=True)).to_be_visible()
                    expect(submit).to_be_disabled()
                    evidence.settle(page)
                    box = dialog.bounding_box()
                    assert box["width"] <= page.viewport_size["width"], box
                    page.screenshot(path=out / f"open-{case}.png", animations="disabled")

                    for bad in (str(high + 1), "1.5", "two"):
                        replicas.fill(bad)
                        expect(dialog.get_by_text(f"Enter {low} to {high}", exact=True)).to_be_visible()
                        expect(replicas).to_have_attribute("aria-invalid", "true")
                        expect(submit).to_be_disabled()
                    assert dialog.bounding_box() == box, (box, dialog.bounding_box())
                    page.screenshot(path=out / f"invalid-{case}.png", animations="disabled")
                    replicas.fill("")
                    cpu.get_by_role("button", name="2 vCPU", exact=True).click()
                    expect(submit).to_be_enabled()
                    cpu.get_by_role("button", name="2 vCPU", exact=True).click()
                    expect(cpu.get_by_role("button", name="2 vCPU", exact=True)).to_have_attribute("aria-pressed", "false")
                    expect(submit).to_be_disabled()
                    replicas.fill(str(high))
                    memory.get_by_role("button", name="4 GiB", exact=True).click()
                    expect(submit).to_be_enabled()
                    evidence.settle(page)
                    assert dialog.bounding_box() == box, (box, dialog.bounding_box())
                    page.screenshot(path=out / f"picked-{case}.png", animations="disabled")
                    submit.click()
                    expect(dialog).to_be_hidden()
                    assert len(sent) == 2 and len({request["resourceId"] for request in sent}) == 2, sent
                    for request in sent:
                        assert request["environmentId"] == production, request
                        assert request["replicas"] == high and request["memory"] == "4Gi" and "cpu" not in request, request
                    assert not errors, errors
                    results.append({"theme": theme, "narrow": narrow, "dialog": box})
                except BaseException:
                    page.screenshot(path=out / f"failure-{case}.png", animations="disabled")
                    raise
                finally:
                    context.close()
    finally:
        browser.close()
(out / "results.json").write_text(json.dumps(results, indent=2))
print(f"PASS: {len(results)} scale dialog cases")
