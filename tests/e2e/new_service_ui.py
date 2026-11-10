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
            ask = page.get_by_role("dialog", name="New resource")
            expect(ask.get_by_text("You can't create resources here", exact=True)).to_be_visible()
            ask.get_by_role("button", name="Close", exact=True).last.click()
            expect(page).to_have_url(re.compile(r"/resources\?env=production$"))
            assert not errors, errors
        except BaseException:
            page.screenshot(path=out / "failure-member.png", animations="disabled")
            raise
        finally:
            context.close()

        repository = f"shop-{suffix}"
        image = f"registry.example.com/acme/{repository}:1.4.2"
        for theme, narrow, name in (("light", False, repository), ("dark", True, f"{repository}-2")):
            case = f"{theme}{'-narrow' if narrow else ''}"
            context, page, errors = open_page(browser, "owner", theme, narrow)
            deployments = []
            rejected = []

            def create_deployment(route):
                if theme == "light" and not rejected:
                    rejected.append(route.request.post_data_json)
                    route.fulfill(status=400, json={"code": "invalid_argument", "message": "invalid request"})
                    return
                deployments.append(route.request.post_data_json)
                route.fulfill(json={"deploymentId": str(uuid.uuid4())})

            context.route("**/loco.deployment.v1.DeploymentService/CreateDeployment", create_deployment)
            try:
                page.goto(base + "/resources?env=production")
                page.get_by_role("button", name="New resource", exact=True).click()
                page.get_by_role("menuitem", name="Service", exact=True).click()
                expect(page).to_have_url(re.compile(r"/resources\?env=production&new=1$"))
                ask = page.get_by_role("dialog", name="New resource")
                image_input = ask.get_by_role("textbox", name="Container image")
                expect(image_input).to_be_focused()
                ask.get_by_role("button", name="Continue", exact=True).click()
                expect(ask.get_by_text("Enter an image", exact=True)).to_be_visible()
                expect(ask.get_by_role("textbox", name="Name")).to_have_count(0)
                image_input.fill(image)
                name_input = ask.get_by_role("textbox", name="Name")
                expect(name_input).to_have_value(name)
                if theme == "dark":
                    name_input.fill(repository)
                    expect(ask.get_by_text(f"{repository} already exists in this workspace", exact=True)).to_be_visible()
                    name_input.fill(name)
                page.screenshot(path=out / f"image-{case}.png", animations="disabled")
                ask.get_by_role("button", name="Continue", exact=True).click()

                drawer = page.get_by_role("dialog", name=name, exact=True)
                expect(drawer.get_by_text(image, exact=True)).to_be_visible()
                expect(drawer.get_by_role("button", name="us-east-1")).to_have_attribute("aria-pressed", "true")
                expect(drawer.get_by_role("button", name="Public", exact=True)).to_have_attribute("aria-pressed", "true")
                subdomain = drawer.get_by_role("textbox", name="Subdomain")
                expect(subdomain).to_have_value(name)
                expect(drawer.get_by_text(f"{name}.{platform_domain} is available", exact=True)).to_be_visible()
                subdomain.fill("x" * 64)
                expect(drawer.get_by_text("Use at most 63 characters", exact=True)).to_be_visible()
                subdomain.fill(name)
                expect(drawer.get_by_text(f"{name}.{platform_domain} is available", exact=True)).to_be_visible()
                expect(drawer.get_by_role("switch", name="Autoscaling")).to_be_disabled()
                drawer.get_by_role("group", name="Replicas per region").get_by_role("button", name="Increase").click()
                expect(drawer.get_by_text("Runs exactly 2 per region.", exact=True)).to_be_visible()

                drawer.get_by_role("button", name="Paste .env", exact=True).click()
                paste = drawer.get_by_role("textbox", name="Paste .env")
                expect(drawer.get_by_role("button", name="Add 0", exact=True)).to_be_disabled()
                paste.fill('# billing\n\nexport STRIPE_API_KEY="sk_test"\nLOG_LEVEL=debug\nnot a variable\nLOG_LEVEL=info')
                drawer.get_by_role("button", name="Add 2", exact=True).click()
                expect(paste).to_have_count(0)
                variables = drawer.get_by_role("list", name="Variables")
                names = variables.get_by_role("textbox", name="Variable name")
                expect(names.nth(0)).to_have_value("STRIPE_API_KEY")
                expect(names.nth(1)).to_have_value("LOG_LEVEL")
                expect(variables.get_by_role("textbox", name="Value of LOG_LEVEL")).to_have_value("info")
                expect(variables.get_by_role("listitem").first.get_by_label("Looks like a secret")).to_have_count(1)
                expect(variables.get_by_role("listitem").nth(1).get_by_label("Looks like a secret")).to_have_count(0)
                drawer.get_by_role("button", name="Add", exact=True).click()
                expect(names.nth(2)).to_be_focused()
                names.nth(2).fill("EXTRA")
                drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
                expect(drawer.get_by_role("alert")).to_have_text("EXTRA has no value")
                variables.get_by_role("button", name="Remove EXTRA", exact=True).click()
                expect(variables.get_by_role("listitem")).to_have_count(2)
                expect(drawer.get_by_role("heading", name="Environment variables 2")).to_be_visible()

                health = drawer.get_by_role("button", name=re.compile(r"^Health check"))
                expect(health).to_have_attribute("aria-expanded", "false")
                health.click()
                expect(health).to_have_attribute("aria-expanded", "true")
                interval = drawer.get_by_role("textbox", name="Interval, sec")
                health_path = drawer.get_by_role("textbox", name="Path", exact=True).input_value()
                assert health_path.startswith("/"), health_path
                interval.fill("1x5")
                expect(interval).to_have_value("15")
                drawer.get_by_role("textbox", name="Timeout, sec").fill("4")
                drawer.get_by_role("textbox", name="Fail after, checks").fill("6")
                expect(health).to_contain_text(f"{health_path} · every 15s")
                expect(drawer.get_by_text("To production", exact=True)).to_be_visible()
                assert drawer.bounding_box()["width"] <= page.viewport_size["width"]
                page.screenshot(path=out / f"drawer-{case}.png", animations="disabled", full_page=True)
                if theme == "light":
                    interval.fill("0")
                    drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
                    expect(drawer.get_by_text("Must be greater than or equal to 1", exact=True)).to_be_visible()
                    expect(drawer.get_by_role("alert")).to_have_text("Health check interval: Must be greater than or equal to 1")
                    assert not rejected, rejected
                    assert evidence.psql(state["container"], f"SELECT count(*) FROM resources WHERE name='{name}';") == "0"
                    page.screenshot(path=out / f"invalid-{case}.png", animations="disabled")
                    interval.fill("15")
                    expect(drawer.get_by_role("alert")).to_have_count(0)
                    drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
                    expect(drawer.get_by_text(f"Couldn't deploy {image}: Invalid request.", exact=True)).to_be_visible()
                    assert evidence.psql(state["container"], f"SELECT count(*) FROM resources WHERE name='{name}';") == "0"
                    page.screenshot(path=out / f"rejected-{case}.png", animations="disabled")
                drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()

                open_button = drawer.get_by_role("link", name=f"Open {name}", exact=True)
                expect(open_button).to_be_visible()
                expect(drawer.get_by_text(f"Deploying {name}", exact=True)).to_be_visible()
                expect(drawer.get_by_text("Pull image", exact=True)).to_be_visible()
                expect(drawer.get_by_text("Route traffic", exact=True)).to_be_visible()
                expect(drawer.get_by_role("button", name="Done", exact=True)).to_be_visible()
                page.screenshot(path=out / f"deployed-{case}.png", animations="disabled")
                open_button.click()
                expect(page).to_have_url(re.compile(r"/resource/[0-9a-f-]+$"))
                resource = page.url.rsplit("/", 1)[-1]
                assert evidence.psql(state["container"], f"SELECT name FROM resources WHERE id='{resource}';") == name
                assert [(d["resourceId"], d["region"]) for d in deployments] == [(resource, "us-east-1")], deployments
                spec = deployments[0]["spec"]["service"]
                assert spec["build"]["image"] == image, spec
                assert spec["minReplicas"] == 2 and spec["maxReplicas"] == 2, spec
                assert spec["env"] == {"STRIPE_API_KEY": "sk_test", "LOG_LEVEL": "info"}, spec
                check = spec["healthCheck"]
                assert (check["path"], check["intervalSeconds"], check["timeoutSeconds"], check["failureThreshold"]) == (health_path, 15, 4, 6), check
                domain = evidence.psql(state["container"], f"SELECT subdomain_label || '.' || platform_domain FROM resource_domains WHERE resource_id='{resource}';")
                assert domain == f"{name}.{platform_domain}", domain
                assert not errors, errors
            except BaseException:
                page.screenshot(path=out / f"failure-{case}.png", animations="disabled")
                raise
            finally:
                context.close()
    finally:
        browser.close()
print("PASS: new resource flow for the read-only member and two owner cases")
