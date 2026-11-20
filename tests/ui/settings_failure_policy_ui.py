import argparse
from pathlib import Path

from harness import expect_rpc, load_state, open_page, repo, session, sql
from playwright.sync_api import expect
from settings_fixture import seed_settings_resource

parser = argparse.ArgumentParser(
    description="Change a service's restart policy and deploy timeout in resource settings"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-failure-policy"))
args = parser.parse_args()
state = load_state(
    args,
    "settings-failure-policy",
    "tests/ui/fixtures/deployment-history/scenario.json",
)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
_, resource, base = seed_settings_resource(state, "policy")


def stored():
    return sql(
        state,
        "SELECT concat_ws(',', restart_policy, max_restarts, deploy_timeout_seconds) "
        f"FROM resources WHERE id='{resource}';",
    )


with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    deployments = []

    def create_deployment(route):
        deployments.append(route.request.post_data_json)
        route.abort()

    context.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment", create_deployment
    )
    page.goto(base + "?env=production&tab=settings")
    section = page.locator("#set-deployment")
    expect(section).to_be_visible()
    policy = section.get_by_role("group", name="Restart policy")
    limit = section.get_by_label("Restart limit", exact=True)
    timeout = section.get_by_label("Deploy timeout", exact=True)
    expect(
        policy.get_by_role("button", name="On failure", exact=True)
    ).to_have_attribute("aria-pressed", "true")
    expect(limit).to_have_value("2")
    expect(timeout).to_have_value("600")
    expect(section.get_by_role("switch")).to_have_count(0)

    timeout.fill("0")
    expect(section.get_by_text("Whole seconds, at least 1", exact=True)).to_be_visible()
    expect(timeout).to_have_attribute("aria-invalid", "true")
    save = page.get_by_role("button", name="Save", exact=True)
    save.click()
    expect(save).to_be_disabled()

    # The API owns the bounds: an out-of-range limit saves and comes back with its message.
    timeout.fill("300")
    limit.fill("6")
    with expect_rpc(
        page, "UpdateResource", lambda body: body.get("resourceId") == resource
    ) as rejected:
        save.click()
    assert rejected.value.status == 400, rejected.value.status
    expect(page.get_by_text("must be between 1 and 5", exact=False)).to_be_visible()
    assert stored() == "", stored()

    limit.fill("3")
    with expect_rpc(
        page, "UpdateResource", lambda body: body.get("resourceId") == resource
    ) as completed:
        save.click()
    assert completed.value.ok, completed.value.text()
    sent = completed.value.request.post_data_json["failurePolicy"]
    assert sent == {
        "restartPolicy": "RESTART_POLICY_ON_FAILURE",
        "maxRestarts": 3,
        "deployTimeoutSeconds": 300,
    }, sent
    expect(page.get_by_text("No redeploy needed.", exact=False)).to_be_visible()
    assert stored() == "on-failure,3,300", stored()

    policy.get_by_role("button", name="Never", exact=True).click()
    expect(limit).to_have_count(0)
    expect(
        section.get_by_text("A replica that exits stops a new version", exact=False)
    ).to_be_visible()
    expect(page.get_by_text("1 unsaved change", exact=True)).to_be_visible()
    with expect_rpc(
        page, "UpdateResource", lambda body: body.get("resourceId") == resource
    ) as completed:
        save.click()
    assert completed.value.ok, completed.value.text()
    assert stored() == "never,300", stored()

    page.reload()
    expect(policy.get_by_role("button", name="Never", exact=True)).to_have_attribute(
        "aria-pressed", "true"
    )
    expect(timeout).to_have_value("300")
    expect(limit).to_have_count(0)
    assert deployments == [], deployments
    assert not errors, errors
    context.close()
    browser.close()
print(
    "PASS: the restart policy and deploy timeout save without a redeploy and survive a reload"
)
