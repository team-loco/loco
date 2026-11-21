import argparse
import json
import uuid
from pathlib import Path

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
    description="Exercise resource autoscaling, observability settings and failed-save recovery"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-scaling"))
args = parser.parse_args()
state = load_state(
    args, "settings-scaling", "tests/ui/fixtures/deployment-history/scenario.json"
)
workspace = state["workspaces"]["acme/shared"]
org = state["orgs"]["acme"]["id"]
defaults = call(state, "loco.config.v1.ConfigService/GetConfig", {})["serviceDefaults"]
regions = ("us-ca-1", "us-va-1")
source_commit = {"sha": "a" * 40, "dirty": True}
clusters = {region: evidence.seed_cluster(state, region) for region in regions}
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)


def resource_fixture():
    name = "scaling-" + uuid.uuid4().hex[:8]
    runtime = {
        "platform": "linux/amd64",
        "build": {"type": "image", "image": "registry.example.com/app:1"},
        "port": 8080,
        "observability": defaults["observability"],
        "regions": {
            region: {
                "cpu": "100m",
                "memory": "128Mi",
                "minReplicas": 1,
                "maxReplicas": 1,
            }
            for region in regions
        },
    }
    resource = evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": name,
                "deployments": [
                    {
                        "environment": "production",
                        "spec": runtime,
                        "placements": {
                            region: {"cluster": cluster}
                            for region, cluster in clusters.items()
                        },
                    }
                ],
            }
        ],
    )[f"acme/shared/{name}"]
    sql(
        state,
        f"UPDATE deployments SET commit_sha='{source_commit['sha']}', commit_dirty=true WHERE resource_id='{resource}';",
    )
    return resource


with session(out) as browser:
    resource = resource_fixture()
    live = f"SELECT deployment_id FROM live_deployment_placements WHERE resource_id='{resource}'"
    context, page, errors = open_page(browser, state=state)
    calls = []
    failing = [True]

    def update(route):
        calls.append(route.request.post_data_json)
        if failing[0]:
            route.fulfill(
                status=503,
                json={"code": "unavailable", "message": "fixture rejected scaling"},
            )
            return
        route.continue_()

    context.route("**/loco.resource.v1.ResourceService/UpdateResource", update)
    deploy_mode = ["unexpected"]
    deployments = []

    def create_deployment(route):
        deployments.append(route.request.post_data_json)
        if deploy_mode[0] == "unexpected":
            route.abort()
        elif deploy_mode[0] == "fail":
            route.fulfill(
                status=503,
                json={
                    "code": "unavailable",
                    "message": "fixture rejected observability",
                },
            )
        else:
            body = route.request.post_data_json
            # The fixture image has no registry. Store the requested snapshot so the
            # normal read path exercises the saved state without resolving that image.
            runtime = evidence.sql_literal(json.dumps(body["spec"]["service"]))
            deployment = sql(
                state,
                f"UPDATE deployments SET spec={runtime} WHERE id IN ({live}) RETURNING id;",
            )
            route.fulfill(json={"deploymentId": deployment})

    context.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment",
        create_deployment,
    )
    page.goto(
        state["ui_url"]
        + f"/org/{org}/wks/{workspace}/resource/{resource}?env=production&tab=settings"
    )
    section = page.locator("#set-scaling")
    expect(section).to_be_visible()
    for region in regions:
        section.get_by_role(
            "switch", name=f"Autoscaling in {region}", exact=True
        ).click()
        section.get_by_role(
            "button",
            name=f"Increase maximum replicas in {region}",
            exact=True,
        ).click()
        section.get_by_role(
            "slider", name=f"CPU target (%) in {region}", exact=True
        ).focus()
        page.keyboard.press("ArrowLeft")
    section.evaluate("node => node.scrollIntoView({block: 'start'})")
    page.wait_for_function("() => !document.documentElement.classList.contains('dark')")

    def assert_edits(region, target):
        expect(
            section.get_by_role(
                "group", name=f"Maximum replicas in {region}", exact=True
            )
        ).to_contain_text("3")
        expect(
            section.get_by_role(
                "slider", name=f"CPU target (%) in {region}", exact=True
            )
        ).to_have_value(str(target))

    def stored_regions():
        return json.loads(
            sql(state, f"SELECT spec FROM resources WHERE id='{resource}';")
        )["regions"]

    def save_and_settle(method="UpdateResource"):
        calls.clear()
        expected_deployments = len(deployments) + 1
        with expect_rpc(
            page,
            method,
            lambda body: (
                body.get("resourceId") == resource
                if method == "UpdateResource"
                else len(deployments) == expected_deployments
                and body == deployments[-1]
            ),
        ) as completed:
            save.click()
        assert completed.value.ok, completed.value.text()
        completed.value.finished()
        expect(page.get_by_role("button", name="Discard", exact=True)).to_have_count(0)
        expect(page.get_by_text("Saved scaling-", exact=False)).to_be_visible()

    target = defaults["cpuTarget"] - 1
    save = page.get_by_role("button", name="Save & redeploy", exact=True)
    save.click()
    expect(page.get_by_text("2 unsaved changes", exact=True)).to_be_visible()
    expect(page.get_by_text("fixture rejected scaling", exact=False)).to_be_visible()
    for region in regions:
        assert_edits(region, target)
    assert len(calls) == 1, calls
    for region in regions:
        sent = calls[0]["regions"]["targets"][region]
        assert (sent["minReplicas"], sent["maxReplicas"]) == (1, 3), sent
        assert sent["scalers"] == {"enabled": True, "cpuTarget": target}, sent

    failing[0] = False
    save_and_settle()
    assert len(calls) == 1, calls
    for region in regions:
        assert stored_regions()[region]["scalers"] == {
            "enabled": True,
            "cpuTarget": target,
        }
        assert_edits(region, target)
    commits = sql(
        state,
        f"SELECT DISTINCT commit_sha || ':' || commit_dirty FROM deployments WHERE id IN ({live});",
    )
    assert commits == f"{source_commit['sha']}:true", commits

    section.get_by_role(
        "slider", name=f"CPU target (%) in {regions[0]}", exact=True
    ).focus()
    page.keyboard.press("ArrowLeft")
    save_and_settle()
    assert stored_regions()[regions[0]]["scalers"]["cpuTarget"] == target - 1

    section.get_by_role(
        "button",
        name=f"Decrease maximum replicas in {regions[0]}",
        exact=True,
    ).click()
    section.get_by_role(
        "switch", name=f"Autoscaling in {regions[0]}", exact=True
    ).click()
    save_and_settle()
    fixed = stored_regions()[regions[0]]
    assert fixed["minReplicas"] == fixed["maxReplicas"] == 1, fixed
    assert not fixed.get("scalers", {}).get("enabled", False), fixed
    autoscaling = section.get_by_role(
        "switch", name=f"Autoscaling in {regions[0]}", exact=True
    )
    autoscaling.click()
    section.get_by_role(
        "slider", name=f"CPU target (%) in {regions[0]}", exact=True
    ).focus()
    page.keyboard.press("ArrowLeft")
    autoscaling.click()
    expect(page.get_by_role("button", name="Discard", exact=True)).to_have_count(0)
    sdk = page.get_by_role("switch", name="Configure SDK environment", exact=True)
    expect(sdk).to_be_checked()
    sdk.focus()
    page.keyboard.press("Space")
    expect(sdk).not_to_be_checked()
    expect(page.get_by_text("1 unsaved change", exact=True)).to_be_visible()
    deploy_mode[0] = "fail"
    save.click()
    expect(
        page.get_by_text("fixture rejected observability", exact=False)
    ).to_be_visible()
    expect(sdk).not_to_be_checked()
    expect(page.get_by_text("1 unsaved change", exact=True)).to_be_visible()
    assert len(deployments) == 1, deployments
    assert sorted(deployments[0]["regions"]) == sorted(regions), deployments
    expected = {"injectOtel": False}
    assert all(
        call["spec"]["service"]["observability"] == expected for call in deployments
    ), deployments
    deployments.clear()
    deploy_mode[0] = "pass"
    save_and_settle("CreateDeployment")
    assert len(deployments) == 1, deployments
    assert sorted(deployments[0]["regions"]) == sorted(regions), deployments
    expect(sdk).not_to_be_checked()
    sdk.click()
    save_and_settle("CreateDeployment")
    expect(sdk).to_be_checked()
    runtime = json.loads(
        sql(
            state,
            f"SELECT json_agg(spec) FROM deployments WHERE id IN ({live});",
        )
    )
    assert all(spec["observability"] == {"injectOtel": True} for spec in runtime), (
        runtime
    )
    assert not errors, errors
    context.close()
print(
    "PASS: Settings scaling and observability settings saves, with failed-save recovery"
)
