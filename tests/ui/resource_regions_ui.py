import argparse
import json
import uuid
from pathlib import Path

from harness import call, evidence, load_state, open_page, repo, session, sql
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check editing regions in resource settings"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/resource-regions"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "resource-regions", "tests/ui/fixtures/deployment-history/scenario.json"
)
workspace = state["workspaces"]["acme/shared"]
org = state["orgs"]["acme"]["id"]
name = "regions-" + uuid.uuid4().hex[:8]
with session(out) as browser:
    staging = call(
        state,
        "loco.environment.v1.EnvironmentService/CreateEnvironment",
        {"workspaceId": workspace, "name": "staging"},
    )["environmentId"]
    production = sql(
        state,
        f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND slug='production';",
    )
    east_cluster = evidence.seed_cluster(
        state, "us-ca-1", domains=["apps.example.test"]
    )
    west_cluster = evidence.seed_cluster(
        state, "us-va-1", domains=["apps.example.test"]
    )
    resource = evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": name,
                "deployments": [
                    {
                        "environment": "production",
                        "placements": {"us-ca-1": {"cluster": east_cluster}},
                        "spec": {
                            "platform": "linux/amd64",
                            "observability": {"injectOtel": True},
                            "build": {
                                "type": "image",
                                "image": "registry.example.com/shop:latest",
                            },
                            "port": 8080,
                            "regions": {
                                "us-ca-1": {
                                    "cpu": "500m",
                                    "memory": "512Mi",
                                    "minReplicas": 2,
                                    "maxReplicas": 2,
                                }
                            },
                        },
                    },
                    {
                        "environment": "staging",
                        "placements": {"us-ca-1": {"cluster": east_cluster}},
                        "spec": {
                            "platform": "linux/amd64",
                            "observability": {"injectOtel": True},
                            "build": {
                                "type": "image",
                                "image": "registry.example.com/shop:staging",
                            },
                            "port": 9090,
                            "regions": {
                                "us-ca-1": {
                                    "cpu": "250m",
                                    "memory": "256Mi",
                                    "minReplicas": 1,
                                    "maxReplicas": 1,
                                }
                            },
                        },
                    },
                ],
                "domains": [
                    {
                        "environment": "production",
                        "domain": f"prod-{name}.apps.example.test",
                    },
                    {
                        "environment": "staging",
                        "domain": f"staging-{name}.apps.example.test",
                    },
                ],
            }
        ],
    )[f"acme/shared/{name}"]
    sql(
        state,
        f"UPDATE cluster_heartbeats SET last_heartbeat=NOW() - INTERVAL '5 minutes' WHERE cluster_id IN ('{east_cluster}', '{west_cluster}');",
    )
    # Instant scrolling: a smooth scroll to a focused region card would move the save bar under a click.
    context, page, errors = open_page(browser, state=state, reduced_motion="reduce")
    calls = []
    concurrent_edit = []

    def staging_state():
        return sql(
            state,
            f"""SELECT jsonb_build_object(
            'revision', (SELECT revision FROM environments WHERE id='{staging}'),
            'configuration', (SELECT to_jsonb(c) FROM resource_environments c WHERE resource_id='{resource}' AND environment_id='{staging}'),
            'deployments', (SELECT jsonb_agg(to_jsonb(d) ORDER BY d.id) FROM deployments d WHERE resource_id='{resource}' AND environment_id='{staging}'),
            'placements', (SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM placements p WHERE resource_id='{resource}' AND environment_id='{staging}')
        );""",
        )

    staging_before = staging_state()

    def record(route):
        calls.append(
            (route.request.url.rsplit("/", 1)[-1], route.request.post_data_json)
        )
        if concurrent_edit:
            concurrent_edit.clear()
            # Another save lands between the page's last read and this request.
            sql(
                state,
                f"UPDATE resource_environments SET revision=revision+1 WHERE resource_id='{resource}' AND environment_id=(SELECT id FROM environments WHERE workspace_id='{workspace}' AND slug='production');",
            )
        route.continue_()

    page.route("**/loco.resource.v1.ResourceService/UpdateResource", record)
    page.route("**/loco.deployment.v1.DeploymentService/CreateDeployment", record)

    def stored():
        return json.loads(
            sql(
                state,
                f"SELECT spec FROM resource_environments WHERE resource_id='{resource}' AND environment_id=(SELECT id FROM environments WHERE workspace_id='{workspace}' AND slug='production');",
            )
        )["regions"]

    def save():
        calls.clear()
        page.get_by_role("button", name="Save & redeploy", exact=True).click()

    page.goto(
        state["ui_url"] + f"/org/{org}/wks/{workspace}/resource/{resource}?tab=settings"
    )
    west = page.get_by_role("button", name="Run in us-va-1", exact=True)
    expect(west).to_be_visible()
    expect(west).to_be_enabled()
    east = page.get_by_role("button", name="Run in us-ca-1", exact=True)
    expect(east).to_have_attribute("aria-disabled", "true")
    expect(east).to_have_attribute("title", "A resource needs at least one region")
    east.focus()
    expect(east).to_be_focused()
    page.keyboard.press("Shift+Tab")
    page.keyboard.press("Tab")
    expect(east).to_be_focused()
    page.keyboard.press("Space")
    expect(east).to_have_attribute("aria-pressed", "true")
    expect(
        page.get_by_role("button", name="Save & redeploy", exact=True)
    ).to_have_count(0)
    page.set_viewport_size(evidence.NARROW)
    west.focus()
    page.keyboard.press("Space")
    expect(west).to_have_attribute("aria-pressed", "true")
    expect(page.locator("#set-regions")).to_contain_text("Will be added")
    expect(east).to_be_enabled()
    revision = sql(
        state,
        f"SELECT revision FROM resource_environments WHERE resource_id='{resource}' AND environment_id=(SELECT id FROM environments WHERE workspace_id='{workspace}' AND slug='production');",
    )
    save()
    expect(page.get_by_text("Rolling out in us-va-1.", exact=False)).to_be_visible()
    assert [call[0] for call in calls] == ["UpdateResource"], calls
    request = calls[0][1]
    assert request["revision"] == revision, request
    added = request["regions"]["targets"]["us-va-1"]
    assert added["enabled"] and added["cpu"] == "500m" and added["minReplicas"] == 2, (
        added
    )
    assert stored()["us-va-1"]["enabled"], stored()
    assert (
        sql(
            state,
            f"SELECT count(*) FROM live_deployment_placements WHERE resource_id='{resource}' AND environment_id='{production}' AND region='us-va-1';",
        )
        == "1"
    )
    expect(west).to_have_attribute("aria-pressed", "true")
    assert request["environmentId"] == production, request
    assert staging_state() == staging_before, "production region update changed staging"
    page.goto(
        state["ui_url"]
        + f"/org/{org}/wks/{workspace}/resource/{resource}?env=staging&tab=settings"
    )
    expect(
        page.get_by_role("button", name="Run in us-va-1", exact=True)
    ).to_have_attribute("aria-pressed", "false")
    expect(page.get_by_role("slider", name="CPU", exact=True)).to_have_attribute(
        "aria-valuetext", "0.25 vCPU"
    )
    expect(page.get_by_role("textbox", name="Port", exact=True)).to_have_value("9090")
    expect(page.locator("#set-net")).to_contain_text(
        f"staging-{name}.apps.example.test"
    )
    expect(page.locator("#set-net")).not_to_contain_text(
        f"prod-{name}.apps.example.test"
    )
    page.goto(
        state["ui_url"]
        + f"/org/{org}/wks/{workspace}/resource/{resource}?env=production&tab=settings"
    )
    expect(page.get_by_role("textbox", name="Port", exact=True)).to_have_value("8080")
    expect(page.locator("#set-net")).to_contain_text(f"prod-{name}.apps.example.test")
    expect(page.locator("#set-net")).not_to_contain_text(
        f"staging-{name}.apps.example.test"
    )

    sql(
        state,
        "UPDATE cluster_heartbeats SET last_heartbeat=NOW()-INTERVAL '1 day' FROM clusters WHERE id=cluster_id AND region='us-ca-1';",
    )
    page.reload()
    expect(east).to_be_enabled()
    expect(east).to_contain_text("Offline")
    east.click()
    expect(east).to_contain_text("Will be removed")
    save()
    expect(page.get_by_text("Stopping in us-ca-1.", exact=False)).to_be_visible()
    assert not stored()["us-ca-1"].get("enabled", False), stored()
    assert (
        sql(
            state,
            f"SELECT count(*) FROM live_deployment_placements WHERE resource_id='{resource}' AND environment_id='{production}' AND region='us-ca-1';",
        )
        == "0"
    )
    expect(east).to_be_disabled()
    sql(
        state,
        "UPDATE cluster_heartbeats SET last_heartbeat=NOW() FROM clusters WHERE id=cluster_id AND region='us-ca-1';",
    )
    page.reload()
    east.click()
    save()
    expect(page.get_by_text("Rolling out in us-ca-1.", exact=False)).to_be_visible()

    concurrent_edit.append(True)
    west.click()
    expect(west).to_have_attribute("aria-pressed", "false")
    save()
    expect(page.get_by_text("changed since you opened it", exact=False)).to_be_visible()
    expect(west).to_have_attribute("aria-pressed", "true")
    assert stored()["us-va-1"]["enabled"], stored()

    west.click()
    save()
    expect(page.get_by_text("Stopping in us-va-1.", exact=False)).to_be_visible()
    assert not stored()["us-va-1"].get("enabled", False), stored()
    assert (
        sql(
            state,
            f"SELECT count(*) FROM live_deployment_placements WHERE resource_id='{resource}' AND environment_id='{production}' AND region='us-va-1';",
        )
        == "0"
    )

    west.click()
    page.get_by_role("textbox", name="Tag or digest", exact=True).fill("next")
    save()
    expect(
        page.get_by_text(
            "Save region, size and scaling changes separately", exact=False
        )
    ).to_be_visible()
    assert calls == [], calls
    page.get_by_role("button", name="Discard", exact=True).click()

    sql(state, "UPDATE clusters SET is_active=false WHERE region='us-ca-1';")
    page.reload()
    expect(east).to_contain_text("No cluster in this region")
    sql(state, "UPDATE clusters SET is_active=true WHERE region='us-ca-1';")

    sql(state, f"UPDATE resources SET partial='app' WHERE id='{resource}';")
    page.reload()
    expect(
        page.get_by_role("note").filter(
            has_text="Managed by loco.yaml. Change settings"
        )
    ).to_be_visible()
    expect(west).to_be_disabled()
    sql(
        state,
        f"UPDATE resources SET partial=NULL WHERE id='{resource}'; DELETE FROM placements WHERE resource_id='{resource}';",
    )
    page.reload()
    expect(
        page.get_by_text(
            "Deploy this resource before adding regions", exact=False
        ).first
    ).to_be_visible()
    expect(west).to_be_disabled()
    assert not errors, errors
    context.close()
print("Region settings passed")
