import argparse
import json
import urllib.request

from harness import load_state, open_page, repo, session
from playwright.sync_api import expect

AGENT = "loco/v1.2.3 (darwin arm64; build-box)"
ADDRESS = "127.0.0.1"

parser = argparse.ArgumentParser(description="Check the device approval page")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "cli-device", "tests/ui/fixtures/audit/scenario.json")


def start_device_login():
    request = urllib.request.Request(
        state["api_url"] + "/loco.auth.v1.AuthService/StartDeviceLogin",
        data=b"{}",
        headers={
            "Content-Type": "application/json",
            "User-Agent": AGENT,
        },
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.loads(response.read())["userCode"]


with session(repo / "tests/ui/out/cli-device") as browser:
    context, page, errors = open_page(browser, state=state)
    code = start_device_login()
    page.goto(state["ui_url"] + "/cli/device?code=" + code)
    device = page.locator('dl[aria-label="Requesting device"]')
    expect(device).to_contain_text("Loco CLI on build-box")
    expect(device).to_contain_text("macOS arm64, v1.2.3")
    expect(device).to_contain_text(ADDRESS)

    page.goto(state["ui_url"] + "/cli/device")
    page.get_by_label("Device code").fill(start_device_login())
    expect(page.locator('dl[aria-label="Requesting device"]')).to_contain_text(ADDRESS)
    assert not errors, errors
    context.close()

    code = start_device_login()
    for motion, animation in (("no-preference", "auth-rise"), ("reduce", "none")):
        context, page, errors = open_page(
            browser, user=None, state=state, reduced_motion=motion
        )
        page.goto(state["ui_url"] + "/cli/device?code=" + code)
        expect(
            page.get_by_text("Check that this matches your terminal")
        ).to_be_visible()
        expect(page.locator("output")).to_have_text(code)
        expect(page.get_by_role("button", name="Continue", exact=True)).to_be_visible()
        expect(page).to_have_url(state["ui_url"] + "/cli/device?code=" + code)
        card = page.get_by_role("heading", name="Sign in to loco").locator(
            "xpath=../.."
        )
        assert (
            card.evaluate("node => getComputedStyle(node).animationName") == animation
        )
        assert not errors, errors
        context.close()
print(
    "PASS: the device approval page shows the requesting device; signed out, it shows the terminal's code "
    "before sign-in, and the card rises in unless motion is reduced"
)
