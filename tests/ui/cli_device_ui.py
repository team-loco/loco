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
    expect(device).to_contain_text("macOS arm64 · v1.2.3")
    expect(device).to_contain_text(ADDRESS)

    page.goto(state["ui_url"] + "/cli/device")
    page.get_by_label("Device code").fill(start_device_login())
    expect(page.locator('dl[aria-label="Requesting device"]')).to_contain_text(ADDRESS)
    assert not errors, errors
    context.close()
print("PASS: the device approval page shows the requesting device")
