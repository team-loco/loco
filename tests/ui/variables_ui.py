import argparse
import json
import re
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
    description="Choose the secrets and add the plain variables of a dashboard service"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/variables"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "variables", "tests/ui/fixtures/deployment-history/scenario.json"
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
name = "vars-" + uuid.uuid4().hex[:8]
resource = evidence.seed_resources(
    state,
    [
        {
            "workspace": "acme/shared",
            "name": name,
            "deployments": [
                {
                    "environment": "production",
                    "status": "running",
                    "placements": {"us-east-1": {"ready_replicas": 1}},
                    "spec": {
                        "observability": {"injectOtel": True},
                        "build": {
                            "type": "image",
                            "image": "registry.example.com/app:1",
                        },
                        "port": 8080,
                        "regions": {
                            "us-east-1": {
                                "cpu": "100m",
                                "memory": "128Mi",
                                "minReplicas": 1,
                                "maxReplicas": 1,
                            }
                        },
                    },
                }
            ],
        }
    ],
)[f"acme/shared/{name}"]
call(
    state,
    "loco.secret.v1.SecretService/SetSecrets",
    {
        "resourceId": resource,
        "environmentId": production,
        "values": {"SESSION_KEY": "fixture-session", "STRIPE_API_KEY": "sk_test"},
    },
)


def deliver(env, secret_names):
    """Stores what the active deployment runs with, as an agent-free CreateDeployment would."""
    desired = json.dumps({"app_spec": {"serviceSpec": {"deployment": {"env": env}}}})
    names = "{" + ",".join(secret_names) + "}"
    return sql(
        state,
        f"""BEGIN;
UPDATE placements SET desired_spec={evidence.sql_literal(desired)} WHERE resource_id='{resource}';
UPDATE deployments SET secret_names='{names}' WHERE id IN (SELECT deployment_id FROM live_deployment_placements WHERE resource_id='{resource}') RETURNING id;
COMMIT;""",
    )


deployment = deliver({"LOG_LEVEL": "info"}, ["SESSION_KEY"])
base = state["ui_url"] + f"/org/{org}/wks/{workspace}/resource/{resource}"

with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    deployments = []

    def create_deployment(route):
        body = route.request.post_data_json
        deployments.append(body)
        bound = body.get("secretBindings", {}).get("names")
        if bound is not None:
            deliver(body["spec"]["service"]["env"], bound)
        route.fulfill(json={"deploymentId": deployment})

    context.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment", create_deployment
    )
    page.goto(base + "?env=production&tab=settings")
    variables = page.locator("#set-env")
    session_key = variables.get_by_role(
        "switch", name="Use SESSION_KEY in deployments", exact=True
    )
    stripe = variables.get_by_role(
        "switch", name="Use STRIPE_API_KEY in deployments", exact=True
    )
    expect(session_key).to_be_checked()
    expect(stripe).not_to_be_checked()
    expect(
        variables.get_by_text("Deployments don't read it", exact=True)
    ).to_be_visible()
    log_level = variables.get_by_label("Value of LOG_LEVEL", exact=True)
    expect(log_level).to_have_value("info")
    expect(log_level).not_to_have_attribute("type", "password")
    expect(variables.get_by_text("Plain", exact=True)).to_be_visible()

    stripe.click()
    expect(stripe).to_be_checked()
    save = page.get_by_role("button", name="Save & redeploy", exact=True)
    expect(save).to_be_visible()
    stripe.click()
    expect(save).to_have_count(0)
    stripe.click()

    variables.get_by_role("button", name="Add", exact=True).click()
    as_secret = variables.get_by_role("switch", name="Store as secret", exact=True)
    expect(as_secret).to_be_checked()
    new_name = variables.get_by_label("New variable name", exact=True)
    new_value = variables.get_by_label("New variable value", exact=True)
    new_name.fill("LOG_LEVEL")
    expect(
        variables.get_by_text("LOG_LEVEL is already a plain variable.", exact=True)
    ).to_be_visible()
    as_secret.click()
    expect(
        variables.get_by_text("LOG_LEVEL is already a variable.", exact=True)
    ).to_be_visible()
    new_name.fill("FEATURE_FLAGS")
    new_value.fill("beta")
    expect(new_value).to_have_attribute("type", "text")
    variables.get_by_role("button", name="Add variable", exact=True).click()
    expect(variables.get_by_label("Value of FEATURE_FLAGS", exact=True)).to_have_value(
        "beta"
    )
    as_secret.click()
    new_name.fill("NEW_TOKEN")
    new_value.fill("token-value")
    expect(new_value).to_have_attribute("type", "password")
    variables.get_by_role("button", name="Add secret", exact=True).click()
    expect(
        variables.get_by_role("switch", name="Use NEW_TOKEN in deployments", exact=True)
    ).to_be_checked()
    expect(page.get_by_text("3 unsaved changes", exact=True)).to_be_visible()

    with expect_rpc(
        page, "SetSecrets", lambda body: body.get("resourceId") == resource
    ) as stored:
        save.click()
    assert stored.value.ok, stored.value.text()
    expect(page.get_by_text(re.compile(r"Rolling out in us-east-1\."))).to_be_visible()
    expect(page.get_by_text("No deployment of", exact=False)).to_have_count(0)
    assert len(deployments) == 1, deployments
    sent = deployments[0]
    assert sent["secretBindings"]["names"] == [
        "NEW_TOKEN",
        "SESSION_KEY",
        "STRIPE_API_KEY",
    ], sent
    assert sent["spec"]["service"]["env"] == {
        "LOG_LEVEL": "info",
        "FEATURE_FLAGS": "beta",
    }, sent
    expect(stripe).to_be_checked()
    expect(
        variables.get_by_role("switch", name="Use NEW_TOKEN in deployments", exact=True)
    ).to_be_checked()
    expect(variables.get_by_label("Value of FEATURE_FLAGS", exact=True)).to_have_value(
        "beta"
    )
    expect(variables.get_by_role("button", name="Remove FEATURE_FLAGS")).to_have_count(
        0
    )

    page.goto(base + "?env=production")
    page.get_by_role("button", name="Redeploy", exact=True).click()
    dialog = page.get_by_role("dialog", name=f"Redeploy {name}?")
    dialog.get_by_role("button", name="Redeploy", exact=True).click()
    expect(dialog).to_have_count(0)
    expect(
        page.locator("[data-sonner-toast]").filter(has_text=f"Redeploying {name}")
    ).to_be_visible()
    expect(page.get_by_role("status")).to_have_count(0)
    assert len(deployments) == 2, deployments
    assert "secretBindings" not in deployments[1], deployments[1]
    assert not errors, errors
    context.close()
print("PASS: variables")
