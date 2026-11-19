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
    "port": 8080,
    "regions": {
        "us-east-1": {
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
            "placements": {"us-east-1": {"ready_replicas": 0}},
        }
    ],
)
sql(
    state,
    f"""BEGIN;
UPDATE resources SET partial='recommendation', description='Ranks products for each shopper' WHERE id='{resource}';
INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,actor_type,data)
VALUES('resource.degraded','{org}','{workspace}','{resource}','{production}','agent',jsonb_build_object('reason','restarting','message','The service exited with code 1.','region','us-east-1','placementId',(SELECT id::text FROM placements WHERE resource_id='{resource}')));
INSERT INTO deployment_rollouts(deployment_id,cluster_id,revision,snapshot) VALUES('{deployment}',(SELECT cluster_id FROM deployment_placements WHERE deployment_id='{deployment}'),1,'{{"problem":{{"reason":"restarting","detail":"The pod restarted"}}}}');
COMMIT;""",
)
sql(
    state,
    f"UPDATE deployments SET created_at = NOW() - INTERVAL '30 minutes 30 seconds', started_at = NOW() - INTERVAL '10 minutes 30 seconds' WHERE id='{deployment}';",
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
    page.get_by_role("button", name=re.compile(r"^us-east-1")).click()
    metrics_href = page.get_by_role(
        "link", name="All metrics", exact=True
    ).get_attribute("href")
    assert parse_qs(urlparse(metrics_href).query)["q"] == ["@region:us-east-1"], (
        metrics_href
    )
    page.get_by_role("button", name=re.compile(r"^Events")).click()
    runtime_events = page.get_by_role("dialog", name="Events", exact=True)
    expect(runtime_events.get_by_text("restarting", exact=True)).to_be_visible()
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
    # Every "deployed N ago" counts from when the deployment became ready.
    expect(page.get_by_text("deployed 10 min ago by Olivia Owner")).to_be_visible()
    deployment_row = page.get_by_role("row").filter(has_text="current")
    expect(deployment_row.get_by_role("cell").nth(-1)).to_have_text("10 min ago")
    expect(deployment_row.get_by_role("cell").nth(-2)).to_have_text("30 min ago")
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
                        "us-east-1": {
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
    more.click()
    page.get_by_role("menuitem", name="Copy image reference", exact=True).click()
    assert page.evaluate("navigator.clipboard.readText()") == image
    expect(page.get_by_text("Managed by loco.yaml", exact=True).first).to_be_visible()
    more.click()
    restart = page.get_by_role("menuitem", name="Restart all replicas", exact=True)
    expect(restart).to_be_enabled()
    restart.click()
    confirm = page.get_by_role("alertdialog", name=f"Restart {name}?")
    expect(confirm).to_contain_text(
        f"every replica of {name} in the production environment"
    )
    confirm.get_by_role("button", name="Cancel", exact=True).click()
    expect(confirm).to_have_count(0)
    assert (
        sql(
            state,
            f"SELECT COUNT(*) FROM events WHERE type='resource.restarted' AND resource_id='{resource}';",
        )
        == "0"
    )
    more.click()
    restart.click()
    with expect_rpc(
        page, "RestartResource", lambda body: body.get("resourceId") == resource
    ) as restarted:
        confirm.get_by_role("button", name="Restart all replicas", exact=True).click()
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
    confirm.get_by_role("button", name="Restart all replicas", exact=True).click()
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
            "Secret values stay hidden. Revealing a value you type shows it for 30 seconds.",
            exact=True,
        )
    ).to_be_visible()
    mode = page.get_by_label("Value of MODE", exact=True)
    api_base = page.get_by_label("Value of API_BASE_URL", exact=True)
    for plain, value in ((mode, "production"), (api_base, "https://api.example.test")):
        expect(plain).not_to_have_attribute("type", "password")
        expect(plain).to_have_value(value)
    expect(page.get_by_role("button", name="Show MODE for 30 seconds")).to_have_count(0)
    replacement = page.get_by_label("New value for API_TOKEN", exact=True)
    expect(replacement).to_have_value("")
    expect(replacement).to_have_attribute("placeholder", "•••••••• · type to replace")
    expect(
        variables.get_by_text(
            f"Saved. To use this key, add API_TOKEN to services.{name}.secrets and redeploy.",
            exact=True,
        )
    ).to_be_visible()
    expect(
        page.get_by_role("button", name="Show API_TOKEN for 30 seconds", exact=True)
    ).to_be_disabled()
    replacement.fill("replacement-token")
    expect(replacement).to_have_attribute("type", "password")
    page.get_by_role("button", name="Show API_TOKEN for 30 seconds", exact=True).click()
    expect(replacement).to_have_attribute("type", "text")
    page.clock.fast_forward(30_000)
    expect(replacement).to_have_attribute("type", "password")
    page.clock.resume()
    replacement.fill("")
    variables.get_by_role("button", name="Add", exact=True).click()
    page.get_by_label("New variable name", exact=True).fill("STRIPE_API_KEY")
    page.get_by_label("New variable value", exact=True).fill("sk_test")
    expect(variables.get_by_role("switch", name="Store as secret")).to_have_count(0)
    page.get_by_role("button", name="Add secret", exact=True).click()
    with expect_rpc(
        page, "SetSecrets", lambda body: body.get("resourceId") == resource
    ) as saved:
        page.get_by_role("button", name="Save", exact=True).click()
    assert saved.value.ok, saved.value.text()
    hint = f"Saved. To use this key, add STRIPE_API_KEY to services.{name}.secrets and redeploy."
    expect(page.get_by_text(hint, exact=True)).to_have_count(2)
    variables.get_by_role("button", name="Add", exact=True).click()
    page.get_by_label("New variable name", exact=True).fill("EMPTY_FLAG")
    add_secret = page.get_by_role("button", name="Add secret", exact=True)
    expect(add_secret).to_be_enabled()
    add_secret.click()
    expect(page.get_by_label("New value for EMPTY_FLAG", exact=True)).to_have_value("")
    expect(page.get_by_text("1 unsaved change", exact=True)).to_be_visible()
    count = page.locator("#set-env-title > span")
    expect(count).to_have_text(re.compile(r"^\d+$"))
    general = page.locator("#set-general")
    expect(general.get_by_role("textbox", name="Name", exact=True)).to_have_value(name)
    expect(general.get_by_role("textbox", name="Name", exact=True)).not_to_be_editable()
    display = general.get_by_role("textbox", name="Display name", exact=True)
    expect(display).to_have_value(name)
    save = page.get_by_role("button", name="Save", exact=True)
    display.fill("   ")
    save.click()
    page.get_by_text("Enter a display name", exact=True).wait_for()
    display_name = "QA Echo 🚀"
    display.fill(display_name)
    save.click()
    expect(page.get_by_text(f"Saved {display_name}", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", level=1)).to_have_text(display_name)
    expect(page.get_by_label(f"Name {name}", exact=True)).to_have_text(name)
    stored = sql(
        state,
        f"SELECT name || '|' || display_name FROM resources WHERE id='{resource}';",
    )
    assert stored == f"{name}|{display_name}", stored
    delete = page.get_by_role("button", name="Delete resource", exact=True)
    expect(delete).to_be_enabled()
    delete.click()
    dialog = page.get_by_role("alertdialog", name=f"Delete {display_name}?", exact=True)
    expect(dialog.get_by_text(f"Type {name} to confirm", exact=True)).to_be_visible()
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
        dialog.get_by_role("button", name=f"Delete {display_name}", exact=True)
    ).to_be_disabled()
    dialog.get_by_role("textbox").fill(name.upper())
    expect(
        dialog.get_by_role("button", name=f"Delete {display_name}", exact=True)
    ).to_be_disabled()
    dialog.get_by_role("textbox").fill(f" {name} ")
    expect(
        dialog.get_by_role("button", name=f"Delete {display_name}", exact=True)
    ).to_be_enabled()
    page.keyboard.press("Escape")
    expect(delete).to_be_focused()
    delete.click()
    dialog.get_by_role("textbox").fill(name)
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
