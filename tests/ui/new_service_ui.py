import argparse
import json
import re
import uuid
from functools import partial
from pathlib import Path
from urllib.parse import urlparse

from harness import (
    call,
    evidence,
    expect_rpc,
    load_state,
    open_page as new_page,
    repo,
    session,
    sql,
)
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Create and deploy a service from the resources page against an isolated API"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/new-service"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "new-service", "tests/ui/fixtures/deployment-history/scenario.json"
)
workspace = state["workspaces"]["acme/shared"]
org = state["orgs"]["acme"]["id"]
suffix = uuid.uuid4().hex[:6]
platform_domain = "apps.e2e.test"
sql(state, "UPDATE clusters SET is_active=false WHERE provider='fixture';")
clusters = {
    health: evidence.seed_cluster(
        state,
        region,
        name=f"{health}-{suffix}",
        domains=[platform_domain],
        health=health,
        default=health == "healthy",
    )
    for region, health in (
        ("us-east-1", "healthy"),
        ("eu-west-1", "degraded"),
        ("eu-north-1", "unhealthy"),
    )
}
sql(
    state,
    f"UPDATE clusters SET last_heartbeat=NOW()-INTERVAL '1 day' WHERE id='{clusters['unhealthy']}';",
)
base = state["ui_url"] + f"/org/{org}/wks/{workspace}"
production = sql(
    state,
    f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND slug='production';",
)
replica_limit = call(state, "loco.config.v1.ConfigService/GetConfig", {})["sizeLimits"][
    "maxReplicas"
]
drafts_key = f"loco:drafts:v1:{org}:{workspace}:{production}"
DRAFT_URL = re.compile(r"/resources\?env=production&draft=[0-9a-f-]+$")


open_page = partial(new_page, state=state)


def open_new_service(page):
    page.get_by_role("button", name="New resource", exact=True).click()
    page.get_by_role("menuitem", name="New service", exact=True).click()


def stored_drafts(page):
    raw = page.evaluate(f"localStorage.getItem({json.dumps(drafts_key)})")
    return [] if raw is None else [d["name"] for d in json.loads(raw)]


def draft_row(page, name):
    return (
        page.get_by_role("row")
        .filter(has=page.get_by_role("link", name=name, exact=True))
        .filter(has_text="Not deployed yet")
    )


def seeded_draft(name, image):
    return {
        "id": str(uuid.uuid4()),
        "name": name,
        "slug": name,
        "image": image,
        "createdMs": 0,
        "edits": {},
    }


def seed_deployment(body, status, replica_state, detail, problem=None):
    spec = {**body["spec"]["service"], "observability": {"injectOtel": True}}
    [deployment] = evidence.seed_deployments(
        state,
        body["resourceId"],
        [
            {
                "environment": "production",
                "status": status,
                "spec": spec,
                "placements": {
                    region: {"ready_replicas": 0} for region in body["regions"]
                },
            }
        ],
    )
    rows = [
        f"INSERT INTO deployment_activity(deployment_id,seq,revision,at,severity,replica_name,message) VALUES('{deployment}',1,1,NOW(),'info','','Deployment requested.');"
    ]
    placements = json.loads(
        sql(
            state,
            f"SELECT json_agg(json_build_object('id',id,'cluster',cluster_id,'region',region)) FROM placements WHERE deployment_id='{deployment}';",
        )
    )
    for seq, placement in enumerate(placements, 2):
        replica = {
            "uid": f"replica-{deployment}-{placement['region']}",
            "name": "service-new-1",
            "node": "worker-east-1",
            "placement_revision": 1,
            "deployment_id": deployment,
            "ready": False,
            "state": replica_state,
            "detail": detail,
        }
        snapshot = {"placementId": placement["id"], "replicas": [replica]}
        if problem is not None:
            snapshot["problem"] = {
                "reason": problem,
                "detail": detail,
                "replica_name": replica["name"],
            }
        rows += [
            "INSERT INTO deployment_rollouts(deployment_id,cluster_id,revision,snapshot) "
            f"VALUES('{deployment}','{placement['cluster']}',1,{evidence.sql_literal(json.dumps(snapshot))});",
            "INSERT INTO deployment_activity(deployment_id,seq,revision,at,severity,replica_name,message,cluster_id,region) "
            f"VALUES('{deployment}',{seq},1,NOW(),'{'error' if problem else 'info'}','{replica['name']}',"
            f"{evidence.sql_literal(detail)},'{placement['cluster']}','{placement['region']}');",
        ]
    sql(state, "BEGIN;\n" + "\n".join(rows) + "\nCOMMIT;")
    return deployment


with session(out) as browser:
    context, page, errors = open_page(
        browser,
        "member",
        storage={
            drafts_key: json.dumps(
                [seeded_draft("member-draft", "registry.example.com/acme/member:1")]
            )
        },
    )
    page.goto(base + "/new?env=production")
    expect(page).to_have_url(re.compile(r"/resources\?env=production&new=1$"))
    ask = page.get_by_role("dialog", name="New resource")
    expect(
        ask.get_by_text("You can't create resources here", exact=True)
    ).to_be_visible()
    ask.get_by_role("button", name="Close", exact=True).last.click()
    expect(page).to_have_url(re.compile(r"/resources\?env=production$"))
    expect(page.get_by_text("Deploy your first resource", exact=True)).to_be_visible()
    expect(page.get_by_text("member-draft")).to_have_count(0)
    page.get_by_role("link", name="New resource", exact=True).click()
    expect(page).to_have_url(re.compile(r"/resources\?env=production&new=1$"))
    page.get_by_role("dialog", name="New resource").get_by_role(
        "button", name="Close", exact=True
    ).last.click()
    page.goto(base + "?env=production")
    expect(page.get_by_text("No resources yet", exact=True)).to_be_visible()
    expect(page.get_by_text("member-draft")).to_have_count(0)
    assert not errors, errors
    context.close()

    loading = seeded_draft("loading-draft", "registry.example.com/acme/loading:1")
    context, page, errors = open_page(
        browser, "owner", storage={drafts_key: json.dumps([loading])}
    )
    pending_config = []
    context.route(
        "**/loco.config.v1.ConfigService/GetConfig",
        lambda route: pending_config.append(route),
        times=1,
    )
    with page.expect_request("**/loco.config.v1.ConfigService/GetConfig"):
        page.goto(base + f"/resources?env=production&draft={loading['id']}")
    drawer = page.get_by_role("dialog", name="loading-draft", exact=True)
    expect(page.get_by_role("status")).to_be_visible()
    expect(drawer.get_by_role("group", name="Replicas", exact=True)).to_have_count(0)
    expect(
        drawer.get_by_text("Use at most 0 replicas per region", exact=True)
    ).to_have_count(0)
    for route in pending_config:
        route.continue_()
    expect(drawer.get_by_role("group", name="Replicas", exact=True)).to_be_visible()
    assert not errors, errors
    context.close()

    missing = seeded_draft("missing-region", "registry.example.com/acme/missing:1")
    missing["edits"] = {"regions": ["us-east-1", "ap-south-1"]}
    context, page, errors = open_page(
        browser, "owner", storage={drafts_key: json.dumps([missing])}
    )
    page.goto(base + f"/resources?env=production&draft={missing['id']}")
    drawer = page.get_by_role("dialog", name="missing-region", exact=True)
    card = drawer.get_by_role(
        "button", name="ap-south-1 No cluster in this region", exact=True
    )
    expect(card).to_be_enabled()
    expect(card).to_have_attribute("aria-pressed", "true")
    card.click()
    expect(card).to_have_count(0)
    assert not errors, errors
    context.close()

    lone = seeded_draft("lone-region", "registry.example.com/acme/lone:1")
    context, page, errors = open_page(
        browser, "owner", storage={drafts_key: json.dumps([lone])}
    )
    context.route(
        "**/loco.resource.v1.ResourceService/ListRegions",
        lambda route: route.fulfill(
            json={
                "regions": [
                    {
                        "region": "us-east-1",
                        "isDefault": False,
                        "healthStatus": "healthy",
                        "availability": "REGION_AVAILABILITY_AVAILABLE",
                    }
                ]
            }
        ),
    )
    page.goto(base + f"/resources?env=production&draft={lone['id']}")
    drawer = page.get_by_role("dialog", name="lone-region", exact=True)
    region = drawer.get_by_role("button", name=re.compile(r"^us-east-1"))
    expect(region).to_have_attribute("aria-pressed", "true")
    expect(drawer.get_by_text("Choose at least one region")).to_have_count(0)
    assert not errors, errors
    context.close()

    context, page, errors = open_page(browser, "owner")
    pending_scopes = []
    context.route(
        "**/loco.token.v1.TokenService/GetScopes",
        lambda route: pending_scopes.append(route),
        times=1,
    )
    with page.expect_request("**/loco.token.v1.TokenService/GetScopes"):
        page.goto(base + "/resources?env=production&new=1")
    early = page.get_by_role("dialog", name="New resource").get_by_role(
        "textbox", name="Container image"
    )
    early.press_sequentially("ghcr.io/acme/early:1")
    for route in pending_scopes:
        route.continue_()
    expect(
        page.get_by_role("dialog", name="New resource").get_by_role(
            "textbox", name="Name"
        )
    ).to_have_value("early")
    expect(early).to_have_value("ghcr.io/acme/early:1")
    assert not errors, errors
    context.close()

    repository = f"shop-{suffix}"
    image = f"registry.example.com/acme/{repository}:1.4.2"
    name = repository
    context, page, errors = open_page(browser, "owner")
    deployments = []
    rejected = []
    reject_next = []
    missing = "registry.example.com/acme/doesnotexist:1"
    pinned_image = f"registry.example.com/acme/{repository}@sha256:" + "a" * 64

    def resolve_image(route):
        if route.request.post_data_json["image"] == missing:
            route.fulfill(
                status=400,
                json={
                    "code": "invalid_argument",
                    "message": f'Image "{missing}" was not found. Check the repository and tag or digest.',
                },
            )
            return
        if reject_next:
            reject_next.pop()
            rejected.append(route.request.post_data_json)
            route.continue_(
                post_data=json.dumps(
                    {**route.request.post_data_json, "image": "UPPER/Case::bad"}
                )
            )
            return
        route.fulfill(json={"image": pinned_image})

    context.route("**/loco.deployment.v1.DeploymentService/ResolveImage", resolve_image)
    order = []
    creations = []

    def record_resource_create(route):
        creations.append(route.request.post_data_json)
        route.continue_()

    context.route(
        "**/loco.resource.v1.ResourceService/CreateResource", record_resource_create
    )

    def create_deployment(route):
        order.append(("CreateDeployment", route.request.post_data_json["resourceId"]))
        deployments.append(route.request.post_data_json)
        route.fulfill(
            json={
                "deploymentId": seed_deployment(
                    route.request.post_data_json,
                    "deploying",
                    "Starting",
                    "Starting application; waiting for readiness.",
                )
            }
        )

    context.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment",
        create_deployment,
    )
    page.goto(base + "/resources?env=production")
    open_new_service(page)
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
    last_region = drawer.get_by_role("button", name="us-east-1", exact=True)
    expect(last_region).to_have_attribute("aria-pressed", "true")
    # One read measures the card and its contents in the same frame while the drawer slides in.
    offsets = last_region.evaluate(
        """card => {
            const middle = (el) => {
                const box = el.getBoundingClientRect();
                return box.top + box.height / 2;
            };
            const text = [...card.querySelectorAll("*")].find(
                (el) => el.childElementCount === 0 && el.textContent === "us-east-1"
            );
            return {
                text: middle(text) - middle(card),
                icon: middle(card.querySelector("svg")) - middle(card),
            };
        }"""
    )
    for content, offset in offsets.items():
        assert abs(offset) <= 1, f"the {content} is {offset}px from the card center"
    offline = drawer.get_by_role("button", name="eu-north-1 Offline", exact=True)
    expect(offline).to_be_disabled()
    expect(
        drawer.get_by_role("button", name="eu-west-1 degraded", exact=True)
    ).to_be_enabled()
    expect(last_region).to_have_attribute("aria-disabled", "true")
    expect(last_region).to_have_attribute(
        "title", "A resource needs at least one region"
    )
    last_region.focus()
    expect(last_region).to_be_focused()
    page.keyboard.press("Shift+Tab")
    page.keyboard.press("Tab")
    expect(last_region).to_be_focused()
    page.keyboard.press("Space")
    expect(last_region).to_have_attribute("aria-pressed", "true")
    drawer.get_by_role(
        "button", name=f"Platform domain: {platform_domain}", exact=True
    ).click()
    domain_option = page.get_by_role(
        "menuitemradio", name=re.compile(rf"^{re.escape(platform_domain)}")
    )
    expect(domain_option.get_by_text("us-east-1", exact=True)).to_be_visible()
    expect(domain_option.get_by_text("healthy", exact=True)).to_have_count(0)
    expect(domain_option.get_by_text("eu-west-1", exact=True)).to_have_count(0)
    page.keyboard.press("Escape")
    expect(drawer.get_by_role("button", name="Public", exact=True)).to_have_attribute(
        "aria-pressed", "true"
    )
    subdomain = drawer.get_by_role("textbox", name="Subdomain")
    expect(subdomain).to_have_value(name)
    expect(
        drawer.get_by_text(f"{name}.{platform_domain} is available", exact=True)
    ).to_be_visible()
    subdomain.fill("x" * 64)
    expect(drawer.get_by_text("Use at most 63 characters", exact=True)).to_be_visible()
    subdomain.fill(name)
    expect(
        drawer.get_by_text(f"{name}.{platform_domain} is available", exact=True)
    ).to_be_visible()
    # Nothing defaults a port, and a public service needs one.
    port = drawer.get_by_role("textbox", name="Port", exact=True)
    expect(port).to_have_value("")
    port.fill("8080")
    expect(
        drawer.get_by_role("button", name="Decrease replicas", exact=True)
    ).to_be_disabled()
    drawer.get_by_role("button", name="Increase replicas", exact=True).click()
    replicas = drawer.get_by_role("group", name="Replicas", exact=True)
    expect(replicas.get_by_text("2", exact=True)).to_be_visible()
    expect(drawer.get_by_text("Runs exactly 2 per region.", exact=True)).to_have_count(
        0
    )
    cpu = drawer.get_by_role("slider", name="CPU", exact=True)
    memory = drawer.get_by_role("slider", name="Memory", exact=True)
    expect(cpu).to_have_attribute("max", "4")
    expect(memory).to_have_attribute("max", "5")
    cpu.focus()
    page.keyboard.press("Home")
    page.keyboard.press("ArrowRight")
    page.keyboard.press("ArrowRight")
    expect(cpu).to_have_attribute("aria-valuetext", "0.5 vCPU")
    # Clicking the track moves the thumb to the nearest step.
    rail = drawer.locator(".dsl-rail").nth(1)
    box = rail.bounding_box()
    rail.click(position={"x": box["width"] - 3, "y": box["height"] / 2})
    expect(memory).to_have_attribute("aria-valuetext", "4 Gi")
    memory.focus()
    page.keyboard.press("Home")
    for _ in range(3):
        page.keyboard.press("ArrowRight")
    expect(memory).to_have_attribute("aria-valuetext", "1 Gi")

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
    paste.fill(
        '# billing\n\nexport STRIPE_API_KEY="sk_test"\nLOG_LEVEL=debug\nnot a variable\nLOG_LEVEL=info'
    )
    drawer.get_by_role("button", name="Add 2", exact=True).click()
    expect(paste).to_have_count(0)
    variables = drawer.get_by_role("list", name="Variables")
    names = variables.get_by_role("textbox", name="Variable name")
    expect(names.nth(0)).to_have_value("STRIPE_API_KEY")
    expect(names.nth(1)).to_have_value("LOG_LEVEL")
    expect(variables.get_by_role("textbox", name="Value of LOG_LEVEL")).to_have_value(
        "info"
    )
    secret_value = variables.get_by_label("Value of STRIPE_API_KEY", exact=True)
    expect(secret_value).to_have_value("sk_test")
    secret_value.fill("${{ secrets.STRIPE_API_KEY }}")
    # A storage update from another tab reaches the drawer.
    page.evaluate(
        """key => {
                const drafts = JSON.parse(localStorage.getItem(key));
                const row = drafts[0].edits.vars.find(row => row.key === "LOG_LEVEL");
                row.value = "debug";
                localStorage.setItem(key, JSON.stringify(drafts));
                dispatchEvent(new StorageEvent("storage", {key}));
            }""",
        drafts_key,
    )
    log_level = variables.get_by_label("Value of LOG_LEVEL", exact=True)
    expect(log_level).to_have_value("debug")
    expect(secret_value).to_have_value("${{ secrets.STRIPE_API_KEY }}")
    log_level.fill("info")
    # A draft that cannot be saved reports it once, not on every keystroke.
    save_failed = page.get_by_text("Couldn't save changes to this draft", exact=True)
    for _ in range(2):
        page.evaluate(
            """key => {
                    window.realSetItem = Storage.prototype.setItem;
                    Storage.prototype.setItem = function (name, value) {
                        if (name === key) throw new DOMException("full", "QuotaExceededError");
                        return window.realSetItem.call(this, name, value);
                    };
                }""",
            drafts_key,
        )
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
    expect(
        drawer.get_by_text(
            'STRIPE_API_KEY: "${{ secrets.STRIPE_API_KEY }}"', exact=False
        )
    ).to_be_visible()
    drawer.get_by_text("loco.yaml", exact=True).first.click()
    secret_value.scroll_into_view_if_needed()
    drawer.get_by_role("button", name="Add", exact=True).click()
    expect(names.nth(2)).to_be_focused()
    names.nth(2).fill("1EXTRA")
    drawer.get_by_role("button", name="Add", exact=True).click()
    variables.get_by_role("textbox", name="Value of variable").fill("orphan")
    # Every invalid row is marked at once.
    expect(variables.get_by_role("listitem").nth(2)).to_contain_text(
        "Use letters, digits and _, not starting with a digit"
    )
    expect(variables.get_by_role("listitem").nth(3)).to_contain_text(
        "Enter a name for this value"
    )
    expect(variables.locator("[data-invalid]")).to_have_count(2)
    drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
    expect(drawer.get_by_role("alert")).to_have_text("Fix the 2 highlighted variables")
    variables.get_by_role("button", name="Remove 1EXTRA", exact=True).click()
    variables.get_by_role("button", name="Remove variable", exact=True).click()
    expect(variables.get_by_role("listitem")).to_have_count(2)
    expect(variables.locator("[data-invalid]")).to_have_count(0)
    # A variable may be empty.
    drawer.get_by_role("button", name="Add", exact=True).click()
    names.nth(2).fill("EMPTY_FLAG")
    expect(variables.locator("[data-invalid]")).to_have_count(0)
    expect(
        drawer.get_by_role("heading", name="Environment variables 3")
    ).to_be_visible()

    health = drawer.get_by_role("button", name=re.compile(r"^Health check"))
    expect(health).to_have_attribute("aria-expanded", "false")
    health.click()
    expect(health).to_have_attribute("aria-expanded", "true")
    interval = drawer.get_by_role("textbox", name="Interval, sec")
    health_path = drawer.get_by_role("textbox", name="Path", exact=True).input_value()
    assert health_path.startswith("/"), health_path
    interval.fill("1x5")
    expect(interval).to_have_value("15")
    timeout = drawer.get_by_role("textbox", name="Timeout, sec")
    timeout.fill("15")
    expect(
        drawer.get_by_text("Must be below the interval", exact=True).first
    ).to_be_visible()
    expect(timeout).to_have_attribute("aria-invalid", "true")
    timeout.fill("4")
    expect(drawer.get_by_text("Must be below the interval", exact=True)).to_have_count(
        0
    )
    drawer.get_by_role("textbox", name="Fail after, checks").fill("6")
    expect(health).to_contain_text(f"{health_path} · every 15s")
    start_command = drawer.get_by_role("button", name=re.compile(r"^Start command"))
    expect(start_command).to_contain_text("Image default")
    start_command.click()
    drawer.get_by_role("textbox", name="Arguments", exact=True).fill(
        "-text=hello world\n\n-listen=:8080\n"
    )
    expect(start_command).to_contain_text("-text=hello world -listen=:8080")
    drawer.get_by_text("loco.yaml", exact=True).first.click()
    expect(
        drawer.get_by_text('args: ["-text=hello world", "-listen=:8080"]', exact=False)
    ).to_be_visible()
    drawer.get_by_text("loco.yaml", exact=True).first.click()
    expect(drawer.get_by_text("To production", exact=True)).to_be_visible()
    assert drawer.bounding_box()["width"] <= page.viewport_size["width"]

    page.reload()
    expect(page).to_have_url(DRAFT_URL)
    expect(secret_value).to_have_value("${{ secrets.STRIPE_API_KEY }}")
    expect(replicas.get_by_text("2", exact=True)).to_be_visible()
    expect(memory).to_have_attribute("aria-valuetext", "1 Gi")
    expect(names.nth(0)).to_have_value("STRIPE_API_KEY")
    expect(variables.get_by_role("textbox", name="Value of LOG_LEVEL")).to_have_value(
        "info"
    )
    expect(health).to_contain_text(f"{health_path} · every 15s")
    expect(start_command).to_contain_text("-text=hello world -listen=:8080")
    page.keyboard.press("Escape")
    expect(page).to_have_url(re.compile(r"/resources\?env=production$"))
    row = draft_row(page, name)
    expect(row.get_by_text("Not deployed yet", exact=True)).to_be_visible()
    expect(row.get_by_text("Draft", exact=True)).to_be_visible()
    expect(row.get_by_role("checkbox")).to_have_count(0)
    expect(row.get_by_role("button")).to_have_count(0)
    listed = len(
        call(
            state,
            "loco.resource.v1.ResourceService/ListWorkspaceResources",
            {"workspaceId": workspace},
        ).get("resources", [])
    )
    all_filter = page.get_by_role("group", name="Filter by status", exact=True)
    expect(
        all_filter.get_by_role("button", name="All", exact=False).first.locator(
            '[data-slot="segmented-count"]'
        )
    ).to_have_text(str(listed))
    all_filter.get_by_role("button", name=re.compile(r"^Draft\b")).click()
    expect(row).to_be_visible()
    page.goto(base + "?env=production")
    dashboard_row = draft_row(page, name)
    dashboard_filter = page.get_by_role(
        "group", name="Filter resources by status", exact=True
    )
    dashboard_filter.get_by_role("button", name=re.compile(r"^Draft\b")).click()
    expect(dashboard_row).to_be_visible()
    expect(dashboard_row.get_by_text("Not deployed yet", exact=True)).to_be_visible()
    expect(dashboard_row.get_by_text("Draft", exact=True)).to_be_visible()
    page.get_by_role("button", name="Region: All regions", exact=True).click()
    expect(
        page.get_by_role("menuitemradio", name=re.compile(r"^us-east-1\b"))
    ).to_be_visible()
    expect(
        page.get_by_role("menuitemradio", name=re.compile(r"^eu-north-1\b"))
    ).to_have_count(0)
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
    expect(drawer.get_by_role("textbox", name="Timeout, sec")).to_have_value("5")
    drawer.get_by_role("textbox", name="Timeout, sec").fill("4")
    interval.fill("0")
    drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
    expect(
        drawer.get_by_text("Must be greater than or equal to 1", exact=True)
    ).to_be_visible()
    expect(drawer.get_by_role("alert")).to_have_text(
        "Health check interval: Must be greater than or equal to 1"
    )
    assert not rejected, rejected
    assert sql(state, f"SELECT count(*) FROM resources WHERE slug='{name}';") == "0"
    interval.fill("15")
    expect(drawer.get_by_role("alert")).to_have_count(0)
    expect(drawer.get_by_text("Checking the image…", exact=True)).to_have_count(0)
    creations_before = len(creations)
    reject_next.append(True)
    with expect_rpc(
        page, "ResolveImage", lambda body: body.get("workspaceId") == workspace
    ) as resolution:
        drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
    details = resolution.value.json()["details"]
    assert [detail["type"] for detail in details] == [
        "google.rpc.BadRequest",
        "google.rpc.ErrorInfo",
    ], details
    resolve_error = drawer.get_by_text(
        "Invalid image reference. Use a repository with a tag or digest.", exact=True
    )
    expect(resolve_error).to_be_visible()
    expect(drawer.get_by_role("alert")).to_have_count(0)
    # The image is editable in the drawer; a bad reference is named at the field.
    image_field = drawer.get_by_role("textbox", name="Container image", exact=True)
    image_field.fill("UPPER/Case")
    expect(resolve_error).to_have_count(0)
    expect(
        drawer.get_by_text("Use lowercase letters in the image reference", exact=True)
    ).to_have_count(1)
    drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
    expect(drawer.get_by_role("alert")).to_have_count(0)
    expect(
        drawer.get_by_text("Use lowercase letters in the image reference", exact=True)
    ).to_have_count(1)
    # An image the registry does not have is flagged before Deploy.
    image_field.fill(missing)
    expect(
        drawer.get_by_text(
            f'Image "{missing}" was not found. Check the repository and tag or digest.',
            exact=True,
        )
    ).to_have_count(1)
    expect(drawer.get_by_role("alert")).to_have_count(0)
    assert not order, order
    image_field.fill(image)
    expect(drawer.get_by_role("alert")).to_have_count(0)
    assert (
        json.loads(page.evaluate(f"localStorage.getItem({json.dumps(drafts_key)})"))[0][
            "image"
        ]
        == image
    )
    assert len(creations) == creations_before, creations
    assert sql(state, f"SELECT count(*) FROM resources WHERE slug='{name}';") == "0"
    assert stored_drafts(page) == [name], stored_drafts(page)
    assert not order, order

    # Resource creation errors leave the draft ready to retry.
    def resource_error(route):
        return route.fulfill(
            status=503,
            json={
                "code": "unavailable",
                "message": "Resource creation unavailable",
            },
        )

    page.route("**/loco.resource.v1.ResourceService/CreateResource", resource_error)
    drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
    expect(drawer.get_by_role("alert")).to_have_text("Resource creation unavailable.")
    expect(
        drawer.get_by_role("button", name=f"Deploy {name}", exact=True)
    ).to_be_enabled()
    expect(drawer.get_by_role("link", name="Open resource", exact=True)).to_have_count(
        0
    )
    page.unroute("**/loco.resource.v1.ResourceService/CreateResource", resource_error)

    # Failed deployment creation cleans up the resource and permits retry.
    def deployment_error(route):
        return route.fulfill(
            status=503,
            json={
                "code": "unavailable",
                "message": "Deployment creation unavailable",
            },
        )

    page.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment",
        deployment_error,
    )
    drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
    expect(drawer.get_by_role("alert")).to_have_text(
        f"Couldn't deploy {image}: Deployment creation unavailable."
    )
    expect(
        drawer.get_by_role("button", name=f"Deploy {name}", exact=True)
    ).to_be_enabled()
    expect(drawer.get_by_role("link", name="Open resource", exact=True)).to_have_count(
        0
    )
    assert sql(state, f"SELECT count(*) FROM resources WHERE slug='{name}';") == "0"
    assert stored_drafts(page) == [name], stored_drafts(page)
    page.unroute(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment",
        deployment_error,
    )

    # A retained resource keeps its error and cannot be deployed twice.
    def unavailable(route):
        return route.fulfill(status=503, json={"code": "unavailable"})

    page.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment",
        deployment_error,
    )
    page.route("**/loco.resource.v1.ResourceService/DeleteResource", unavailable)
    drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
    expect(
        drawer.get_by_text("Deployment creation unavailable.", exact=True)
    ).to_be_visible()
    drawer.get_by_role("textbox", name="Description").fill("Retained resource")
    expect(drawer.get_by_role("alert")).to_have_text("Deployment creation unavailable.")
    expect(
        drawer.get_by_role("button", name=f"Deploy {name}", exact=True)
    ).to_be_disabled()
    retained = drawer.get_by_role("link", name="Open resource", exact=True)
    expect(retained).to_have_attribute(
        "href", re.compile(r"/resource/[0-9a-f-]+\?env=production$")
    )
    expect(page).to_have_url(DRAFT_URL)
    assert not deployments, deployments
    assert stored_drafts(page) == [name], stored_drafts(page)
    page.unroute(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment",
        deployment_error,
    )
    page.unroute("**/loco.resource.v1.ResourceService/DeleteResource")
    # The drawer learns of the retained resource before it is deleted elsewhere.
    taken = f"{name}.{platform_domain} is taken"
    expect(drawer.get_by_text(taken, exact=True).first).to_be_visible()
    expect(drawer.get_by_role("alert")).to_have_text("Deployment creation unavailable.")
    orphan = sql(state, f"SELECT id FROM resources WHERE slug='{name}';")
    call(
        state, "loco.resource.v1.ResourceService/DeleteResource", {"resourceId": orphan}
    )
    drawer.get_by_role("button", name="Close", exact=True).click()
    expect(page).to_have_url(re.compile(r"/resources\?env=production$"))
    draft_row(page, name).get_by_role("link", name=name, exact=True).click()
    expect(page).to_have_url(DRAFT_URL)
    expect(
        drawer.get_by_text(f"{name}.{platform_domain} is available", exact=True)
    ).to_be_visible()
    expect(drawer.get_by_text(taken, exact=True)).to_have_count(0)
    expect(drawer.get_by_text("already uses this name")).to_have_count(0)
    drawer.get_by_role("button", name="Close", exact=True).click()
    expect(page).to_have_url(re.compile(r"/resources\?env=production$"))
    expect(
        page.get_by_role("row").filter(
            has=page.get_by_role("link", name=name, exact=True)
        )
    ).to_have_count(1)
    draft_row(page, name).get_by_role("link", name=name, exact=True).click()
    expect(page).to_have_url(DRAFT_URL)
    drawer.get_by_role("textbox", name="Description").fill("Takes checkout payments")
    pending = []
    page.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment",
        lambda route: pending.append(route),
    )
    deploy_button = drawer.get_by_role("button", name=f"Deploy {name}", exact=True)
    with expect_rpc(page, "CreateDeployment", lambda body: "spec" in body):
        deploy_button.click()
        expect(deploy_button).to_be_disabled()
        expect(deploy_button.locator("[data-slot=spinner]")).to_be_visible()
        expect(page).to_have_url(DRAFT_URL)
        while not pending:
            page.wait_for_timeout(50)
        pending[0].fallback()

    expect(page).to_have_url(re.compile(r"/resource/[0-9a-f-]+\?env=production$"))
    expect(drawer).to_have_count(0)
    panel = page.get_by_role("region", name="Deploying", exact=True)
    new_version = panel.get_by_role("region", name="New version", exact=True)
    expect(new_version.get_by_text("service-new-1", exact=True)).to_be_visible()
    expect(new_version.get_by_text("worker-east-1", exact=True)).to_be_visible()
    expect(new_version.get_by_text("Starting", exact=True)).to_be_visible()
    expect(
        panel.get_by_role("region", name="Previous version", exact=True)
    ).to_have_count(0)
    activity = panel.get_by_role("region", name="Activity feed", exact=True)
    expect(activity.get_by_role("listitem")).to_have_count(2)
    expect(activity.get_by_role("listitem").last).to_contain_text(
        "Starting application; waiting for readiness."
    )
    expect(activity.get_by_role("listitem").last).to_contain_text("service-new-1")
    page.wait_for_function(
        f"() => localStorage.getItem({json.dumps(drafts_key)}) === null"
    )
    resource = urlparse(page.url).path.rsplit("/", 1)[-1]
    assert sql(state, f"SELECT name FROM resources WHERE id='{resource}';") == name
    assert (
        sql(state, f"SELECT description FROM resources WHERE id='{resource}';")
        == "Takes checkout payments"
    )
    assert [(d["resourceId"], d["regions"]) for d in deployments] == [
        (resource, ["us-east-1"])
    ], deployments
    spec = deployments[0]["spec"]["service"]
    assert spec["build"]["image"] == pinned_image, spec
    assert spec["port"] == 8080, spec
    sizing = spec["regions"]["us-east-1"]
    assert sizing["minReplicas"] == 2 and sizing["maxReplicas"] == 2, spec
    assert sizing["scalers"] == {}, spec
    assert sizing["cpu"] == "500m" and sizing["memory"] == "1Gi", spec
    assert spec["env"] == {
        "STRIPE_API_KEY": "${{ secrets.STRIPE_API_KEY }}",
        "LOG_LEVEL": "info",
        "EMPTY_FLAG": "",
    }, spec
    assert spec["args"] == ["-text=hello world", "-listen=:8080"], spec
    assert "command" not in spec, spec
    check = spec["healthCheck"]
    assert (
        check["path"],
        check["intervalSeconds"],
        check["timeoutSeconds"],
        check["failureThreshold"],
    ) == (health_path, 15, 4, 6), check
    domain = sql(
        state,
        f"SELECT subdomain_label || '.' || platform_domain FROM resource_domains WHERE resource_id='{resource}';",
    )
    assert domain == f"{name}.{platform_domain}", domain
    assert not errors, errors
    context.close()

    # A second service at narrow width: a draft whose name is taken, autoscaling, and a failed rollout.
    name = f"{repository}-2"
    context, page, errors = open_page(browser, "owner", narrow=True)
    deployments = []
    context.route(
        "**/loco.deployment.v1.DeploymentService/ResolveImage",
        lambda route: route.fulfill(json={"image": pinned_image}),
    )

    def create_failing_deployment(route):
        body = route.request.post_data_json
        deployments.append(body)
        deployment = seed_deployment(
            body,
            "failed",
            "Failing",
            "ImagePullBackOff: manifest unknown",
            "image_pull_failed",
        )
        route.fulfill(json={"deploymentId": deployment})

    context.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment",
        create_failing_deployment,
    )
    page.goto(base + "/resources?env=production")
    stale = seeded_draft(repository, f"registry.example.com/acme/{repository}:0.9")
    page.evaluate(
        f"localStorage.setItem({json.dumps(drafts_key)},{json.dumps(json.dumps([stale]))})"
    )
    page.reload()
    row = draft_row(page, repository)
    expect(row.get_by_text("Not deployed yet", exact=True)).to_be_visible()
    row.get_by_role("link", name=repository, exact=True).click()
    stale_drawer = page.get_by_role("dialog", name=repository, exact=True)
    expect(stale_drawer.get_by_role("note")).to_have_text(
        f"{repository} already uses this name or slug in this workspace. Deploying this draft will fail; discard it and start again with another name."
    )
    stale_drawer.get_by_role("button", name="Discard draft", exact=True).click()
    confirm = page.get_by_role("alertdialog", name=f"Discard {repository}?")
    confirm.get_by_role("button", name="Discard draft", exact=True).click()
    expect(page.get_by_text(f"Discarded {repository}", exact=True)).to_be_visible()
    expect(page).to_have_url(re.compile(r"/resources\?env=production$"))
    expect(draft_row(page, repository)).to_have_count(0)
    assert stored_drafts(page) == [], stored_drafts(page)

    open_new_service(page)
    ask = page.get_by_role("dialog", name="New resource")
    ask.get_by_role("textbox", name="Container image").fill(image)
    name_input = ask.get_by_role("textbox", name="Name")
    slug_input = ask.get_by_role("textbox", name="Slug")
    expect(name_input).to_have_value(name)
    expect(slug_input).to_have_value(name)
    name_input.fill(repository)
    expect(slug_input).to_have_value(repository)
    expect(
        ask.get_by_text(f"{repository} already exists in this workspace", exact=True)
    ).to_have_count(2)
    name_input.fill(repository.upper())
    expect(
        ask.get_by_text(
            f"{repository.upper()} already exists in this workspace", exact=True
        )
    ).to_be_visible()
    rule = "Use 1 to 63 lowercase letters, digits and hyphens, starting with a letter and ending with a letter or digit"
    for typed, derived in [
        ("QA Echo", "qa-echo"),
        ("🚀 Rocket Launch", "rocket-launch"),
        (" Café Orders ", "cafe-orders"),
        ("Zürich", "zurich"),
    ]:
        name_input.fill(typed)
        expect(slug_input).to_have_value(derived)
        expect(ask.get_by_text(rule, exact=True)).to_have_count(0)
    name_input.fill("a" * 64)
    expect(ask.get_by_text("Use at most 63 characters", exact=True)).to_be_visible()
    name_input.fill("   ")
    expect(ask.get_by_text("Enter a name", exact=True)).to_be_visible()
    expect(ask.get_by_text("Enter a slug", exact=True)).to_be_visible()
    label = f"Shop {suffix} Two"
    name_input.fill(label)
    for invalid in ["QA Echo", "-bad-", "🚀rocket", "a" * 64]:
        slug_input.fill(invalid)
        expect(ask.get_by_text(rule, exact=True)).to_be_visible()
    slug_input.fill("7-cart")
    expect(ask.get_by_text("Start the slug with a letter", exact=True)).to_be_visible()
    slug_input.fill("localhost")
    expect(ask.get_by_text("localhost is reserved", exact=True)).to_be_visible()
    slug_input.fill(name)
    name_input.fill(f"{label} ")
    expect(slug_input).to_have_value(name)
    ask.get_by_role("button", name="Continue", exact=True).click()

    expect(page).to_have_url(DRAFT_URL)
    drawer = page.get_by_role("dialog", name=label, exact=True)
    assert drawer.bounding_box()["width"] <= page.viewport_size["width"]
    drawer.get_by_role("textbox", name="Port", exact=True).fill("8080")
    drawer.get_by_role("button", name="Increase replicas", exact=True).click()
    drawer.get_by_role("switch", name="Autoscaling", exact=True).click()
    increase_max = drawer.get_by_role(
        "button", name="Increase maximum replicas", exact=True
    )
    expect(
        drawer.get_by_text("Scales per region based on CPU utilization.", exact=True)
    ).to_be_visible()
    expect(
        drawer.get_by_role("slider", name="CPU target (%)", exact=True)
    ).to_have_value("70")
    for _ in range(replica_limit - 3):
        increase_max.click()
    expect(increase_max).to_be_disabled()
    for _ in range(replica_limit - 3):
        drawer.get_by_role(
            "button", name="Decrease maximum replicas", exact=True
        ).click()
    with expect_rpc(page, "CreateDeployment", lambda body: "spec" in body):
        drawer.get_by_role("button", name=f"Deploy {label}", exact=True).click()

    expect(page).to_have_url(re.compile(r"/resource/[0-9a-f-]+\?env=production$"))
    expect(drawer).to_have_count(0)
    resource = urlparse(page.url).path.rsplit("/", 1)[-1]
    stored = sql(
        state, f"SELECT name || '|' || slug FROM resources WHERE id='{resource}';"
    )
    assert stored == f"{label}|{name}", stored
    expect(page.get_by_role("heading", level=1)).to_have_text(label)
    expect(page.get_by_label(f"Slug {name}", exact=True)).to_be_visible()
    panel = page.get_by_role("region", name="Failed", exact=True)
    problem = panel.get_by_role("status").filter(
        has_text="ImagePullBackOff: manifest unknown"
    )
    expect(
        problem.get_by_text("ImagePullBackOff: manifest unknown", exact=True)
    ).to_be_visible()
    expect(problem.get_by_text("service-new-1", exact=True)).to_be_visible()
    expect(
        panel.get_by_role("region", name="New version", exact=True).get_by_text(
            "Failing", exact=True
        )
    ).to_be_visible()
    expect(panel.get_by_role("link", name="View logs", exact=True)).to_have_count(1)
    sizing = deployments[0]["spec"]["service"]["regions"]["us-east-1"]
    assert sizing["minReplicas"] == 2 and sizing["maxReplicas"] == 3, sizing
    assert sizing["scalers"] == {"enabled": True, "cpuTarget": 70}, sizing
    assert not errors, errors
    context.close()
    # A service in two regions is one deployment that rolls out to both.
    name = f"{repository}-multi"
    draft = seeded_draft(name, image)
    draft["edits"].update(
        {
            "regions": ["us-east-1", "eu-west-1"],
            "isPublic": False,
            "port": "8080",
        }
    )
    context, page, errors = open_page(
        browser, "owner", storage={drafts_key: json.dumps([draft])}
    )
    regional_requests = []
    context.route(
        "**/loco.deployment.v1.DeploymentService/ResolveImage",
        lambda route: route.fulfill(json={"image": pinned_image}),
    )

    def regional_deployment(route):
        body = route.request.post_data_json
        regional_requests.append(body)
        route.fulfill(
            json={
                "deploymentId": seed_deployment(
                    body,
                    "deploying",
                    "Starting",
                    "Starting application; waiting for readiness.",
                )
            }
        )

    context.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment",
        regional_deployment,
    )
    page.goto(base + "/resources?env=production")
    draft_row(page, name).get_by_role("link", name=name, exact=True).click()
    drawer = page.get_by_role("dialog", name=name, exact=True)
    drawer.get_by_role("button", name=f"Deploy {name}", exact=True).click()
    expect(page).to_have_url(re.compile(r"/resource/[0-9a-f-]+\?env=production$"))
    expect(drawer).to_have_count(0)
    panel = page.get_by_role("region", name="Deploying", exact=True)
    expect(panel).to_have_count(1)
    expect(panel).to_contain_text("Rolling out to 2 regions")
    chips = panel.get_by_role("group", name="Rollout regions", exact=True)
    expect(chips.get_by_role("button", name="us-east-1")).to_be_visible()
    expect(chips.get_by_role("button", name="eu-west-1")).to_be_visible()
    assert len(regional_requests) == 1, regional_requests
    assert sorted(regional_requests[0]["regions"]) == ["eu-west-1", "us-east-1"]
    assert sorted(regional_requests[0]["spec"]["service"]["regions"]) == [
        "eu-west-1",
        "us-east-1",
    ]
    assert regional_requests[0]["environmentId"] == production
    resource = urlparse(page.url).path.rsplit("/", 1)[-1]
    assert (
        sql(state, f"SELECT count(*) FROM deployments WHERE resource_id='{resource}';")
        == "1"
    )
    assert not errors, errors
    context.close()
print(
    "PASS: new resource drafts for the read-only member and owner creation, rollout, and multi-region rollout"
)
