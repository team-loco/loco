import argparse
import datetime
import json
import re
import uuid
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from harness import (
    call,
    evidence,
    expect_rpc,
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
    if env["name"] == "production"
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
                    "us-east-1": {
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
    "cpu": "500m",
    "memory": "512Mi",
    "minReplicas": 1,
    "maxReplicas": 1,
    "port": 8080,
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
            "region": "us-east-1",
            "spec": runtime,
            "ready_replicas": 0,
        }
    ],
)
sql(
    state,
    f"""BEGIN;
UPDATE resources SET partial='recommendation', description='Ranks products for each shopper' WHERE id='{resource}';
INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,actor_type,data)
VALUES('resource.degraded','{org}','{workspace}','{resource}','{production}','agent',jsonb_build_object('reason','crash_loop','message','The service exited with code 1.','region','us-east-1','placementId',(SELECT id::text FROM placements WHERE resource_id='{resource}')));
INSERT INTO deployment_rollouts(deployment_id,revision,snapshot) VALUES('{deployment}',1,'{{"problem":{{"reason":"crash_loop","detail":"The pod restarted"}}}}');
COMMIT;""",
)
desired = json.dumps(
    {
        "app_spec": {
            "serviceSpec": {
                "deployment": {
                    "env": {
                        "MODE": "production",
                        "API_BASE_URL": "https://api.example.test",
                    }
                }
            }
        }
    }
)
sql(
    state,
    f"UPDATE placements SET desired_spec={evidence.sql_literal(desired)} WHERE resource_id='{resource}';",
)
call(
    state,
    "loco.secret.v1.SecretService/SetSecrets",
    {
        "resourceId": resource,
        "environmentId": production,
        "values": {"API_TOKEN": "fixture-token"},
    },
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
USAGE = {"min": 0.5, "p50": 0.6, "max": 0.9}
NOW = datetime.datetime.now(datetime.UTC).replace(microsecond=0)
STAMP = NOW.isoformat()


def metrics(route):
    body = route.request.post_data_json
    if body.get("byPod"):
        route.fulfill(json={"series": []})
        return
    value = USAGE[body["aggregation"]] if "cpu" in body["metricName"] else 0.25
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
        permissions=["clipboard-read", "clipboard-write"],
    )
    context.route("**/loco.observability.v1.ObservabilityService/QueryMetrics", metrics)
    page.goto(base)
    expect(page.get_by_text("Last warning", exact=True)).to_be_visible()
    expect(
        page.get_by_text("The service exited with code 1.", exact=True)
    ).to_be_visible()
    expect(page.get_by_text("limit 0.5 vCPU", exact=True)).to_be_visible()
    expect(page.get_by_text("0.45 vCPU", exact=True)).to_be_visible()
    expect(page.get_by_label("Above 85% of the limit")).to_have_count(1)
    page.get_by_role("button", name=re.compile(r"^us-east-1")).click()
    metrics_href = page.get_by_role(
        "link", name="All metrics", exact=True
    ).get_attribute("href")
    assert parse_qs(urlparse(metrics_href).query)["q"] == ["@region:us-east-1"], (
        metrics_href
    )
    page.get_by_role("button", name=re.compile(r"^Events")).click()
    runtime_events = page.get_by_role("dialog", name="Events", exact=True)
    expect(runtime_events.get_by_text("crash loop", exact=True)).to_be_visible()
    expect(
        runtime_events.get_by_text("The service exited with code 1.", exact=True)
    ).to_be_visible()
    runtime_events.get_by_role("button", name="Warnings", exact=True).click()
    expect(
        runtime_events.get_by_text("The service exited with code 1.", exact=True)
    ).to_be_visible()
    page.keyboard.press("Escape")
    expect(
        page.get_by_label("Not reachable from the internet", exact=True)
    ).to_be_visible()
    expect(
        page.get_by_text(re.compile(r"^deployed .+ by Olivia Owner$"))
    ).to_be_visible()
    page.get_by_role("button", name="Switch resource", exact=True).click()
    expect(
        page.get_by_role("menuitem", name=re.compile(rf"^{name}")).get_by_text(
            "Ranks products for each shopper", exact=True
        )
    ).to_be_visible()
    page.keyboard.press("Escape")
    more = page.get_by_role("button", name="More actions", exact=True)
    more.click()
    page.get_by_role("menuitem", name="Copy image reference", exact=True).click()
    assert page.evaluate("navigator.clipboard.readText()") == image
    expect(page.get_by_text("Managed by loco.yaml", exact=True).first).to_be_visible()
    more.click()
    restart = page.get_by_role("menuitem", name="Restart all replicas", exact=True)
    expect(restart).to_be_enabled()
    with expect_rpc(
        page, "RestartResource", lambda body: body.get("resourceId") == resource
    ) as restarted:
        restart.click()
    assert restarted.value.ok, restarted.value.text()
    expect(page.get_by_text(f"Restarting {name}", exact=True)).to_be_visible()
    assert (
        sql(
            state,
            f"SELECT deployment_id FROM placements WHERE resource_id='{resource}';",
        )
        == deployment
    )
    assert (
        sql(
            state,
            f"SELECT COUNT(*) FROM events WHERE type='resource.restarted' AND resource_id='{resource}' AND environment_id='{production}';",
        )
        == "1"
    )
    receipt = "01a1e000-1234-7000-8000-000000000123"
    failed_restart = "**/loco.resource.v1.ResourceService/RestartResource"
    context.route(
        failed_restart,
        lambda route: route.fulfill(
            status=500,
            headers={
                "x-loco-request-id": receipt,
                "access-control-expose-headers": "x-loco-request-id",
            },
            json={"code": "internal", "message": "internal error"},
        ),
    )
    more.click()
    page.get_by_role("menuitem", name="Restart all replicas", exact=True).click()
    expect(page.get_by_text(f"Couldn’t restart {name}", exact=True)).to_be_visible()
    copy_receipt = page.get_by_title("Copy request ID", exact=True)
    expect(copy_receipt).to_contain_text(receipt)
    copy_receipt.click()
    assert page.evaluate("navigator.clipboard.readText()") == receipt
    # Toasts pause their timers while hovered.
    page.mouse.move(0, 0)
    page.clock.fast_forward(8000)
    expect(copy_receipt).to_be_visible()
    context.unroute(failed_restart)
    sql(state, f"UPDATE deployments SET status='running' WHERE id='{deployment}';")
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
    expect(
        page.get_by_text(
            "Values stay hidden. Revealing one shows it for 30 seconds.",
            exact=True,
        )
    ).to_be_visible()
    mode = page.get_by_label("Value of MODE", exact=True)
    api_base = page.get_by_label("Value of API_BASE_URL", exact=True)
    expect(mode).to_have_attribute("type", "password")
    expect(api_base).to_have_attribute("type", "password")
    expect(
        page.get_by_role("button", name="Show API_TOKEN for 30 seconds", exact=True)
    ).to_be_disabled()
    expect(page.locator("#set-env").get_by_text("soon", exact=True)).to_be_visible()
    page.get_by_role("button", name="Show MODE for 30 seconds", exact=True).click()
    expect(mode).to_have_attribute("type", "text")
    expect(mode).to_have_value("production")
    expect(api_base).to_have_attribute("type", "password")
    page.clock.fast_forward(29_000)
    expect(mode).to_have_attribute("type", "text")
    page.clock.fast_forward(1_000)
    expect(mode).to_have_attribute("type", "password")
    page.get_by_role("button", name="Show MODE for 30 seconds", exact=True).click()
    page.get_by_role("button", name="Hide MODE", exact=True).click()
    expect(mode).to_have_attribute("type", "password")
    replacement = page.get_by_label("New value for API_TOKEN", exact=True)
    replacement.fill("replacement-token")
    expect(replacement).to_have_attribute("type", "password")
    page.get_by_role("button", name="Show API_TOKEN for 30 seconds", exact=True).click()
    expect(replacement).to_have_attribute("type", "text")
    page.clock.fast_forward(30_000)
    expect(replacement).to_have_attribute("type", "password")
    page.clock.resume()
    replacement.fill("")
    count = page.locator("#set-env-title > span")
    expect(count).to_have_text(re.compile(r"^\d+$"))
    delete = page.get_by_role("button", name="Delete resource", exact=True)
    expect(delete).to_be_enabled()
    delete.click()
    dialog = page.get_by_role("alertdialog", name=f"Delete {name}?", exact=True)
    expect(
        dialog.get_by_text("Deleting here causes drift", exact=False)
    ).to_be_visible()
    expect(
        dialog.get_by_text(
            "its active deployment in us-east-1, and no domains", exact=False
        )
    ).to_be_visible()
    expect(dialog.get_by_role("textbox")).to_be_focused()
    expect(
        dialog.get_by_role("button", name=f"Delete {name}", exact=True)
    ).to_be_disabled()
    dialog.get_by_role("textbox").fill(name.upper())
    expect(
        dialog.get_by_role("button", name=f"Delete {name}", exact=True)
    ).to_be_disabled()
    dialog.get_by_role("textbox").fill(f" {name} ")
    expect(
        dialog.get_by_role("button", name=f"Delete {name}", exact=True)
    ).to_be_enabled()
    page.keyboard.press("Escape")
    expect(delete).to_be_focused()
    delete.click()
    dialog.get_by_role("textbox").fill(name)
    held_delete = []
    delete_rpc = "**/loco.resource.v1.ResourceService/DeleteResource"
    context.route(delete_rpc, lambda route: held_delete.append(route))
    confirm = dialog.get_by_role("button", name=f"Delete {name}", exact=True)
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
    expect(dialog.get_by_role("textbox")).to_have_value(name)
    expect(confirm).to_be_enabled()
    context.unroute(delete_rpc)
    confirm.click()
    expect(page).to_have_url(
        state["ui_url"] + f"/org/{org}/wks/{workspace}?env=production"
    )
    remaining = sql(state, f"SELECT COUNT(*) FROM resources WHERE id='{resource}';")
    assert remaining == "0", remaining
    assert not errors, errors
    results.append({"spec": geometry})
    context.close()
(out / "results.json").write_text(json.dumps(results, indent=2))
print(f"PASS: {len(results)} resource detail cases")
