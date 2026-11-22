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
    autoscaling = section.get_by_role("switch", name="Autoscaling", exact=True)
    cpu_target = section.get_by_role("slider", name="CPU target (%)", exact=True)
    # One control scales every region.
    expect(section.get_by_role("switch")).to_have_count(1)
    autoscaling.click()
    section.get_by_role("button", name="Increase maximum replicas", exact=True).click()
    cpu_target.focus()
    page.keyboard.press("ArrowLeft")
    section.evaluate("node => node.scrollIntoView({block: 'start'})")
    page.wait_for_function("() => !document.documentElement.classList.contains('dark')")

    def assert_edits(target):
        expect(
            section.get_by_role("group", name="Maximum replicas", exact=True)
        ).to_contain_text("3")
        expect(cpu_target).to_have_value(str(target))

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

    target = defaults["cpuTarget"] - 5
    expect(
        section.get_by_text(
            f"Adds replicas when average CPU stays above {target}%.",
            exact=True,
        )
    ).to_be_visible()
    expect(page.locator("#set-size")).to_contain_text("1–3 × (")
    expect(page.locator("#set-danger")).to_contain_text(
        "Removes it from every region, along with its domains and variables."
    )
    save = page.get_by_role("button", name="Save & redeploy", exact=True)
    save.click()
    expect(page.get_by_text("1 unsaved change", exact=True)).to_be_visible()
    expect(page.get_by_text("fixture rejected scaling", exact=False)).to_be_visible()
    assert_edits(target)
    assert len(calls) == 1, calls
    # The one control's values go to every region.
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
    assert_edits(target)
    commits = sql(
        state,
        f"SELECT DISTINCT commit_sha || ':' || commit_dirty FROM deployments WHERE id IN ({live});",
    )
    assert commits == f"{source_commit['sha']}:true", commits

    cpu_target.focus()
    page.keyboard.press("ArrowLeft")
    save_and_settle()
    for region in regions:
        assert stored_regions()[region]["scalers"]["cpuTarget"] == target - 5

    section.get_by_role("button", name="Decrease maximum replicas", exact=True).click()
    autoscaling.click()
    save_and_settle()
    for region in regions:
        fixed = stored_regions()[region]
        assert fixed["minReplicas"] == fixed["maxReplicas"] == 1, fixed
        assert not fixed.get("scalers", {}).get("enabled", False), fixed
    autoscaling.click()
    cpu_target.focus()
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
    # A target off the slider's step shows unchanged and leaves nothing unsaved.
    for region in regions:
        sql(
            state,
            f"""UPDATE deployments SET spec=jsonb_set(jsonb_set(spec, '{{regions,{region},maxReplicas}}', '3'), '{{regions,{region},scalers}}', '{{"enabled": true, "cpuTarget": 72}}') WHERE id IN ({live});""",
        )
    page.reload()
    section = page.locator("#set-scaling")
    expect(
        section.get_by_role("slider", name="CPU target (%)", exact=True)
    ).to_have_attribute("aria-valuenow", "72")
    expect(section.get_by_text("72%", exact=True)).to_be_visible()
    expect(page.get_by_role("button", name="Discard", exact=True)).to_have_count(0)
    # Regions that scale differently show the first region's values and say saving applies them to all.
    sql(
        state,
        f"UPDATE deployments SET spec=jsonb_set(jsonb_set(spec, '{{regions,{regions[1]},minReplicas}}', '2'), '{{regions,{regions[1]},maxReplicas}}', '2') WHERE id IN ({live});",
    )
    page.reload()
    section = page.locator("#set-scaling")
    expect(
        section.get_by_text(
            "Regions scale differently now. Saving applies these values to every region.",
            exact=True,
        )
    ).to_be_visible()
    expect(section.get_by_role("switch")).to_have_count(1)
    assert not errors, errors
    context.close()
print(
    "PASS: Settings scaling and observability settings saves, with failed-save recovery"
)
