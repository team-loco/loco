import argparse
import uuid
from pathlib import Path

from harness import evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check health check validation while typing in resource settings"
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
region = "us-east-1"
name = f"health-{uuid.uuid4().hex[:8]}"
resource = evidence.seed_resources(
    state,
    [
        {
            "workspace": "acme/shared",
            "name": name,
            "deployments": [
                {
                    "cluster": evidence.seed_cluster(state, region),
                    "environment": "production",
                    "region": region,
                    "spec": {
                        "platform": "linux/amd64",
                        "build": {
                            "type": "image",
                            "image": "registry.example.com/app:1",
                        },
                        "cpu": "100m",
                        "memory": "128Mi",
                        "minReplicas": 1,
                        "maxReplicas": 1,
                        "port": 8080,
                        "observability": {"injectOtel": True},
                        "healthCheck": {
                            "path": "/health",
                            "initialDelaySeconds": 5,
                            "intervalSeconds": 30,
                            "timeoutSeconds": 5,
                            "failureThreshold": 3,
                        },
                    },
                }
            ],
        }
    ],
)[f"acme/shared/{name}"]
workspace = state["workspaces"]["acme/shared"]
org = state["orgs"]["acme"]["id"]
base = f"{state['ui_url']}/org/{org}/wks/{workspace}/resource/{resource}"

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
    section = page.locator("#set-health")
    expect(section).to_be_visible()
    path, interval, timeout = section.locator("input").all()[:3]
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
    assert not errors, errors
    context.close()
    browser.close()
print(
    "PASS: health check fields flag invalid values while typing without moving the page"
)
