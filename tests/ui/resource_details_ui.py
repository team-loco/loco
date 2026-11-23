import argparse
import datetime
import json
import re
import uuid
from pathlib import Path

from harness import (
    call,
    evidence,
    load_state,
    open_page,
    repo,
    session,
    sql,
)
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check resource details against an isolated production dashboard"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/resource-details"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "resource-details", "tests/ui/fixtures/deployment-history/scenario.json"
)
workspace = state["workspaces"]["acme/shared"]

org = state["orgs"]["acme"]["id"]
production = next(
    env["id"]
    for env in call(
        state,
        "loco.environment.v1.EnvironmentService/ListEnvironments",
        {"workspaceId": workspace},
    )["environments"]
    if env["slug"] == "production"
)
name = "details-" + uuid.uuid4().hex[:8]
resource = evidence.seed_resources(
    state,
    [
        {
            "workspace": "acme/shared",
            "name": name,
            "service": {
                "routing": {"port": 8080},
                "regions": {
                    "us-ca-1": {
                        "enabled": True,
                        "cpu": "500m",
                        "memory": "512Mi",
                        "minReplicas": 1,
                        "maxReplicas": 1,
                    }
                },
            },
        }
    ],
)[f"acme/shared/{name}"]
deployment = str(uuid.uuid4())
image = f"registry.example.com/recommendation-{resource}:{deployment}"
runtime = {
    "platform": "linux/amd64",
    "observability": {"injectOtel": True},
    "build": {"type": "image", "image": image},
    "port": 8080,
    "env": {"MODE": "production", "API_BASE_URL": "https://api.example.test"},
    "regions": {
        "us-ca-1": {
            "cpu": "500m",
            "memory": "512Mi",
            "minReplicas": 1,
            "maxReplicas": 1,
        }
    },
    "healthCheck": {
        "path": "/health",
        "initialDelaySeconds": 5,
        "intervalSeconds": 10,
        "timeoutSeconds": 2,
        "failureThreshold": 3,
    },
}
evidence.seed_deployments(
    state,
    resource,
    [
        {
            "id": deployment,
            "environment": "production",
            "spec": runtime,
            "age": "10 minutes 30 seconds",
            "placements": {"us-ca-1": {"ready_replicas": 0}},
        }
    ],
)
sql(
    state,
    f"""BEGIN;
UPDATE resources SET partial='recommendation', description='Ranks products for each shopper' WHERE id='{resource}';
INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,actor_type,data)
VALUES('resource.degraded','{org}','{workspace}','{resource}','{production}','agent',jsonb_build_object('reason','restarting','message','The service exited with code 1.','region','us-ca-1','placementId',(SELECT id::text FROM placements WHERE resource_id='{resource}')));
INSERT INTO deployment_rollouts(deployment_id,cluster_id,revision,snapshot) VALUES('{deployment}',(SELECT cluster_id FROM deployment_placements WHERE deployment_id='{deployment}'),1,'{{"problem":{{"reason":"restarting","detail":"The pod restarted"}}}}');
COMMIT;""",
)
sql(
    state,
    f"UPDATE deployments SET created_at = NOW() - INTERVAL '30 minutes 30 seconds', started_at = NOW() - INTERVAL '10 minutes 30 seconds', updated_at = NOW() - INTERVAL '10 minutes' WHERE id='{deployment}';",
)
assert (
    call(
        state,
        "loco.resource.v1.ResourceService/GetResourceEnv",
        {"resourceId": resource, "environmentId": production},
    )["env"]["MODE"]
    == "production"
)
base = state["ui_url"] + f"/org/{org}/wks/{workspace}/resource/{resource}"
USAGE = 0.9
NOW = datetime.datetime.now(datetime.UTC).replace(microsecond=0)
STAMP = NOW.isoformat()


def metrics(route):
    body = route.request.post_data_json
    if body.get("byPod"):
        route.fulfill(json={"series": []})
        return
    value = USAGE if "cpu" in body["metricName"] else 0.25
    route.fulfill(
        json={
            "series": [
                {
                    "resourceId": resource,
                    "points": [{"timestamp": STAMP, "value": value}],
                }
            ]
        }
    )


results = []
with session(out) as browser:
    context, page, errors = open_page(
        browser,
        state=state,
        now=NOW,
    )
    context.route("**/loco.observability.v1.ObservabilityService/QueryMetrics", metrics)
    page.goto(base)
    incident = page.locator('section[aria-labelledby="incident-title"]')
    expect(
        incident.get_by_role("link", name="View replica logs", exact=True)
    ).to_be_visible()
    expect(
        incident.get_by_label("Last log lines in us-ca-1", exact=True)
    ).to_be_visible()
    expect(page.get_by_text("limit 0.5 vCPU", exact=True)).to_be_visible()
    expect(page.get_by_text("0.45 vCPU", exact=True)).to_be_visible()
    title = page.get_by_role("heading", name=name, exact=True)
    events = page.get_by_role("button", name=re.compile(r"^Events"))
    for viewport, below in (
        (evidence.NARROW, True),
        ({"width": 430, "height": 932}, True),
        (evidence.DESKTOP, False),
    ):
        page.set_viewport_size(viewport)
        expect(events).to_be_visible()
        heading = title.bounding_box()
        assert (
            events.bounding_box()["y"] >= heading["y"] + heading["height"]
        ) == below, viewport
    chart = page.locator('[aria-label="CPU: average"]')
    ticks = chart.locator(".recharts-xAxis-tick-labels text")
    expect(ticks.nth(2)).to_be_visible()
    box = chart.bounding_box()
    first, last = ticks.first.bounding_box(), ticks.last.bounding_box()
    assert first["x"] >= box["x"], (first, box)
    assert last["x"] + last["width"] <= box["x"] + box["width"], (last, box)
    assert first["x"] + first["width"] < last["x"], (first, last)
    metrics_panel = page.get_by_role("region", name="Metrics", exact=True)
    expect(metrics_panel.get_by_role("group", name="Time range")).to_be_visible()
    metrics_panel.get_by_role("group", name="Time range").get_by_role(
        "button", name="6h", exact=True
    ).click()
    expect(page).to_have_url(re.compile(r"range=6h"))
    page.get_by_role("button", name=re.compile(r"^Events")).click()
    expect(page).to_have_url(re.compile(rf"[?&]activity={name}(&|$)"))
    scoped = page.get_by_role("dialog", name=f"{name} activity", exact=True)
    expect(scoped).to_be_visible()
    expect(
        scoped.get_by_text("The service exited with code 1.", exact=True)
    ).to_be_visible()
    page.keyboard.press("Escape")
    expect(scoped).to_have_count(0)
    expect(page).not_to_have_url(re.compile(r"[?&]activity="))
    page.get_by_role("tab", name=re.compile(r"^Builds")).click()
    expect(
        page.get_by_text(f"No builds. {name} runs a public image.", exact=True)
    ).to_be_visible()
    page.get_by_role("tab", name=re.compile(r"^Deployments")).click()
    deployment_row = page.get_by_role("row").filter(has_text=deployment[-8:])
    expect(deployment_row.get_by_role("cell").nth(-2)).to_have_text("30m ago")
    switcher = page.get_by_role("button", name="Switch resource", exact=True)
    switcher.click()
    current = page.get_by_role("option", name=re.compile(rf"^{name}"))
    expect(
        current.get_by_text("Ranks products for each shopper", exact=True)
    ).to_be_visible()
    expect(current.get_by_label("Current resource", exact=True)).to_be_visible()
    filter_box = page.get_by_placeholder("Filter resources", exact=True)
    expect(filter_box).to_have_count(0)
    page.keyboard.press("Escape")
    fruit = [
        "zzq-" + fruit
        for fruit in ("pear", "apple", "mango", "plum", "fig", "kiwi", "lime", "date")
    ]
    evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": fruit_name,
                "service": {
                    "routing": {"port": 8080},
                    "regions": {
                        "us-ca-1": {
                            "enabled": True,
                            "cpu": "100m",
                            "memory": "128Mi",
                            "minReplicas": 1,
                            "maxReplicas": 1,
                        }
                    },
                },
            }
            for fruit_name in fruit
        ],
    )
    page.reload()
    switcher.click()
    expect(filter_box).to_be_visible()
    names = [
        text.split("\n")[0] for text in page.get_by_role("option").all_inner_texts()
    ]
    assert names == sorted(names) and len(names) > 8, names
    filter_box.fill("zzq-ma")
    expect(page.get_by_role("option")).to_have_count(1)
    expect(page.get_by_role("option", name="zzq-mango")).to_be_visible()
    filter_box.fill("zzq-none")
    expect(page.get_by_text("No resources match.", exact=True)).to_be_visible()
    page.keyboard.press("Escape")
    more = page.get_by_role("button", name="More actions", exact=True)
    expect(page.get_by_text("Managed by loco.yaml", exact=True).first).to_be_visible()
    more.click()
    created = []

    def create_deployment(route):
        created.append(route.request.post_data_json)
        route.fulfill(json={})

    context.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment", create_deployment
    )
    restart = page.get_by_role("menuitem", name="Restart", exact=True)
    expect(restart).to_be_enabled()
    restart.click()
    confirm = page.get_by_role("dialog", name=f"Restart {name}", exact=True)
    expect(confirm).to_contain_text("Replaces every replica in")
    expect(confirm).to_contain_text("Picks up changed secrets.")
    confirm.get_by_role("button", name="Cancel", exact=True).click()
    expect(confirm).to_have_count(0)
    assert created == [], created
    more.click()
    restart.click()
    confirm.get_by_role("button", name="Restart", exact=True).click()
    expect(page.get_by_text(f"Redeploying {name}", exact=True)).to_be_visible()
    assert len(created) == 1, created
    assert created[0]["resourceId"] == resource, created
    assert created[0]["trigger"] == "DEPLOYMENT_TRIGGER_REDEPLOY", created
    assert created[0]["sourceDeploymentId"] == deployment, created
    context.unroute("**/loco.deployment.v1.DeploymentService/CreateDeployment")
    context.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment",
        lambda route: route.fulfill(
            status=500, json={"code": "internal", "message": "internal error"}
        ),
    )
    more.click()
    restart.click()
    confirm.get_by_role("button", name="Restart", exact=True).click()
    expect(page.get_by_text(f"Redeploying {name} failed", exact=True)).to_be_visible()
    context.unroute("**/loco.deployment.v1.DeploymentService/CreateDeployment")
    sql(
        state,
        f"UPDATE deployment_placements SET status='running' WHERE deployment_id='{deployment}'; UPDATE deployments SET status='running' WHERE id='{deployment}';",
    )
    more.click()
    page.get_by_role("menuitem", name="View spec", exact=True).click()
    dialog = page.get_by_role("dialog")
    expect(dialog).to_be_visible()
    dialog.evaluate(
        "node => Promise.all(node.getAnimations({subtree: true}).map(animation => animation.finished))"
    )
    geometry = dialog.locator(".overflow-auto").evaluate(
        "e => ({height:e.clientHeight, content:e.scrollHeight, width:e.clientWidth})"
    )
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
    variables = page.locator("#set-env")
    expect(
        variables.get_by_text(
            "Plain values, visible to anyone who can read this resource. "
            "For anything sensitive, refer to a secret with ${{ secrets.NAME }}. "
            "Manage secrets in production",
            exact=True,
        )
    ).to_be_visible()
    manage = variables.get_by_role(
        "link", name="Manage secrets in production", exact=True
    )
    manage.click()
    page.wait_for_url(re.compile(r"/secrets\?env=production$"))
    page.go_back()
    mode = page.get_by_label("Value of MODE", exact=True)
    api_base = page.get_by_label("Value of API_BASE_URL", exact=True)
    for plain, value in ((mode, "production"), (api_base, "https://api.example.test")):
        expect(plain).to_have_value(value)
        expect(plain).not_to_be_editable()
    expect(variables.get_by_role("button", name="Add", exact=True)).to_have_count(0)
    count = page.locator("#set-env-title > span")
    expect(count).to_have_text(re.compile(r"^\d+$"))
    general = page.locator("#set-general")
    display = general.get_by_role("textbox", name="Name", exact=True)
    expect(display).to_have_value(name)
    save = page.get_by_role("button", name="Save", exact=True)
    display.fill("   ")
    save.click()
    page.get_by_text("Enter a name", exact=True).wait_for()
    display_name = "QA Echo 🚀"
    display.fill(display_name)
    save.click()
    expect(page.get_by_text(f"Saved {display_name}", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", level=1)).to_have_text(display_name)
    stored = sql(
        state,
        f"SELECT slug || '|' || name FROM resources WHERE id='{resource}';",
    )
    assert stored == f"{name}|{display_name}", stored
    delete = page.get_by_role("button", name="Delete resource", exact=True)
    expect(delete).to_be_enabled()
    delete.click()
    dialog = page.get_by_role("alertdialog", name=f"Delete {display_name}", exact=True)
    expect(
        dialog.get_by_text(f"Type {display_name} to confirm", exact=True)
    ).to_be_visible()
    expect(
        dialog.get_by_text("Deleting here causes drift", exact=False)
    ).to_be_visible()
    expect(
        dialog.get_by_text(
            "It is running. Deleting it stops its compute right away and releases its domains",
            exact=False,
        )
    ).to_be_visible()
    expect(dialog.get_by_role("textbox")).to_be_focused()
    expect(
        dialog.get_by_role("button", name=f"Delete {display_name}", exact=True)
    ).to_be_disabled()
    dialog.get_by_role("textbox").fill(display_name.upper())
    expect(
        dialog.get_by_role("button", name=f"Delete {display_name}", exact=True)
    ).to_be_disabled()
    dialog.get_by_role("textbox").fill(f" {display_name} ")
    expect(
        dialog.get_by_role("button", name=f"Delete {display_name}", exact=True)
    ).to_be_enabled()
    page.keyboard.press("Escape")
    expect(delete).to_be_focused()
    delete.click()
    dialog.get_by_role("textbox").fill(display_name)
    held_delete = []
    delete_rpc = "**/loco.resource.v1.ResourceService/DeleteResource"
    context.route(delete_rpc, lambda route: held_delete.append(route))
    confirm = dialog.get_by_role("button", name=f"Delete {display_name}", exact=True)
    with page.expect_request(delete_rpc):
        confirm.click()
    expect(dialog.get_by_role("textbox")).to_be_disabled()
    expect(dialog.get_by_role("button", name="Cancel", exact=True)).to_be_disabled()
    page.keyboard.press("Escape")
    expect(dialog).to_be_visible()
    held_delete.pop().fulfill(
        status=503, json={"code": "unavailable", "message": "Try again"}
    )
    expect(page.get_by_text("Try again.", exact=True)).to_be_visible()
    expect(dialog.get_by_role("textbox")).to_have_value(display_name)
    expect(confirm).to_be_enabled()
    context.unroute(delete_rpc)
    confirm.click()
    expect(page).to_have_url(
        state["ui_url"] + f"/org/{org}/wks/{workspace}/resources?env=production"
    )
    remaining = sql(state, f"SELECT COUNT(*) FROM resources WHERE id='{resource}';")
    assert remaining == "0", remaining
    assert not errors, errors
    results.append({"spec": geometry})
    context.close()
(out / "results.json").write_text(json.dumps(results, indent=2))
print(f"PASS: {len(results)} resource detail cases")
