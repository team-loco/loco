import argparse
import json
import re
import uuid
from pathlib import Path

from harness import (
    call,
    copied_text,
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
    description="Add, reference, edit and remove the variables of a dashboard service"
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
    if env["slug"] == "production"
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
                    "placements": {"us-ca-1": {"ready_replicas": 1}},
                    "spec": {
                        "observability": {"injectOtel": True},
                        "build": {
                            "type": "image",
                            "image": "registry.example.com/app:1",
                        },
                        "port": 8080,
                        "regions": {
                            "us-ca-1": {
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


def deliver(env):
    """Stores what the active deployment runs with, as an agent-free CreateDeployment would."""
    return sql(
        state,
        f"""BEGIN;
UPDATE deployments SET spec=jsonb_set(spec, '{{env}}', {evidence.sql_literal(json.dumps(env))}::jsonb)
WHERE id IN (SELECT deployment_id FROM live_deployment_placements WHERE resource_id='{resource}') RETURNING id;
COMMIT;""",
    )


deployment = deliver({"LOG_LEVEL": "info", "SESSION_KEY": "${{ secrets.SESSION_KEY }}"})
call(
    state,
    "loco.secret.v1.SecretService/SetSecrets",
    {"environmentId": production, "values": {"SESSION_KEY": "fixture-session"}},
)


def regional(region, mode):
    return {
        "environment": "production",
        "status": "running",
        "spec": {
            "observability": {"injectOtel": True},
            "build": {"type": "image", "image": "registry.example.com/app:1"},
            "port": 8080,
            "env": {"MODE": mode},
            "regions": {
                region: {
                    "cpu": "100m",
                    "memory": "128Mi",
                    "minReplicas": 1,
                    "maxReplicas": 1,
                }
            },
        },
    }


split_name = "split-" + uuid.uuid4().hex[:8]
split = evidence.seed_resources(
    state,
    [
        {
            "workspace": "acme/shared",
            "name": split_name,
            "deployments": [
                regional("us-ca-1", "east"),
                regional("us-va-1", "west"),
            ],
        }
    ],
)[f"acme/shared/{split_name}"]
base = state["ui_url"] + f"/org/{org}/wks/{workspace}/resource/{resource}"

with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    deployments = []

    def create_deployment(route):
        body = route.request.post_data_json
        deployments.append(body)
        if "spec" in body:
            service = body["spec"]["service"]
            sql(
                state,
                f"UPDATE deployments SET spec={evidence.sql_literal(json.dumps(service))} "
                f"WHERE id IN (SELECT deployment_id FROM live_deployment_placements WHERE resource_id='{resource}');",
            )
        route.fulfill(json={"deploymentId": deployment})

    context.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment", create_deployment
    )
    page.goto(base + "?env=production&tab=settings")
    variables = page.locator("#set-env")
    log_level = variables.get_by_label("Value of LOG_LEVEL", exact=True)
    expect(log_level).to_have_value("info")
    expect(variables.get_by_label("Value of SESSION_KEY", exact=True)).to_have_value(
        "${{ secrets.SESSION_KEY }}"
    )
    expect(
        variables.get_by_text("secret in production, value hidden", exact=True)
    ).to_be_visible()
    expect(
        variables.get_by_text(
            "Plain values, visible to anyone who can read this resource. "
            "For anything sensitive, refer to a secret with ${{ secrets.NAME }}. "
            "Manage secrets in production",
            exact=True,
        )
    ).to_be_visible()
    save = page.get_by_role("button", name="Save & redeploy", exact=True)

    expect(
        variables.get_by_text(
            f"Other resources in this environment can use these to reach {name}.",
            exact=True,
        )
    ).to_be_visible()
    copy_url = variables.get_by_role(
        "button", name=f"Copy ${{{{ {name}.PRIVATE_URL }}}}", exact=True
    )
    expect(copy_url).to_be_visible()
    copy_url.click()
    assert copied_text(page) == f"${{{{ {name}.PRIVATE_URL }}}}"

    variables.get_by_role("button", name="Add", exact=True).click()
    new_name = variables.get_by_label("Name of new variable", exact=True)
    new_name.fill("LOG_LEVEL")
    expect(
        variables.get_by_text("LOG_LEVEL is already a variable.", exact=True)
    ).to_be_visible()
    new_name = variables.get_by_label("Name of LOG_LEVEL", exact=True).last
    new_name.fill("feature_flags")
    new_name = variables.get_by_label("Name of FEATURE_FLAGS", exact=True)
    expect(new_name).to_have_value("FEATURE_FLAGS")
    variables.get_by_label("Value of FEATURE_FLAGS", exact=True).fill(
        "${{ secrets.NOPE }}"
    )
    expect(
        variables.get_by_text("No secret named NOPE in production", exact=True)
    ).to_be_visible()
    variables.get_by_label("Value of FEATURE_FLAGS", exact=True).fill("beta")
    expect(variables.get_by_text("new", exact=True)).to_have_count(1)

    variables.get_by_role("button", name="Add reference", exact=True).click()
    picker = page.get_by_role("dialog", name="Add a reference")
    expect(picker.get_by_text("Secrets in production", exact=True)).to_be_visible()
    picker.get_by_placeholder("Search secrets and resources").fill(
        f"{split_name}.private_url"
    )
    picker.get_by_role(
        "option", name=re.compile(rf"^{split_name}\.PRIVATE_URL")
    ).click()
    expect(picker).to_have_count(0)
    split_key = split_name.upper().replace("-", "_") + "_URL"
    expect(variables.get_by_label(f"Value of {split_key}", exact=True)).to_have_value(
        f"${{{{ {split_name}.PRIVATE_URL }}}}"
    )
    expect(
        variables.get_by_text(f"→ http://{split_name}:8080", exact=True)
    ).to_be_visible()
    expect(page.get_by_text("2 unsaved changes", exact=True)).to_be_visible()

    with expect_rpc(
        page, "CreateDeployment", lambda body: body.get("resourceId") == resource
    ):
        save.click()
    expect(page.get_by_text(re.compile(r"Rolling out in us-ca-1\."))).to_be_visible()
    assert len(deployments) == 1, deployments
    sent = deployments[0]
    assert sent["spec"]["service"]["env"] == {
        "LOG_LEVEL": "info",
        "SESSION_KEY": "${{ secrets.SESSION_KEY }}",
        "FEATURE_FLAGS": "beta",
        split_key: f"${{{{ {split_name}.PRIVATE_URL }}}}",
    }, sent
    expect(variables.get_by_text("new", exact=True)).to_have_count(0)

    page.reload()
    page.locator("#set-command").get_by_role(
        "textbox", name="Command", exact=True
    ).fill("/serve")
    with expect_rpc(
        page, "CreateDeployment", lambda body: body.get("resourceId") == resource
    ):
        save.click()
    expect(page.get_by_role("button", name="Discard", exact=True)).to_have_count(0)
    assert len(deployments) == 2, deployments
    sent = deployments[1]
    assert sent["spec"]["service"]["command"] == ["/serve"], sent
    assert sent["spec"]["service"]["env"] == {
        "LOG_LEVEL": "info",
        "SESSION_KEY": "${{ secrets.SESSION_KEY }}",
        "FEATURE_FLAGS": "beta",
        split_key: f"${{{{ {split_name}.PRIVATE_URL }}}}",
    }, sent

    log_level.fill("debug")
    variables.get_by_role("button", name="Remove FEATURE_FLAGS", exact=True).click()
    expect(
        variables.get_by_role("button", name="Keep FEATURE_FLAGS", exact=True)
    ).to_be_visible()
    variables.get_by_role("button", name="Remove SESSION_KEY", exact=True).click()
    variables.get_by_role("button", name=f"Remove {split_key}", exact=True).click()
    expect(page.get_by_text("4 unsaved changes", exact=True)).to_be_visible()
    with expect_rpc(
        page, "CreateDeployment", lambda body: body.get("resourceId") == resource
    ):
        save.click()
    expect(log_level).to_have_value("debug")
    assert len(deployments) == 3, deployments
    sent = deployments[2]
    assert sent["spec"]["service"]["env"] == {"LOG_LEVEL": "debug"}, sent
    expect(variables.get_by_label("Value of FEATURE_FLAGS", exact=True)).to_have_count(
        0
    )

    variables.get_by_role("button", name="Remove LOG_LEVEL", exact=True).click()
    save.click()
    expect(variables.get_by_label("Value of LOG_LEVEL", exact=True)).to_have_count(0)
    assert len(deployments) == 4, deployments
    assert deployments[3]["spec"]["service"].get("env", {}) == {}, deployments[3]

    page.goto(base + "?env=production")
    page.get_by_role("button", name="More actions", exact=True).click()
    page.get_by_role("menuitem", name="Restart", exact=True).click()
    dialog = page.get_by_role("dialog", name=f"Restart {name}", exact=True)
    dialog.get_by_role("button", name="Restart", exact=True).click()
    expect(dialog).to_have_count(0)
    expect(
        page.locator("[data-sonner-toast]").filter(has_text=f"Redeploying {name}")
    ).to_be_visible()
    expect(page.get_by_role("status")).to_have_count(0)
    assert len(deployments) == 5, deployments
    assert "spec" not in deployments[4], deployments[4]
    assert deployments[4]["environmentId"] == production, deployments[4]

    page.goto(
        state["ui_url"]
        + f"/org/{org}/wks/{workspace}/resource/{split}?env=production&tab=settings"
    )
    expect(
        page.locator("#set-env").get_by_text(
            "us-ca-1 and us-va-1 run different variables. This list shows one of them; "
            "saving applies it to every region.",
            exact=True,
        )
    ).to_be_visible()
    page.goto(base + "?env=production&tab=settings")
    expect(variables.get_by_text("run different variables", exact=False)).to_have_count(
        0
    )
    assert not errors, errors
    context.close()
print("PASS: variables")
