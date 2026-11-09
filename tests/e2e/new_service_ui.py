import argparse
import atexit
import contextlib
import importlib.util
import io
import json
import os
import re
import uuid
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description="Create and deploy a service from the resources page against an isolated API")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/e2e/out/new-service"))
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
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="new-service", scenario=str(repo / "tests/e2e/fixtures/deployment-history/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
workspace = state["workspaces"]["acme/shared"]
org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';")
suffix = uuid.uuid4().hex[:6]
platform_domain = "apps.e2e.test"
evidence.psql(state["container"], f"""UPDATE clusters SET is_active=false WHERE provider='fixture';
INSERT INTO clusters(id,name,region,provider,is_active,is_default,health_status,last_heartbeat,domains)
VALUES('{uuid.uuid4()}','new-service-{suffix}','us-east-1','fixture',true,true,'healthy',NOW(),'{{{platform_domain}}}');""")
base = state["ui_url"] + f"/org/{org}/wks/{workspace}"


def open_page(browser, user, theme="light", narrow=False):
    context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme)
    auth = {"idToken": state["users"][user]["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
    context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.MODE_KEY)},{json.dumps(theme)});")
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    return context, page, errors


with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    try:
        context, page, errors = open_page(browser, "member")
        try:
            page.goto(base + "/new?env=production")
            expect(page).to_have_url(re.compile(r"/resources\?env=production&new=1$"))
            ask = page.get_by_role("dialog", name="New service")
            expect(ask.get_by_text("You can't create resources here", exact=True)).to_be_visible()
            ask.get_by_role("button", name="Close", exact=True).last.click()
            expect(page).to_have_url(re.compile(r"/resources\?env=production$"))
            assert not errors, errors
        except BaseException:
            page.screenshot(path=out / "failure-member.png", animations="disabled")
            raise
        finally:
            context.close()

        for theme, narrow in (("light", False), ("dark", True)):
            case = f"{theme}{'-narrow' if narrow else ''}"
            repository = f"shop-{suffix}-{theme}"
            image = f"registry.example.com/acme/{repository}:1.4.2"
            context, page, errors = open_page(browser, "owner", theme, narrow)
            deployments = []

            def create_deployment(route):
                deployments.append(route.request.post_data_json)
                route.fulfill(json={"deploymentId": str(uuid.uuid4())})

            context.route("**/loco.deployment.v1.DeploymentService/CreateDeployment", create_deployment)
            try:
                page.goto(base + "/resources?env=production")
                page.get_by_role("button", name="New resource", exact=True).click()
                page.get_by_role("menuitem", name="Service", exact=True).click()
                expect(page).to_have_url(re.compile(r"/resources\?env=production&new=1$"))
                ask = page.get_by_role("dialog", name="New service")
                image_input = ask.get_by_role("textbox", name="Container image")
                expect(image_input).to_be_focused()
                ask.get_by_role("button", name="Continue", exact=True).click()
                expect(ask.get_by_text("Enter an image", exact=True)).to_be_visible()
                image_input.fill(image)
                page.screenshot(path=out / f"image-{case}.png", animations="disabled")
                ask.get_by_role("button", name="Continue", exact=True).click()

                drawer = page.get_by_role("dialog", name="New service")
                expect(drawer.get_by_text(image, exact=True)).to_be_visible()
                expect(drawer.get_by_role("textbox", name="Name")).to_have_value(repository)
                expect(drawer.get_by_role("button", name="us-east-1")).to_have_attribute("aria-pressed", "true")
                expect(drawer.get_by_role("textbox", name="Subdomain")).to_have_value(repository)
                expect(drawer.get_by_text(f"{repository}.{platform_domain} is available", exact=True)).to_be_visible()
                expect(drawer.get_by_role("group", name="Max replicas").get_by_role("button", name="Increase")).to_be_disabled()
                drawer.get_by_role("group", name="Min replicas").get_by_role("button", name="Increase").click()
                drawer.get_by_role("textbox", name="Environment variables").fill("STRIPE_API_KEY=sk_test\nLOG_LEVEL=info")
                variables = drawer.get_by_role("list", name="Variables")
                expect(variables.get_by_role("listitem")).to_have_text(["STRIPE_API_KEY", "LOG_LEVEL"])
                expect(variables.get_by_role("listitem").first.get_by_label("Looks like a secret")).to_have_count(1)
                expect(variables.get_by_role("listitem").last.get_by_label("Looks like a secret")).to_have_count(0)
                expect(drawer.get_by_text("To production", exact=True)).to_be_visible()
                expect(drawer.get_by_role("combobox", name="Environment")).to_have_count(0)
                assert drawer.bounding_box()["width"] <= page.viewport_size["width"]
                page.screenshot(path=out / f"drawer-{case}.png", animations="disabled")
                drawer.get_by_role("button", name=f"Deploy {repository}", exact=True).click()

                open_button = drawer.get_by_role("button", name=f"Open {repository}", exact=True)
                expect(open_button).to_be_visible()
                expect(drawer.get_by_text("Resource created", exact=True)).to_be_visible()
                page.screenshot(path=out / f"deployed-{case}.png", animations="disabled")
                open_button.click()
                expect(page).to_have_url(re.compile(r"/resource/[0-9a-f-]+$"))
                resource = page.url.rsplit("/", 1)[-1]
                assert evidence.psql(state["container"], f"SELECT name FROM resources WHERE id='{resource}';") == repository
                assert [(d["resourceId"], d["region"]) for d in deployments] == [(resource, "us-east-1")], deployments
                spec = deployments[0]["spec"]["service"]
                assert spec["build"]["image"] == image, spec
                assert spec["minReplicas"] == 2 and spec["maxReplicas"] == 2, spec
                assert spec["env"] == {"STRIPE_API_KEY": "sk_test", "LOG_LEVEL": "info"}, spec
                domain = evidence.psql(state["container"], f"SELECT subdomain_label || '.' || platform_domain FROM resource_domains WHERE resource_id='{resource}';")
                assert domain == f"{repository}.{platform_domain}", domain
                assert not errors, errors
            except BaseException:
                page.screenshot(path=out / f"failure-{case}.png", animations="disabled")
                raise
            finally:
                context.close()
    finally:
        browser.close()
print("PASS: new service flow for the read-only member and two owner cases")
