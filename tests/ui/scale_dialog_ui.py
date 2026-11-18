import argparse
import json
import uuid
from functools import partial
from pathlib import Path

from harness import evidence, load_state, open_page, repo, rpc as api_rpc, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check the bulk Scale dialog against an isolated production dashboard"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/scale-dialog"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "scale-dialog", "tests/ui/fixtures/deployment-history/scenario.json"
)
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]


rpc = partial(api_rpc, state)


org = evidence.psql(
    state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';"
)
production = next(
    env["id"]
    for env in rpc("environment", "ListEnvironments", {"workspaceId": workspace})[
        "environments"
    ]
    if env["name"] == "production"
)
cluster = str(uuid.uuid4())
sql = [
    f"INSERT INTO clusters(platforms, id,name,region,provider,is_active,is_default,health_status,last_heartbeat) VALUES(ARRAY['linux/amd64'], '{cluster}','scale-dialog','us-east-1','fixture',true,false,'healthy',NOW());"
]
resources = []
for name, cpu, memory, replicas in (
    ("scale-small", "500m", "512Mi", 1),
    ("scale-large", "250m", "2Gi", 3),
):
    size = {
        "cpu": cpu,
        "memory": memory,
        "minReplicas": replicas,
        "maxReplicas": replicas,
    }
    resource = rpc(
        "resource",
        "CreateResource",
        {
            "workspaceId": workspace,
            "name": name,
            "type": "RESOURCE_TYPE_SERVICE",
            "spec": {
                "service": {
                    "routing": {"port": 8080},
                    "regions": {"us-east-1": {"enabled": True, **size}},
                }
            },
        },
    )["resourceId"]
    resources.append(resource)
    deployment = str(uuid.uuid4())
    runtime = json.dumps(
        {
            "build": {"type": "image", "image": f"registry.example.com/{name}:1"},
            "port": 8080,
            **size,
        }
    )
    sql.append(
        f"INSERT INTO deployments(actor_type,actor_id,method,id,resource_id,cluster_id,region,replicas,status,is_active,environment_id,spec,spec_version,started_at) VALUES('user','{owner['id']}','web','{deployment}','{resource}','{cluster}','us-east-1',{replicas},'running',true,'{production}',{evidence.sql_literal(runtime)},1,NOW());"
    )
    sql.append(
        f"INSERT INTO placements(resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,ready_replicas,status_updated_at) VALUES('{resource}','{cluster}','us-east-1','{deployment}','{production}','{{\"app_spec\":{{}}}}',{replicas},NOW());"
    )
evidence.psql(state["container"], "BEGIN;\n" + "\n".join(sql) + "\nCOMMIT;")
for resource in resources:
    assert (
        rpc(
            "resource",
            "GetResourceEnv",
            {"resourceId": resource, "environmentId": production},
        )
        == {}
    )
limits = evidence.rpc(
    state["api_url"], owner["token"], "loco.config.v1.ConfigService/GetConfig", {}
)["sizeLimits"]
low, high = limits["minReplicas"], limits["maxReplicas"]
base = state["ui_url"] + f"/org/{org}/wks/{workspace}/resources?env=production"
results = []
with session(out) as browser:
    try:
        context, page, errors = open_page(browser, state=state)
        sent = []

        def scale(route):
            sent.append(json.loads(route.request.post_data))
            route.fulfill(content_type="application/json", body="{}")

        context.route("**/loco.resource.v1.ResourceService/ScaleResource", scale)
        try:
            page.goto(base)
            page.get_by_role("checkbox", name="Select scale-small").check()
            page.get_by_role("checkbox", name="Select scale-large").check()
            page.get_by_role("button", name="Scale", exact=True).click()
            dialog = page.get_by_role("dialog", name="Scale 2 resources in production")
            expect(dialog.locator("dd")).to_have_text(
                ["1–3", "0.25–0.5 vCPU", "512 Mi–2 Gi"]
            )
            replicas = dialog.get_by_role("textbox", name="Replicas per region")
            cpu = dialog.get_by_role("group", name="CPU per replica", exact=True)
            memory = dialog.get_by_role("group", name="Memory per replica", exact=True)
            submit = dialog.get_by_role("button", name="Scale 2", exact=True)
            expect(replicas).to_have_value("")
            expect(replicas).to_have_attribute("inputmode", "numeric")
            expect(dialog.locator('[aria-pressed="true"]')).to_have_count(0)
            expect(dialog.get_by_text(f"{low} to {high}", exact=True)).to_be_visible()
            expect(submit).to_be_disabled()
            dialog.evaluate(
                "node => Promise.all(node.getAnimations({subtree: true}).map(animation => animation.finished))"
            )
            box = dialog.bounding_box()
            assert box["width"] <= page.viewport_size["width"], box

            for bad in (str(high + 1), "1.5", "two"):
                replicas.fill(bad)
                expect(
                    dialog.get_by_text(f"Enter {low} to {high}", exact=True)
                ).to_be_visible()
                expect(replicas).to_have_attribute("aria-invalid", "true")
                expect(submit).to_be_disabled()
            assert dialog.bounding_box() == box, (box, dialog.bounding_box())
            replicas.fill("")
            cpu.get_by_role("button", name="2 vCPU", exact=True).click()
            expect(submit).to_be_enabled()
            cpu.get_by_role("button", name="2 vCPU", exact=True).click()
            expect(
                cpu.get_by_role("button", name="2 vCPU", exact=True)
            ).to_have_attribute("aria-pressed", "false")
            expect(submit).to_be_disabled()
            replicas.fill(str(high))
            memory.get_by_role("button", name="4 Gi", exact=True).click()
            expect(submit).to_be_enabled()
            assert dialog.bounding_box() == box, (box, dialog.bounding_box())
            submit.click()
            expect(dialog).to_be_hidden()
            assert (
                len(sent) == 2 and len({request["resourceId"] for request in sent}) == 2
            ), sent
            for request in sent:
                assert request["environmentId"] == production, request
                assert (
                    request["replicas"] == high
                    and request["memory"] == "4Gi"
                    and "cpu" not in request
                ), request
            assert not errors, errors
            results.append({"dialog": box})
        except BaseException:
            page.screenshot(path=out / "failure.png", animations="disabled")
            raise
        finally:
            context.close()
    finally:
        browser.close()
(out / "results.json").write_text(json.dumps(results, indent=2))
print(f"PASS: {len(results)} scale dialog cases")
