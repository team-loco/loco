import argparse
import re
from pathlib import Path

from harness import evidence, load_state, open_page, repo, session, sql
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check that Workspace activity alerts say when a region's status began"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/alerts"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(args, "alerts", "tests/ui/fixtures/deployment-history/scenario.json")
workspace = state["workspaces"]["acme/shared"]
org = state["orgs"]["acme"]["id"]
REGION = "us-ca-1"
with session(out) as browser:
    cluster = evidence.seed_cluster(state, REGION)
    resource = evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": "alerts-ui",
                "deployments": [
                    {
                        "environment": "production",
                        "placements": {
                            REGION: {"cluster": cluster, "ready_replicas": 1}
                        },
                        "spec": {
                            "platform": "linux/amd64",
                            "observability": {"injectOtel": True},
                            "build": {
                                "type": "image",
                                "image": "registry.example.test/alerts:v1",
                            },
                            "port": 8080,
                            "regions": {
                                REGION: {
                                    "cpu": "500m",
                                    "memory": "256Mi",
                                    "minReplicas": 2,
                                    "maxReplicas": 2,
                                }
                            },
                        },
                    }
                ],
            }
        ],
    )["acme/shared/alerts-ui"]
    sql(
        state,
        "UPDATE placements SET region_status='RESOURCE_STATUS_DEGRADED', "
        f"region_status_since=NOW()-INTERVAL '3 hours', status_updated_at=NOW() WHERE resource_id='{resource}';",
    )
    context, page, errors = open_page(browser, state=state)
    page.goto(f"{state['ui_url']}/org/{org}/wks/{workspace}?env=production")
    page.get_by_role("button", name=re.compile(r"^Workspace activity, ")).click()
    drawer = page.get_by_role("dialog", name="Workspace activity", exact=True)
    card = (
        drawer.get_by_role("listitem")
        .filter(has_text="alerts-ui")
        .filter(has_text="started 3h ago")
    )
    expect(card.get_by_text("Degraded", exact=True)).to_be_visible()
    expect(card.get_by_text("started 3h ago", exact=True)).to_be_visible()
    expect(card.get_by_text(REGION, exact=True)).to_be_visible()
    expect(
        card.get_by_role("link", name="Open alerts-ui", exact=True)
    ).to_have_attribute("data-variant", "outline")
    expect(card.get_by_role("link", name="Logs", exact=True)).to_have_attribute(
        "data-variant", "outline"
    )
    close = drawer.get_by_role("button", name="Close workspace activity", exact=True)
    close.hover()
    tooltip = page.locator("[data-slot=tooltip-content]")
    expect(tooltip).to_contain_text("Close")
    expect(tooltip.locator("kbd")).to_have_text("esc")
    assert not errors, errors
    context.close()
print("PASS: alerts")
