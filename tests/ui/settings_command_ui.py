import argparse
import json
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
    description="Set, show and clear a service's start command and arguments in Settings"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-command"))
args = parser.parse_args()
state = load_state(
    args, "settings-command", "tests/ui/fixtures/deployment-history/scenario.json"
)
workspace = state["workspaces"]["acme/shared"]
org = state["orgs"]["acme"]["id"]
defaults = call(state, "loco.config.v1.ConfigService/GetConfig", {})["serviceDefaults"]
region = "us-east-1"
cluster = evidence.seed_cluster(state, region)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
name = "command-echo"
resource = evidence.seed_resources(
    state,
    [
        {
            "workspace": "acme/shared",
            "name": name,
            "deployments": [
                {
                    "environment": "production",
                    "placements": {region: {"cluster": cluster}},
                    "spec": {
                        "platform": "linux/amd64",
                        "build": {
                            "type": "image",
                            "image": "registry.example.com/echo:1",
                        },
                        "regions": {
                            region: {
                                "cpu": "100m",
                                "memory": "128Mi",
                                "minReplicas": 1,
                                "maxReplicas": 1,
                            }
                        },
                        "port": 8080,
                        "args": ["-text=old"],
                        "observability": defaults["observability"],
                    },
                }
            ],
        }
    ],
)[f"acme/shared/{name}"]


def stored_spec():
    return json.loads(
        sql(
            state,
            f"SELECT spec FROM deployments WHERE id IN (SELECT deployment_id FROM live_deployment_placements WHERE resource_id='{resource}');",
        )
    )


with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    sent = []

    def create_deployment(route):
        body = route.request.post_data_json
        sent.append(body["spec"]["service"])
        # Stores the requested spec as the active snapshot; the fixture image has no registry.
        runtime = evidence.sql_literal(json.dumps(body["spec"]["service"]))
        deployment = sql(
            state,
            f"UPDATE deployments SET spec={runtime} WHERE id IN (SELECT deployment_id FROM live_deployment_placements WHERE resource_id='{resource}') RETURNING id;",
        )
        route.fulfill(json={"deploymentId": deployment})

    context.route(
        "**/loco.deployment.v1.DeploymentService/CreateDeployment", create_deployment
    )
    settings = (
        state["ui_url"]
        + f"/org/{org}/wks/{workspace}/resource/{resource}?env=production&tab=settings"
    )
    page.goto(settings)
    section = page.locator("#set-command")
    command = section.get_by_role("textbox", name="Command", exact=True)
    arguments = section.get_by_role("textbox", name="Arguments", exact=True)
    expect(command).to_have_value("")
    expect(arguments).to_have_value("-text=old")

    def save():
        with expect_rpc(
            page, "CreateDeployment", lambda body: body.get("resourceId") == resource
        ) as completed:
            page.get_by_role("button", name="Save & redeploy", exact=True).click()
        assert completed.value.ok, completed.value.text()
        expect(page.get_by_role("button", name="Discard", exact=True)).to_have_count(0)
        sql(
            state,
            f"UPDATE deployment_placements SET status='running' WHERE deployment_id IN "
            f"(SELECT deployment_id FROM live_deployment_placements WHERE resource_id='{resource}'); "
            f"UPDATE deployments SET status='running' WHERE id IN (SELECT deployment_id FROM live_deployment_placements WHERE resource_id='{resource}');",
        )

    command.fill("/http-echo")
    arguments.fill("-listen=:8080\n\n-text=hello, world\n")
    expect(page.get_by_text("2 unsaved changes", exact=True)).to_be_visible()
    save()
    assert sent[-1]["command"] == ["/http-echo"], sent[-1]
    assert sent[-1]["args"] == ["-listen=:8080", "-text=hello, world"], sent[-1]

    page.reload()
    expect(command).to_have_value("/http-echo")
    expect(arguments).to_have_value("-listen=:8080\n-text=hello, world")

    arguments.fill("\n")
    expect(page.get_by_text("1 unsaved change", exact=True)).to_be_visible()
    arguments.fill("-listen=:8080\n-text=hello, world")
    expect(page.get_by_role("button", name="Discard", exact=True)).to_have_count(0)

    page.goto(
        state["ui_url"]
        + f"/org/{org}/wks/{workspace}/resource/{resource}?env=production"
    )
    page.get_by_role("button", name="More actions", exact=True).click()
    page.get_by_role("menuitem", name="View spec", exact=True).click()
    dialog = page.get_by_role("dialog")
    expect(dialog).to_contain_text("- /http-echo")
    expect(dialog).to_contain_text('- "-listen=:8080"')
    expect(dialog).to_contain_text('- "-text=hello, world"')
    page.keyboard.press("Escape")

    page.goto(settings)
    command.fill("")
    arguments.fill("")
    save()
    assert "command" not in sent[-1] and "args" not in sent[-1], sent[-1]
    spec = stored_spec()
    assert "command" not in spec and "args" not in spec, spec
    page.reload()
    expect(command).to_have_value("")
    expect(arguments).to_have_value("")
    assert not errors, errors
    context.close()
print("Settings command suite passed")
