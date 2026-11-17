from functools import partial
import argparse
import json
import re
import uuid
from pathlib import Path

from playwright.sync_api import expect
from harness import evidence, load_state, open_page as new_page, repo, session

parser = argparse.ArgumentParser(description="Create and deploy a service from the resources page against an isolated API")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/new-service"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(args, 'new-service', 'tests/ui/fixtures/deployment-history/scenario.json')
workspace = state["workspaces"]["acme/shared"]
org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';")
suffix = uuid.uuid4().hex[:6]
platform_domain = "apps.e2e.test"
evidence.psql(state["container"], f"""UPDATE clusters SET is_active=false WHERE provider='fixture';
INSERT INTO clusters(id,name,region,provider,is_active,is_default,health_status,last_heartbeat,domains)
VALUES('{uuid.uuid4()}','new-service-{suffix}','us-east-1','fixture',true,true,'healthy',NOW(),'{{{platform_domain}}}'),
('{uuid.uuid4()}','degraded-{suffix}','eu-west-1','fixture',true,false,'degraded',NOW(),'{{{platform_domain}}}'),
('{uuid.uuid4()}','offline-{suffix}','eu-north-1','fixture',true,false,'unhealthy',NOW()-INTERVAL '1 day','{{{platform_domain}}}');""")
base = state["ui_url"] + f"/org/{org}/wks/{workspace}"
production = evidence.psql(state["container"], f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND name='production';")
replica_limit = evidence.rpc(state["api_url"], state["users"]["owner"]["token"], "loco.config.v1.ConfigService/GetConfig", {})["sizeLimits"]["maxReplicas"]
drafts_key = f"loco:drafts:v1:{org}:{workspace}:{production}"
DRAFT_URL = re.compile(r"/resources\?env=production&draft=[0-9a-f-]+$")


open_page = partial(new_page, state=state)


def stored_drafts(page):
    raw = page.evaluate(f"localStorage.getItem({json.dumps(drafts_key)})")
    return [] if raw is None else [d["name"] for d in json.loads(raw)]


def draft_row(page, name):
    return page.get_by_role("row").filter(has=page.get_by_role("link", name=name, exact=True)).filter(has_text="Not deployed yet")


def seeded_draft(name, image):
    return {"id": str(uuid.uuid4()), "name": name, "image": image, "createdMs": 0, "edits": {}}


def seed_deployment(body, status, replica_state, detail, problem=None):
    cluster, deployment = str(uuid.uuid4()), str(uuid.uuid4())
    replica = {"uid": f"replica-{deployment}", "name": "service-new-1", "node": "worker-east-1", "placement_revision": 1, "state": replica_state, "detail": detail}
    replicas = body["spec"]["service"]["minReplicas"]
    snapshot = {"replicas": [replica]}
    if problem is not None:
        snapshot["problem"] = {"reason": problem, "detail": detail, "replica_name": replica["name"]}
    evidence.psql(state["container"], f"""BEGIN;
INSERT INTO clusters(id,name,region,provider,is_active,is_default,health_status,last_heartbeat)
VALUES('{cluster}','service-{deployment}','{body['region']}','fixture',true,false,'healthy',NOW());
INSERT INTO deployments(id,resource_id,cluster_id,region,replicas,status,is_active,environment_id,spec,spec_version,actor_type,actor_id,method)
VALUES('{deployment}','{body['resourceId']}','{cluster}','{body['region']}',{replicas},'{status}',true,'{body['environmentId']}',{evidence.sql_literal(json.dumps(body['spec']['service']))},1,'user','{state['users']['owner']['id']}','web');
INSERT INTO placements(resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,applied_revision,observed_revision,status_updated_at)
VALUES('{body['resourceId']}','{cluster}','{body['region']}','{deployment}','{body['environmentId']}','{{}}',1,1,NOW());
INSERT INTO deployment_rollouts(deployment_id,revision,snapshot)
VALUES('{deployment}',1,{evidence.sql_literal(json.dumps(snapshot))});
INSERT INTO deployment_activity(deployment_id,seq,revision,at,severity,replica_name,message)
VALUES('{deployment}',1,1,NOW(),'info','','Deployment requested.'),
('{deployment}',2,1,NOW(),'{"error" if problem else "info"}','{replica["name"]}',{evidence.sql_literal(detail)});
COMMIT;""")
    return deployment


with session(out) as browser:
    try:
        context, page, errors = open_page(browser, "member", storage={drafts_key: json.dumps([seeded_draft("member-draft", "registry.example.com/acme/member:1")])})
        try:
            page.goto(base + "/new?env=production")
            expect(page).to_have_url(re.compile(r"/resources\?env=production&new=1$"))
            ask = page.get_by_role("dialog", name="New resource")
            expect(ask.get_by_text("You can't create resources here", exact=True)).to_be_visible()
            ask.get_by_role("button", name="Close", exact=True).last.click()
            expect(page).to_have_url(re.compile(r"/resources\?env=production$"))
            expect(page.get_by_text("Deploy your first resource", exact=True)).to_be_visible()
            expect(page.get_by_text("member-draft")).to_have_count(0)
            page.get_by_role("link", name="New resource", exact=True).click()
            expect(page).to_have_url(re.compile(r"/resources\?env=production&new=1$"))
            page.get_by_role("dialog", name="New resource").get_by_role("button", name="Close", exact=True).last.click()
            page.goto(base + "?env=production")
            expect(page.get_by_text("No resources yet", exact=True)).to_be_visible()
            expect(page.get_by_text("member-draft")).to_have_count(0)
            assert not errors, errors
        except BaseException:
            page.screenshot(path=out / "failure-member.png", animations="disabled")
            raise
        finally:
            context.close()

        loading = seeded_draft("loading-draft", "registry.example.com/acme/loading:1")
        context, page, errors = open_page(browser, "owner", storage={drafts_key: json.dumps([loading])})
        pending_config = []
        context.route("**/loco.config.v1.ConfigService/GetConfig", lambda route: pending_config.append(route), times=1)
        try:
            with page.expect_request("**/loco.config.v1.ConfigService/GetConfig"):
                page.goto(base + f"/resources?env=production&draft={loading['id']}")
            drawer = page.get_by_role("dialog", name="loading-draft", exact=True)
            expect(page.get_by_role("status")).to_be_visible()
            expect(drawer.get_by_role("group", name="Minimum replicas", exact=True)).to_have_count(0)
            expect(drawer.get_by_text("Use at most 0 replicas per region", exact=True)).to_have_count(0)
            for route in pending_config:
                route.continue_()
            expect(drawer.get_by_role("group", name="Minimum replicas", exact=True)).to_be_visible()
            assert not errors, errors
        finally:
            context.close()

        missing = seeded_draft("missing-region", "registry.example.com/acme/missing:1")
        missing["edits"] = {"regions": ["us-east-1", "ap-south-1"]}
        context, page, errors = open_page(browser, "owner", storage={drafts_key: json.dumps([missing])})
        try:
            page.goto(base + f"/resources?env=production&draft={missing['id']}")
            drawer = page.get_by_role("dialog", name="missing-region", exact=True)
            card = drawer.get_by_role("button", name="ap-south-1 No cluster in this region", exact=True)
            expect(card).to_be_enabled()
            expect(card).to_have_attribute("aria-pressed", "true")
            card.click()
            expect(card).to_have_count(0)
            assert not errors, errors
        finally:
            context.close()

        repository = f"shop-{suffix}"
        image = f"registry.example.com/acme/{repository}:1.4.2"
        name = repository
        context, page, errors = open_page(browser, "owner")
        deployments = []
        rejected = []
        resolutions = []
        pinned_image = f"registry.example.com/acme/{repository}@sha256:" + "a" * 64

        def resolve_image(route):
            resolutions.append(route.request.post_data_json)
            if len(resolutions) == 2:
                rejected.append(route.request.post_data_json)
                route.continue_(post_data=json.dumps({**route.request.post_data_json, "image": "UPPER/Case::bad"}))
                return
            route.fulfill(json={"image": pinned_image})

        context.route("**/loco.deployment.v1.DeploymentService/ResolveImage", resolve_image)
        writes = []
        order = []
        creations = []

        def record_resource_create(route):
            creations.append(route.request.post_data_json)
            route.continue_()

        context.route("**/loco.resource.v1.ResourceService/CreateResource", record_resource_create)

        def record_secret_write(route):
            writes.append(route.request.post_data_json)
            order.append(("SetSecrets", route.request.post_data_json["resourceId"]))
            route.continue_()

        context.route("**/loco.secret.v1.SecretService/SetSecrets", record_secret_write)

        def create_deployment(route):
            order.append(("CreateDeployment", route.request.post_data_json["resourceId"]))
            deployments.append(route.request.post_data_json)
            route.fulfill(json={"deploymentId": seed_deployment(route.request.post_data_json, "deploying", "Starting", "Starting application; waiting for readiness.")})

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
            ask.get_by_role("button", name="Continue", exact=True).click()

            expect(page).to_have_url(DRAFT_URL)
            drawer = page.get_by_role("dialog", name=name, exact=True)
            expect(drawer.get_by_text(image, exact=True)).to_be_visible()
            expect(drawer.get_by_text("Draft", exact=True)).to_be_visible()
            assert stored_drafts(page) == [name], stored_drafts(page)
            expect(drawer.get_by_role("button", name="us-east-1", exact=True)).to_have_attribute("aria-pressed", "true")
            offline = drawer.get_by_role("button", name="eu-north-1 Offline", exact=True)
            expect(offline).to_be_disabled()
            expect(drawer.get_by_role("button", name="eu-west-1 degraded", exact=True)).to_be_enabled()
            last_region = drawer.get_by_role("button", name="us-east-1", exact=True)
            expect(last_region).to_have_attribute("aria-disabled", "true")
            expect(last_region).to_have_attribute("title", "A resource needs at least one region")
            last_region.focus()
            expect(last_region).to_be_focused()
            page.keyboard.press("Shift+Tab")
            page.keyboard.press("Tab")
            expect(last_region).to_be_focused()
            page.keyboard.press("Space")
            expect(last_region).to_have_attribute("aria-pressed", "true")
            drawer.get_by_role("button", name=f"Platform domain: {platform_domain}", exact=True).click()
            domain_option = page.get_by_role("menuitemradio", name=re.compile(rf"^{re.escape(platform_domain)}"))
            expect(domain_option.get_by_text("us-east-1", exact=True)).to_be_visible()
            expect(domain_option.get_by_text("healthy", exact=True)).to_have_count(0)
            expect(domain_option.get_by_text("eu-west-1", exact=True)).to_have_count(0)
            page.keyboard.press("Escape")
            expect(drawer.get_by_role("button", name="Public", exact=True)).to_have_attribute("aria-pressed", "true")
            subdomain = drawer.get_by_role("textbox", name="Subdomain")
            expect(subdomain).to_have_value(name)
            expect(drawer.get_by_text(f"{name}.{platform_domain} is available", exact=True)).to_be_visible()
            subdomain.fill("x" * 64)
            expect(drawer.get_by_text("Use at most 63 characters", exact=True)).to_be_visible()
            subdomain.fill(name)
            expect(drawer.get_by_text(f"{name}.{platform_domain} is available", exact=True)).to_be_visible()
            # Nothing defaults a port, and a public service needs one.
            port = drawer.get_by_role("textbox", name="Port", exact=True)
            expect(port).to_have_value("")
            port.fill("8080")
            expect(drawer.get_by_role("button", name="Decrease minimum replicas", exact=True)).to_be_disabled()
            drawer.get_by_role("button", name="Increase minimum replicas", exact=True).click()
            expect(drawer.get_by_text("Runs exactly 2 per region.", exact=True)).to_be_visible()
            cpu = drawer.get_by_role("slider", name="CPU", exact=True)
            memory = drawer.get_by_role("slider", name="Memory", exact=True)
            expect(cpu).to_have_attribute("max", "4")
            expect(memory).to_have_attribute("max", "5")
            cpu.focus()
            page.keyboard.press("Home")
            page.keyboard.press("ArrowRight")
            page.keyboard.press("ArrowRight")
            expect(cpu).to_have_attribute("aria-valuetext", "0.5 vCPU")
            memory.focus()
            page.keyboard.press("Home")
            for _ in range(3):
                page.keyboard.press("ArrowRight")
            expect(memory).to_have_attribute("aria-valuetext", "1 GiB")

            drawer.get_by_role("button", name="Paste .env", exact=True).click()
            paste = drawer.get_by_role("textbox", name="Paste .env")
            paste_trigger = drawer.get_by_role("button", name="Paste .env", exact=True)
            expect(paste_trigger).to_have_attribute("aria-expanded", "true")
            panel_id = paste_trigger.get_attribute("aria-controls")
            assert panel_id and page.locator(f'[id="{panel_id}"]').is_visible()
            paste_trigger.focus()
            page.keyboard.press("Space")
            expect(paste).not_to_be_visible()
            page.keyboard.press("Space")
            expect(paste).to_be_visible()
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
            secret_switch = variables.get_by_role("switch", name="Store STRIPE_API_KEY as secret", exact=True)
            secret_switch.click()
            secret_value = variables.get_by_label("Value of STRIPE_API_KEY", exact=True)
            expect(secret_value).to_have_attribute("type", "password")
            expect(secret_value).to_have_value("sk_test")
            assert "sk_test" not in page.evaluate(f"localStorage.getItem({json.dumps(drafts_key)})")
            # A storage update from another tab must not be masked by the in-memory secret.
            page.evaluate("""key => {
                const drafts = JSON.parse(localStorage.getItem(key));
                const row = drafts[0].edits.vars.find(row => row.key === "LOG_LEVEL");
                row.value = "debug";
                localStorage.setItem(key, JSON.stringify(drafts));
                dispatchEvent(new StorageEvent("storage", {key}));
            }""", drafts_key)
            log_level = variables.get_by_label("Value of LOG_LEVEL", exact=True)
            expect(log_level).to_have_value("debug")
            expect(secret_value).to_have_value("sk_test")
            log_level.fill("info")
            # A draft that cannot be saved reports it once, not on every keystroke.
            save_failed = page.get_by_text("Couldn't save changes to this draft", exact=True)
            for _ in range(2):
                page.evaluate("""key => {
                    window.realSetItem = Storage.prototype.setItem;
                    Storage.prototype.setItem = function (name, value) {
                        if (name === key) throw new DOMException("full", "QuotaExceededError");
                        return window.realSetItem.call(this, name, value);
                    };
                }""", drafts_key)
                log_level.press_sequentially("abc")
                expect(save_failed).to_have_count(1)
                expect(log_level).to_have_value("info")
                page.evaluate("() => { Storage.prototype.setItem = window.realSetItem; }")
                # A successful save shows the next failure again.
                log_level.press_sequentially("x")
                expect(log_level).to_have_value("infox")
                log_level.fill("info")
                expect(save_failed).to_have_count(0, timeout=10_000)
            drawer.get_by_text("loco.yaml", exact=True).first.click()
            expect(drawer.get_by_text("secrets: [STRIPE_API_KEY]", exact=False)).to_be_visible()
            assert "sk_test" not in drawer.inner_text()
            drawer.get_by_text("loco.yaml", exact=True).first.click()
            secret_value.scroll_into_view_if_needed()
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

            page.reload()
            expect(page).to_have_url(DRAFT_URL)
            expect(secret_switch).to_be_checked()
            expect(secret_value).to_have_value("")
            expect(drawer.get_by_text("Runs exactly 2 per region.", exact=True)).to_be_visible()
            expect(memory).to_have_attribute("aria-valuetext", "1 GiB")
            expect(names.nth(0)).to_have_value("STRIPE_API_KEY")
            expect(variables.get_by_role("textbox", name="Value of LOG_LEVEL")).to_have_value("info")
            expect(health).to_contain_text(f"{health_path} · every 15s")
            page.keyboard.press("Escape")
            expect(page).to_have_url(re.compile(r"/resources\?env=production$"))
            row = draft_row(page, name)
            expect(row.get_by_text("Not deployed yet", exact=True)).to_be_visible()
            expect(row.get_by_text("Draft", exact=True)).to_be_visible()
            expect(row.get_by_role("checkbox")).to_have_count(0)
            expect(row.get_by_role("button")).to_have_count(0)
            page.goto(base + "?env=production")
            dashboard_row = draft_row(page, name)
            expect(dashboard_row.get_by_text("Not deployed yet", exact=True)).to_be_visible()
            expect(dashboard_row.get_by_text("Draft", exact=True)).to_be_visible()
            page.get_by_role("button", name="Region: All", exact=True).click()
            expect(page.get_by_role("menuitemradio", name=re.compile(r"^us-east-1\b"))).to_be_visible()
            expect(page.get_by_role("menuitemradio", name=re.compile(r"^eu-north-1\b"))).to_have_count(0)
            page.keyboard.press("Escape")
            expect(dashboard_row.get_by_role("button")).to_have_count(0)
            page.goto(base + "?env=production&view=map")
            node = page.get_by_role("link", name=re.compile(rf"^{re.escape(name)}\s*draft"))
            expect(node).to_be_visible()
            node.click()
            expect(page).to_have_url(re.compile(r"\?env=production&view=map&draft=[0-9a-f-]+$"))
            health.click()
            drawer.get_by_role("textbox", name="Timeout, sec").fill("5")
            page.keyboard.press("Escape")
            page.goto(base + "/resources?env=production")
            row.get_by_role("link", name=name, exact=True).focus()
            page.keyboard.press("Enter")
            expect(page).to_have_url(DRAFT_URL)
            health.click()
            expect(secret_value).to_have_value("")
            secret_value.fill("sk_test")
            expect(drawer.get_by_role("textbox", name="Timeout, sec")).to_have_value("5")
            drawer.get_by_role("textbox", name="Timeout, sec").fill("4")
            interval.fill("0")
            drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
            expect(drawer.get_by_text("Must be greater than or equal to 1", exact=True)).to_be_visible()
            expect(drawer.get_by_role("alert")).to_have_text("Health check interval: Must be greater than or equal to 1")
            assert not rejected, rejected
            assert evidence.psql(state["container"], f"SELECT count(*) FROM resources WHERE name='{name}';") == "0"
            interval.fill("15")
            expect(drawer.get_by_role("alert")).to_have_count(0)
            creations_before = len(creations)
            with page.expect_response(lambda response: response.url.endswith("/loco.deployment.v1.DeploymentService/ResolveImage")) as resolution:
                drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
            details = resolution.value.json()["details"]
            assert [detail["type"] for detail in details] == ["google.rpc.BadRequest", "google.rpc.ErrorInfo"], details
            expect(drawer.get_by_role("alert")).to_have_text("Container image: Invalid image reference. Use a repository with a tag or digest.")
            assert len(creations) == creations_before, creations
            assert evidence.psql(state["container"], f"SELECT count(*) FROM resources WHERE name='{name}';") == "0"
            assert stored_drafts(page) == [name], stored_drafts(page)
            assert not order, order
            # When SetSecrets fails and the cleanup delete fails too, nothing deployed and the draft stays.
            unavailable = lambda route: route.fulfill(status=503, json={"code": "unavailable"})
            page.route("**/loco.secret.v1.SecretService/SetSecrets", unavailable)
            page.route("**/loco.resource.v1.ResourceService/DeleteResource", unavailable)
            drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
            expect(drawer.get_by_text("Couldn't store the secrets.", exact=True)).to_be_visible()
            assert not deployments, deployments
            assert stored_drafts(page) == [name], stored_drafts(page)
            page.unroute("**/loco.secret.v1.SecretService/SetSecrets")
            page.unroute("**/loco.resource.v1.ResourceService/DeleteResource")
            orphan = evidence.psql(state["container"], f"SELECT id FROM resources WHERE name='{name}';")
            evidence.rpc(state["api_url"], state["users"]["owner"]["token"], "loco.resource.v1.ResourceService/DeleteResource", {"resourceId": orphan})
            drawer.get_by_role("button", name="Done", exact=True).click()
            expect(page).to_have_url(re.compile(r"/resources\?env=production$"))
            draft_row(page, name).get_by_role("link", name=name, exact=True).click()
            expect(page).to_have_url(DRAFT_URL)
            secret_value.fill("sk_test")
            drawer.get_by_role("textbox", name="Description").fill("Takes checkout payments")
            drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()

            open_button = drawer.get_by_role("link", name=f"Open {name}", exact=True)
            expect(open_button).to_be_visible()
            expect(drawer.get_by_text(f"Deploying {name}", exact=True)).to_be_visible()
            new_version = drawer.get_by_role("region", name="New version", exact=True)
            expect(new_version.get_by_text("service-new-1", exact=True)).to_be_visible()
            expect(new_version.get_by_text("worker-east-1", exact=True)).to_be_visible()
            expect(new_version.get_by_text("Starting", exact=True)).to_be_visible()
            expect(drawer.get_by_role("region", name="Previous version", exact=True).get_by_text("No replicas", exact=True)).to_be_visible()
            activity = drawer.get_by_role("region", name="Activity feed", exact=True)
            expect(activity.get_by_role("listitem")).to_have_count(2)
            expect(activity.get_by_role("listitem").last).to_contain_text("Starting application; waiting for readiness.")
            expect(activity.get_by_role("listitem").last).to_contain_text("service-new-1")
            expect(drawer.get_by_role("link", name="View logs", exact=True)).to_be_visible()
            expect(drawer.get_by_role("button", name="Done", exact=True)).to_be_visible()
            header = drawer.get_by_role("heading", name=name, exact=True).locator("..")
            expect(header.get_by_text("Deploying", exact=True)).to_be_visible()
            page.wait_for_function(f"localStorage.getItem({json.dumps(drafts_key)}) === null")
            open_button.click()
            expect(page).to_have_url(re.compile(r"/resource/[0-9a-f-]+$"))
            resource = page.url.rsplit("/", 1)[-1]
            assert evidence.psql(state["container"], f"SELECT name FROM resources WHERE id='{resource}';") == name
            assert evidence.psql(state["container"], f"SELECT description FROM resources WHERE id='{resource}';") == "Takes checkout payments"
            assert [(d["resourceId"], d["region"]) for d in deployments] == [(resource, "us-east-1")], deployments
            spec = deployments[0]["spec"]["service"]
            assert spec["build"]["image"] == pinned_image, spec
            assert spec["port"] == 8080, spec
            assert spec["minReplicas"] == 2 and spec["maxReplicas"] == 2, spec
            assert spec["scalers"] == {}, spec
            assert spec["cpu"] == "500m" and spec["memory"] == "1Gi", spec
            assert spec["env"] == {"LOG_LEVEL": "info"}, spec
            assert deployments[0]["secretBindings"] == {"names": ["STRIPE_API_KEY"]}
            assert writes[-1] == {"resourceId": resource, "environmentId": production, "values": {"STRIPE_API_KEY": "sk_test"}}
            assert [call for call in order if call[1] == resource] == [("SetSecrets", resource), ("CreateDeployment", resource)], order
            names = evidence.rpc(state["api_url"], state["users"]["owner"]["token"], "loco.secret.v1.SecretService/ListSecrets", {"resourceId": resource, "environmentId": production})
            assert [secret["name"] for secret in names["secrets"]] == ["STRIPE_API_KEY"]
            check = spec["healthCheck"]
            assert (check["path"], check["intervalSeconds"], check["timeoutSeconds"], check["failureThreshold"]) == (health_path, 15, 4, 6), check
            domain = evidence.psql(state["container"], f"SELECT subdomain_label || '.' || platform_domain FROM resource_domains WHERE resource_id='{resource}';")
            assert domain == f"{name}.{platform_domain}", domain
            assert not errors, errors
        except BaseException:
            page.screenshot(path=out / "failure-owner.png", animations="disabled")
            raise
        finally:
            context.close()

        # A second service at narrow width: a draft whose name is taken, autoscaling, and a failed rollout.
        name = f"{repository}-2"
        context, page, errors = open_page(browser, "owner", narrow=True)
        deployments = []
        context.route("**/loco.deployment.v1.DeploymentService/ResolveImage", lambda route: route.fulfill(json={"image": pinned_image}))

        def create_failing_deployment(route):
            body = route.request.post_data_json
            deployments.append(body)
            deployment = seed_deployment(body, "failed", "Failing", "ImagePullBackOff: manifest unknown", "image_pull_failed")
            route.fulfill(json={"deploymentId": deployment})

        context.route("**/loco.deployment.v1.DeploymentService/CreateDeployment", create_failing_deployment)
        try:
            page.goto(base + "/resources?env=production")
            stale = seeded_draft(repository, f"registry.example.com/acme/{repository}:0.9")
            page.evaluate(f"localStorage.setItem({json.dumps(drafts_key)},{json.dumps(json.dumps([stale]))})")
            page.reload()
            row = draft_row(page, repository)
            expect(row.get_by_text("Not deployed yet", exact=True)).to_be_visible()
            row.get_by_role("link", name=repository, exact=True).click()
            stale_drawer = page.get_by_role("dialog", name=repository, exact=True)
            expect(stale_drawer.get_by_role("note")).to_have_text(
                f"{repository} already uses this name in this workspace. Deploying this draft will fail; discard it and start again with another name."
            )
            stale_drawer.get_by_role("button", name="Discard draft", exact=True).click()
            confirm = page.get_by_role("alertdialog", name=f"Discard {repository}?")
            confirm.get_by_role("button", name="Discard draft", exact=True).click()
            expect(page.get_by_text(f"Discarded {repository}", exact=True)).to_be_visible()
            expect(page).to_have_url(re.compile(r"/resources\?env=production$"))
            expect(draft_row(page, repository)).to_have_count(0)
            assert stored_drafts(page) == [], stored_drafts(page)

            page.get_by_role("button", name="New resource", exact=True).click()
            page.get_by_role("menuitem", name="Service", exact=True).click()
            ask = page.get_by_role("dialog", name="New resource")
            ask.get_by_role("textbox", name="Container image").fill(image)
            name_input = ask.get_by_role("textbox", name="Name")
            expect(name_input).to_have_value(name)
            name_input.fill(repository)
            expect(ask.get_by_text(f"{repository} already exists in this workspace", exact=True)).to_be_visible()
            name_input.fill(name)
            ask.get_by_role("button", name="Continue", exact=True).click()

            expect(page).to_have_url(DRAFT_URL)
            drawer = page.get_by_role("dialog", name=name, exact=True)
            assert drawer.bounding_box()["width"] <= page.viewport_size["width"]
            drawer.get_by_role("textbox", name="Port", exact=True).fill("8080")
            drawer.get_by_role("button", name="Increase minimum replicas", exact=True).click()
            increase_max = drawer.get_by_role("button", name="Increase maximum replicas", exact=True)
            increase_max.click()
            expect(drawer.get_by_text("Scales per region based on CPU utilization.", exact=True)).to_be_visible()
            expect(drawer.get_by_role("slider", name="CPU target (%)", exact=True)).to_have_value("70")
            for _ in range(replica_limit - 3):
                increase_max.click()
            expect(increase_max).to_be_disabled()
            for _ in range(replica_limit - 3):
                drawer.get_by_role("button", name="Decrease maximum replicas", exact=True).click()
            drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()

            expect(drawer.get_by_text(f"Couldn't deploy {name}", exact=True)).to_be_visible()
            problem = drawer.get_by_role("status").filter(has_text="ImagePullBackOff: manifest unknown")
            expect(problem.get_by_text("ImagePullBackOff: manifest unknown", exact=True)).to_be_visible()
            expect(problem.get_by_text("service-new-1", exact=True)).to_be_visible()
            expect(drawer.get_by_role("region", name="New version", exact=True).get_by_text("Failing", exact=True)).to_be_visible()
            expect(drawer.get_by_role("link", name="View logs", exact=True)).to_have_count(1)
            header = drawer.get_by_role("heading", name=name, exact=True).locator("..")
            expect(header.get_by_text("Failed", exact=True)).to_be_visible()
            spec = deployments[0]["spec"]["service"]
            assert spec["minReplicas"] == 2 and spec["maxReplicas"] == 3, spec
            assert spec["scalers"] == {"enabled": True, "cpuTarget": 70}, spec
            assert not errors, errors
        except BaseException:
            page.screenshot(path=out / "failure-owner-narrow.png", animations="disabled")
            raise
        finally:
            context.close()
    finally:
        browser.close()
print("PASS: new resource drafts for the read-only member and two owner cases")
