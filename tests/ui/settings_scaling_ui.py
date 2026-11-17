import argparse
import json
import uuid
from pathlib import Path

from harness import evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Exercise resource autoscaling, observability settings and failed-save recovery"
)
parser.add_argument("--state")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-scaling"))
args = parser.parse_args()
state = load_state(
    args, "settings-scaling", "tests/ui/fixtures/deployment-history/scenario.json"
)
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]
org = evidence.psql(
    state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';"
)
production = evidence.psql(
    state["container"],
    f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND name='production';",
)
defaults = evidence.rpc(
    state["api_url"], owner["token"], "loco.config.v1.ConfigService/GetConfig", {}
)["serviceDefaults"]
regions = ("us-east-1", "eu-west-1")
source_commit = {"sha": "a" * 40, "dirty": True}
clusters = {region: str(uuid.uuid4()) for region in regions}
for region, cluster in clusters.items():
    evidence.psql(
        state["container"],
        f"INSERT INTO clusters(id,name,region,provider,is_active,is_default,health_status,last_heartbeat) VALUES('{cluster}','scaling-{cluster}','{region}','fixture',true,false,'healthy',NOW());",
    )
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)


def resource_fixture():
    name = "scaling-" + uuid.uuid4().hex[:8]
    resource = evidence.rpc(
        state["api_url"],
        owner["token"],
        "loco.resource.v1.ResourceService/CreateResource",
        {
            "workspaceId": workspace,
            "name": name,
            "type": "RESOURCE_TYPE_SERVICE",
            "spec": {
                "service": {
                    "routing": {"port": 8080},
                    "regions": {
                        region: {
                            "enabled": True,
                            "cpu": "100m",
                            "memory": "128Mi",
                            "minReplicas": 1,
                            "maxReplicas": 1,
                        }
                        for region in regions
                    },
                }
            },
        },
    )["resourceId"]
    runtime = json.dumps(
        {
            "build": {"type": "image", "image": "registry.example.com/app:1"},
            "cpu": "100m",
            "memory": "128Mi",
            "minReplicas": 1,
            "maxReplicas": 1,
            "port": 8080,
            "observability": defaults["observability"],
        }
    )
    for region in regions:
        deployment = str(uuid.uuid4())
        cluster = clusters[region]
        evidence.psql(
            state["container"],
            f"""INSERT INTO deployments(actor_type,actor_id,method,commit_sha,commit_dirty,id,resource_id,cluster_id,region,replicas,status,is_active,environment_id,spec,spec_version,started_at)
VALUES('user','{owner["id"]}','web','{source_commit["sha"]}',true,'{deployment}','{resource}','{cluster}','{region}',1,'running',true,'{production}',{evidence.sql_literal(runtime)},1,NOW());
INSERT INTO placements(resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,ready_replicas,status_updated_at)
VALUES('{resource}','{cluster}','{region}','{deployment}','{production}','{{"app_spec":{{}}}}',1,NOW());""",
        )
    return resource


with session(out) as browser:
    try:
        resource = resource_fixture()
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
                deployment = evidence.psql(
                    state["container"],
                    f"UPDATE deployments SET spec={runtime} WHERE resource_id='{resource}' AND region='{body['region']}' AND is_active RETURNING id;",
                )
                route.fulfill(json={"deploymentId": deployment})

        context.route(
            "**/loco.deployment.v1.DeploymentService/CreateDeployment",
            create_deployment,
        )
        try:
            page.goto(
                state["ui_url"]
                + f"/org/{org}/wks/{workspace}/resource/{resource}?env=production&tab=settings"
            )
            section = page.locator("#set-scaling")
            expect(section).to_be_visible()
            for region in regions:
                for _ in range(2):
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
            page.wait_for_function(
                "!document.documentElement.classList.contains('dark')"
            )

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
                    evidence.psql(
                        state["container"],
                        f"SELECT spec FROM resources WHERE id='{resource}';",
                    )
                )["regions"]

            def save_and_settle():
                calls.clear()
                save.click()
                expect(
                    page.get_by_role("button", name="Discard", exact=True)
                ).to_have_count(0)
                expect(page.get_by_text("Saved scaling-", exact=False)).to_be_visible()
                # The API leaves new deployments deploying without a worker; finish them so the
                # next save is not refused as a rollout in progress.
                evidence.psql(
                    state["container"],
                    f"UPDATE deployments SET status='running' WHERE resource_id='{resource}' AND is_active;",
                )

            target = defaults["cpuTarget"] - 1
            save = page.get_by_role("button", name="Save & redeploy", exact=True)
            save.click()
            expect(page.get_by_text("2 unsaved changes", exact=True)).to_be_visible()
            expect(
                page.get_by_text("fixture rejected scaling", exact=False)
            ).to_be_visible()
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
            commits = evidence.psql(
                state["container"],
                f"SELECT DISTINCT commit_sha || ':' || commit_dirty FROM deployments WHERE resource_id='{resource}' AND is_active;",
            )
            assert commits == f"{source_commit['sha']}:true", commits

            section.get_by_role(
                "slider", name=f"CPU target (%) in {regions[0]}", exact=True
            ).focus()
            page.keyboard.press("ArrowLeft")
            save_and_settle()
            assert stored_regions()[regions[0]]["scalers"]["cpuTarget"] == target - 1

            for _ in range(2):
                section.get_by_role(
                    "button",
                    name=f"Decrease maximum replicas in {regions[0]}",
                    exact=True,
                ).click()
            save_and_settle()
            fixed = stored_regions()[regions[0]]
            assert fixed["minReplicas"] == fixed["maxReplicas"] == 1, fixed
            assert not fixed.get("scalers", {}).get("enabled", False), fixed
            section.get_by_role(
                "button", name=f"Increase maximum replicas in {regions[0]}", exact=True
            ).click()
            section.get_by_role(
                "slider", name=f"CPU target (%) in {regions[0]}", exact=True
            ).focus()
            page.keyboard.press("ArrowLeft")
            section.get_by_role(
                "button", name=f"Decrease maximum replicas in {regions[0]}", exact=True
            ).click()
            expect(
                page.get_by_role("button", name="Discard", exact=True)
            ).to_have_count(0)
            sdk = page.get_by_role(
                "switch", name="Configure SDK environment", exact=True
            )
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
            assert len(deployments) == len(regions), deployments
            expected = {"injectOtel": False}
            assert all(
                call["spec"]["service"]["observability"] == expected
                for call in deployments
            ), deployments
            deployments.clear()
            deploy_mode[0] = "pass"
            save_and_settle()
            assert len(deployments) == len(regions), deployments
            expect(sdk).not_to_be_checked()
            sdk.click()
            save_and_settle()
            expect(sdk).to_be_checked()
            runtime = json.loads(
                evidence.psql(
                    state["container"],
                    f"SELECT json_agg(spec) FROM deployments WHERE resource_id='{resource}' AND is_active;",
                )
            )
            assert all(
                spec["observability"] == {"injectOtel": True} for spec in runtime
            ), runtime
            assert not errors, errors
        except BaseException:
            page.screenshot(path=out / "failure.png", animations="disabled")
            raise
        finally:
            context.close()
    finally:
        browser.close()
print(
    "PASS: Settings scaling and observability settings saves, with failed-save recovery"
)
