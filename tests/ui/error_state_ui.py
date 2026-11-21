import argparse
from urllib.parse import parse_qs, urlparse

from harness import load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check the Contact support button on internal errors"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "error_state", "tests/ui/fixtures/audit/scenario.json")
LIST_TOKENS = "**/loco.token.v1.TokenService/ListTokens"
GET_CONFIG = "**/loco.config.v1.ConfigService/GetConfig*"


def fail(route):
    route.fulfill(status=500, json={"code": "internal", "message": "internal error"})


def without_support_email(route):
    config = route.fetch().json()
    config.pop("supportEmail", None)
    route.fulfill(json=config)


with session(repo / "tests/ui/out/error_state") as browser:
    context, page, errors = open_page(browser, state=state)
    context.route(LIST_TOKENS, fail)
    page.goto(state["ui_url"] + "/tokens?owner=organization")
    alert = page.get_by_role("alert")
    expect(alert.get_by_role("heading")).to_have_text("Couldn’t load tokens")
    support = alert.get_by_role("link", name="Contact support")
    expect(support).to_be_visible()
    href = urlparse(support.get_attribute("href"))
    assert href.scheme == "mailto" and href.path == "support@loco.test", href
    body = parse_qs(href.query)["body"][0]
    assert "Call: TokenService/ListTokens" in body and "Code: internal" in body, body
    context.close()

    context, page, errors = open_page(browser, state=state)
    context.route(LIST_TOKENS, fail)
    context.route(GET_CONFIG, without_support_email)
    page.goto(state["ui_url"] + "/tokens?owner=organization")
    alert = page.get_by_role("alert")
    expect(alert.get_by_role("button", name="Copy details")).to_be_visible()
    expect(alert.get_by_role("link", name="Contact support")).to_have_count(0)
    context.close()
print(
    "PASS: internal errors link Contact support to the installation's support email, and hide it without one"
)
