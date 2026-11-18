import argparse
from pathlib import Path

from harness import call, evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check that the dashboard shows resources changed outside the tab"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/outside-changes"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "outside-changes", "tests/ui/fixtures/deployment-history/scenario.json"
)
org = state["orgs"]["acme"]["id"]
workspace = state["workspaces"]["acme/shared"]
cluster = evidence.seed_cluster(state, "us-east-1", name="outside-changes")
base = state["ui_url"] + f"/org/{org}/wks/{workspace}"


def seed(*names):
    return evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": name,
                "deployments": [
                    {
                        "cluster": cluster,
                        "environment": "production",
                        "region": "us-east-1",
                        "spec": {
                            "build": {
                                "type": "image",
                                "image": f"registry.example.com/{name}:1",
                            },
                            "port": 8080,
                            "cpu": "250m",
                            "memory": "512Mi",
                            "minReplicas": 1,
                            "maxReplicas": 1,
                        },
                    }
                ],
            }
            for name in names
        ],
    )


def delete(resource):
    call(
        state,
        "loco.resource.v1.ResourceService/DeleteResource",
        {"resourceId": resource},
    )


resources = seed("outside-focus", "outside-navigate")
with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    page.goto(base + "/resources?env=production")

    def row(name):
        return page.get_by_role("link", name=name, exact=True)

    expect(row("outside-focus")).to_be_visible()
    expect(row("outside-navigate")).to_be_visible()

    delete(resources["acme/shared/outside-focus"])
    page.evaluate(
        "document.dispatchEvent(new Event('visibilitychange', { bubbles: true }))"
    )
    expect(row("outside-focus")).to_have_count(0)

    page.get_by_role("link", name="Dashboard", exact=True).click()
    expect(page.get_by_role("heading", level=1)).to_have_text("Dashboard")
    delete(resources["acme/shared/outside-navigate"])
    page.get_by_role("link", name="Resources", exact=True).click()
    expect(page.get_by_role("heading", level=1)).to_have_text("Resources")
    expect(row("outside-navigate")).to_have_count(0)

    seed("outside-reload")
    page.reload()
    expect(row("outside-reload")).to_be_visible()
    assert not errors, errors
    context.close()
print("Outside changes passed")
