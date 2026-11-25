import argparse
import json
import uuid
from pathlib import Path

import evidence
from harness import call, expect_rpc, load_state, open_page, repo, session, sql
from playwright.sync_api import expect
from settings_fixture import REGION, seed_settings_resource

parser = argparse.ArgumentParser(
    description="Check the health check switch and validation while typing in resource settings"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-health"))
args = parser.parse_args()
state = load_state(
    args, "settings-health", "tests/ui/fixtures/deployment-history/scenario.json"
)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
_, resource, base = seed_settings_resource(state, "health")
_, unchecked, unchecked_base = seed_settings_resource(state, "health-off", health=False)
config = call(state, "loco.config.v1.ConfigService/GetConfig", {})
default_health = config["serviceDefaults"]["healthCheck"]
default_health["initialDelaySeconds"] = 17
digest = "sha256:" + "a" * 64
server_check = {
    "path": "/server",
    "initialDelaySeconds": 5,
    "intervalSeconds": 30,
    "timeoutSeconds": 5,
    "failureThreshold": 3,
}


def undeployed(name, health=None):
    service = {
        "routing": {"port": 8080},
        "regions": {
            REGION: {
                "enabled": True,
                "cpu": "100m",
                "memory": "128Mi",
                "minReplicas": 1,
                "maxReplicas": 1,
            }
        },
    }
    if health is not None:
        service["healthCheck"] = health
    resource_id = evidence.seed_resources(
        state, [{"workspace": "acme/shared", "name": name, "service": service}]
    )[f"acme/shared/{name}"]
    return base.rsplit("/resource/", 1)[0] + f"/resource/{resource_id}"


idle_checked = undeployed("health-idle-checked", server_check)
idle_unchecked = undeployed("health-idle")


def redeploy(resource_id, regions, service):
    """Records a new running deployment of the service in the regions, as a finished rollout would."""
    names = ",".join(evidence.sql_literal(r) for r in regions)
    clusters = json.loads(
        sql(
            state,
            "SELECT json_object_agg(region, cluster_id) FROM placements "
            f"WHERE resource_id='{resource_id}' AND region IN ({names});",
        )
    )
    deployment_id = str(uuid.uuid4())
    evidence.seed_deployments(
        state,
        resource_id,
        [
            {
                "id": deployment_id,
                "environment": "production",
                "live": False,
                "spec": service,
                "placements": {r: {"cluster": clusters[r]} for r in regions},
            }
        ],
        then=[
            f"UPDATE placements SET deployment_id='{deployment_id}' "
            f"WHERE resource_id='{resource_id}' AND region IN ({names});"
        ],
    )
    return deployment_id


OTHER_REGION = "us-va-1"


def two_deployments(name):
    """Seeds a service whose two regions run separate deployments with the same health check."""

    def spec(region):
        return {
            "platform": "linux/amd64",
            "build": {"type": "image", "image": "registry.example.com/app:1"},
            "regions": {
                region: {
                    "cpu": "100m",
                    "memory": "128Mi",
                    "minReplicas": 1,
                    "maxReplicas": 1,
                }
            },
            "port": 8080,
            "observability": {"injectOtel": True},
            "env": {"LOG_LEVEL": "info"},
            "healthCheck": server_check,
        }

    resource_id = evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": name,
                "deployments": [
                    {
                        "environment": "production",
                        "placements": {
                            region: {"cluster": evidence.seed_cluster(state, region)}
                        },
                        "spec": spec(region),
                    }
                    for region in (REGION, OTHER_REGION)
                ],
            }
        ],
    )[f"acme/shared/{name}"]
    return resource_id, base.rsplit("/resource/", 1)[0] + f"/resource/{resource_id}"


split, split_base = two_deployments("health-split")


with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    deployments = []
    # A health check the fake server stores in place of the requested one.
    server_override = {}
    # Regions whose deployment requests the fake server refuses.
    failing = set()

    def create_deployment(route):
        body = route.request.post_data_json
        deployments.append(body)
        if failing & set(body["regions"]):
            route.fulfill(
                status=503, json={"code": "unavailable", "message": "region down"}
            )
            return
        service = {**body["spec"]["service"], **server_override}
        if "@" not in service["build"]["image"]:
            service["build"]["image"] += "@" + digest
        # The fixture image has no registry, so the fake records the rollout as finished.
        deployment = redeploy(body["resourceId"], body["regions"], service)
        route.fulfill(json={"deploymentId": deployment})

    context.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment", create_deployment
    )
    # Reloads of the resource the suite holds back until it releases them.
    held = []

    def get_resource(route):
        if held and held[0] is None:
            held.append(route)
        else:
            route.continue_()

    context.route("**/loco.resource.v1.ResourceService/GetResource", get_resource)
    context.route(
        "**/loco.config.v1.ConfigService/GetConfig*",
        lambda route: route.fulfill(json=config),
    )
    page.goto(base + "?env=production&tab=settings")
    section = page.locator("#set-health")
    expect(section).to_be_visible()
    switch = section.get_by_role("switch", name="Health check", exact=True)
    fields = section.get_by_role("textbox")
    discard = page.get_by_role("button", name="Discard", exact=True)
    expect(switch).to_be_checked()
    path, interval, timeout = fields.all()[:3]
    height = section.bounding_box()["height"]

    def settled():
        assert section.bounding_box()["height"] == height

    timeout.fill("30")
    expect(
        section.get_by_text("Must be below the interval", exact=True)
    ).to_be_visible()
    expect(timeout).to_have_attribute("aria-invalid", "true")
    settled()
    timeout.fill("5")
    expect(section.get_by_text("Must be below the interval", exact=True)).to_have_count(
        0
    )
    interval.fill("5")
    expect(
        section.get_by_text("Must be below the interval", exact=True)
    ).to_be_visible()
    settled()
    interval.fill("4")
    expect(section.get_by_text("Whole seconds, at least 5", exact=True)).to_be_visible()
    settled()
    interval.fill("0")
    expect(section.get_by_text("Whole seconds, at least 5", exact=True)).to_be_visible()
    expect(interval).to_have_attribute("aria-invalid", "true")
    settled()
    interval.fill("30")
    path.fill("nope")
    expect(section.get_by_text("Start the path with /", exact=True)).to_be_visible()
    expect(path).to_have_attribute("aria-invalid", "true")
    settled()
    path.fill("/ready")
    expect(section.get_by_text("Start the path with /", exact=True)).to_have_count(0)
    timeout.fill("40")
    save = page.get_by_role("button", name="Save & redeploy", exact=True)
    save.click()
    expect(save).to_be_disabled()
    assert deployments == [], deployments
    timeout.fill("6")
    expect(save).to_be_enabled()

    def saved(match):
        with expect_rpc(page, "CreateDeployment", match) as completed:
            save.click()
        assert completed.value.ok, completed.value.text()
        expect(discard).to_have_count(0)
        return deployments[-1]

    # Turning the check off hides its fields and removes it on save.
    discard.click()
    switch.click()
    expect(fields).to_have_count(0)
    expect(page.get_by_text("Remove health check", exact=True)).to_be_visible()
    # The server reports a check after the save; the switch follows it once the draft lets go.
    server_override["healthCheck"] = server_check
    sent = saved(lambda body: body.get("resourceId") == resource)
    server_override.clear()
    assert sent["removeHealthCheck"] is True, sent
    assert "healthCheck" not in sent["spec"]["service"], sent
    expect(switch).to_be_checked()
    expect(fields.first).to_have_value("/server")

    # A service no region runs shows its stored check, or none, and cannot change it.
    hint = "The health check can be changed once the service is deployed."
    page.goto(idle_checked + "?env=production&tab=settings")
    expect(section.get_by_text(hint, exact=True)).to_be_visible()
    expect(switch).to_be_checked()
    expect(switch).to_be_disabled()
    expect(fields.first).to_have_value("/server")
    expect(fields.first).to_be_disabled()
    page.goto(idle_unchecked + "?env=production&tab=settings")
    expect(section.get_by_text(hint, exact=True)).to_be_visible()
    expect(switch).not_to_be_checked()
    expect(switch).to_be_disabled()
    expect(fields).to_have_count(0)

    # A service without a check shows the switch off and no values.
    page.goto(unchecked_base + "?env=production&tab=settings")
    expect(switch).not_to_be_checked()
    expect(fields).to_have_count(0)
    switch.click()
    expect(fields.first).to_have_value(default_health["path"])
    switch.click()
    expect(discard).to_have_count(0)

    # Turning it on sends the API's default check, startup delay included.
    switch.click()
    expect(page.get_by_text("Add health check", exact=True)).to_be_visible()
    sent = saved(lambda body: body.get("resourceId") == unchecked)
    assert "removeHealthCheck" not in sent, sent
    assert sent["spec"]["service"]["healthCheck"] == default_health, sent
    expect(switch).to_be_checked()

    # A save that reaches one deployment and not the other stays pending, and a retry sends
    # only the one that failed.
    page.goto(split_base + "?env=production&tab=settings")
    expect(switch).to_be_checked()
    switch.click()
    variables = page.locator("#set-env")
    variables.get_by_role("button", name="Add", exact=True).click()
    variables.get_by_label("Name of new variable", exact=True).fill("FEATURE_FLAG")
    variables.get_by_label("Value of FEATURE_FLAG", exact=True).fill("on")
    failing.add(OTHER_REGION)
    first = len(deployments)
    held.append(None)
    page.get_by_role("button", name="Save & redeploy", exact=True).click()
    expect(page.get_by_text("region down", exact=False)).to_be_visible()
    assert len(deployments) == first + 2, deployments[first:]
    # The notice shows once the requests are done; saving lasts until the held reload returns.
    expect(page.get_by_role("button", name="Saving…", exact=True)).to_be_disabled()
    pending = held[1:]
    held.clear()
    for route in pending:
        route.continue_()
    expect(discard).to_be_enabled()
    failing.clear()
    with expect_rpc(
        page, "CreateDeployment", lambda body: body.get("resourceId") == split
    ) as completed:
        page.get_by_role("button", name="Save & redeploy", exact=True).click()
    assert completed.value.ok, completed.value.text()
    expect(discard).to_have_count(0)
    retried = deployments[first + 2 :]
    assert [d["regions"] for d in retried] == [[OTHER_REGION]], retried
    assert retried[0]["removeHealthCheck"] is True, retried
    assert "healthCheck" not in retried[0]["spec"]["service"], retried
    assert retried[0]["spec"]["service"]["env"]["FEATURE_FLAG"] == "on", retried
    assert not errors, errors
    context.close()
    browser.close()
print(
    "PASS: the health check switch adds and removes a check, and its fields flag invalid values while typing without moving the page"
)
