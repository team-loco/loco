import argparse
from pathlib import Path

from harness import call, load_state, open_page, repo, session
from playwright.sync_api import expect
from settings_fixture import seed_settings_resource

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
_, resource, base = seed_settings_resource(state, "health")
_, _, bare = seed_settings_resource(state, "nohealth", health=False)
defaults = call(state, "loco.config.v1.ConfigService/GetConfig", {})["serviceDefaults"][
    "healthCheck"
]

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
    expect(section.get_by_role("switch", name="Health check")).to_be_checked()
    expect(section.get_by_role("switch", name="Health check")).to_be_disabled()
    expect(section.locator("input").first).to_have_value("/health")
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

    page.goto(bare + "?env=production&tab=settings")
    section = page.locator("#set-health")
    toggle = section.get_by_role("switch", name="Health check")
    expect(toggle).not_to_be_checked()
    expect(section.locator("input")).to_have_count(0)
    port = page.get_by_role("textbox", name="Port", exact=True)
    port.fill("9090")
    save = page.get_by_role("button", name="Save & redeploy", exact=True)
    save.click()
    expect(save).to_be_enabled()
    page.wait_for_timeout(500)
    assert len(deployments) == 1, deployments
    assert "healthCheck" not in deployments[0]["spec"]["service"], deployments[0]

    page.goto(bare + "?env=production&tab=settings")
    toggle.click()
    expect(toggle).to_be_checked()
    path, interval, timeout, failures = section.locator("input").all()
    expect(path).to_have_value(defaults["path"])
    expect(interval).to_have_value(str(defaults["intervalSeconds"]))
    expect(timeout).to_have_value(str(defaults["timeoutSeconds"]))
    expect(failures).to_have_value(str(defaults["failureThreshold"]))
    toggle.click()
    expect(section.locator("input")).to_have_count(0)
    expect(page.get_by_text("unsaved change")).to_have_count(0)
    assert not errors, errors
    context.close()
    browser.close()
print(
    "PASS: health check fields flag invalid values while typing without moving the page"
)
