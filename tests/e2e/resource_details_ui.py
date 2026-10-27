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
parser = argparse.ArgumentParser(description="Check resource details against an isolated production dashboard")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/e2e/out/resource-details"))
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
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="resource-details", scenario=str(repo / "tests/e2e/fixtures/deployment-history/scenario.json")))
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
name = "details-" + uuid.uuid4().hex[:8]
resource = rpc("resource", "CreateResource", {"workspaceId": workspace, "name": name, "type": "RESOURCE_TYPE_SERVICE", "spec": {"service": {"regions": {"us-east-1": {"enabled": True, "cpu": "500m", "memory": "512Mi", "minReplicas": 1, "maxReplicas": 1}}}}})["resourceId"]
cluster, deployment = str(uuid.uuid4()), str(uuid.uuid4())
image = f"registry.example.com/recommendation-{resource}:{deployment}"
runtime = json.dumps({"build": {"type": "image", "image": image}, "cpu": "500m", "memory": "512Mi", "minReplicas": 1, "maxReplicas": 1, "port": 8080, "healthCheck": {"path": "/health", "initialDelaySeconds": 5, "intervalSeconds": 10, "timeoutSeconds": 2, "failureThreshold": 3}})
evidence.psql(state["container"], f"""BEGIN;
UPDATE resources SET partial='recommendation' WHERE id='{resource}';
INSERT INTO clusters(id,name,region,provider,is_active,is_default,health_status,last_heartbeat) VALUES('{cluster}','resource-details','us-east-1','fixture',true,false,'healthy',NOW());
INSERT INTO deployments(id,resource_id,cluster_id,region,replicas,status,is_active,message,environment_id,spec,spec_version,started_at) VALUES('{deployment}','{resource}','{cluster}','us-east-1',1,'running',true,'','{production}',{evidence.sql_literal(runtime)},1,NOW());
INSERT INTO placements(resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,ready,ready_replicas,status_phase,status_updated_at) VALUES('{resource}','{cluster}','us-east-1','{deployment}','{production}','{{}}',true,1,'Ready',NOW());
COMMIT;""")
base = state["ui_url"] + f"/org/{org}/wks/{workspace}/resource/{resource}"
results = []
with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    try:
        for theme in ("light", "dark"):
            for narrow in (False, True):
                case = f"{theme}{'-narrow' if narrow else ''}"
                context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme, permissions=["clipboard-read", "clipboard-write"])
                auth = {"idToken": owner["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
                context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.THEME_KEY)},{json.dumps(theme)});")
                page = context.new_page()
                errors = []
                page.on("pageerror", lambda error: errors.append(str(error)))
                try:
                    page.goto(base)
                    image_button = page.get_by_role("button", name="Copy image " + image, exact=True)
                    expect(image_button).to_be_visible()
                    assert len(image_button.inner_text()) < len(image)
                    image_button.click()
                    assert page.evaluate("navigator.clipboard.readText()") == image
                    expect(page.get_by_text("Managed by loco.yaml", exact=True).first).to_be_visible()
                    view_spec = page.get_by_role("button", name="View spec", exact=True)
                    view_spec.click()
                    dialog = page.get_by_role("dialog")
                    expect(dialog).to_be_visible()
                    geometry = dialog.locator("pre").evaluate("e => ({height:e.clientHeight, content:e.scrollHeight, width:e.clientWidth, fullWidth:e.scrollWidth})")
                    assert geometry["height"] > 0
                    assert geometry["fullWidth"] <= geometry["width"] + 1, geometry
                    if not narrow:
                        assert geometry["content"] <= geometry["height"] + 1, geometry
                    assert dialog.bounding_box()["width"] <= page.viewport_size["width"]
                    dialog.get_by_role("button", name="Close", exact=True).click()
                    expect(view_spec).to_be_focused()
                    page.goto(base + "?tab=variables")
                    expect(page.get_by_text("No secrets for this resource", exact=True)).to_be_visible()
                    secrets = page.locator("section").filter(has=page.get_by_text("Secrets", exact=True))
                    expect(secrets.get_by_role("button", name="Environment", exact=True)).to_have_count(0)
                    expect(page.get_by_text("declares which secrets", exact=False)).to_have_count(0)
                    page.goto(base + "?tab=settings")
                    delete = page.get_by_role("button", name="Delete resource", exact=True)
                    expect(delete).to_be_enabled()
                    delete.click()
                    dialog = page.get_by_role("dialog")
                    expect(dialog.get_by_text("Deleting here causes drift", exact=False)).to_be_visible()
                    expect(dialog.get_by_role("button", name="Cancel", exact=True)).to_be_focused()
                    expect(dialog.get_by_role("button", name="Delete resource", exact=True)).to_be_disabled()
                    dialog.get_by_role("textbox").fill(name)
                    expect(dialog.get_by_role("button", name="Delete resource", exact=True)).to_be_enabled()
                    page.screenshot(path=out / f"delete-{case}.png", animations="disabled")
                    page.keyboard.press("Escape")
                    expect(delete).to_be_focused()
                    if theme == "dark" and narrow:
                        delete.click()
                        dialog.get_by_role("textbox").fill(name)
                        dialog.get_by_role("button", name="Delete resource", exact=True).click()
                        expect(page).to_have_url(state["ui_url"] + f"/org/{org}/wks/{workspace}")
                        remaining = evidence.psql(state["container"], f"SELECT COUNT(*) FROM resources WHERE id='{resource}';")
                        assert remaining == "0", remaining
                    assert not errors, errors
                    results.append({"theme": theme, "narrow": narrow, "spec": geometry})
                except BaseException:
                    page.screenshot(path=out / f"failure-{case}.png", animations="disabled")
                    raise
                finally:
                    context.close()
    finally:
        browser.close()
(out / "results.json").write_text(json.dumps(results, indent=2))
print(f"PASS: {len(results)} resource detail cases")
