import argparse

from harness import expect_rpc, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check that a failed background account refresh keeps the shell, and a failed first load shows the account error"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(
    args, "shell-flash", "tests/ui/fixtures/deployment-history/scenario.json"
)

WHOAMI = "**/loco.user.v1.UserService/WhoAmI"
# Sets window.__interrupted once the sidebar has rendered and then leaves the page, even for a moment.
WATCH_JS = """
window.__interrupted = false;
let seen = false;
new MutationObserver(() => {
  const up = document.querySelector('a[aria-label="Loco dashboard"]') !== null;
  if (up) seen = true;
  else if (seen) window.__interrupted = true;
}).observe(document, { childList: true, subtree: true });
"""
FAILURES = {
    503: '{"code":"unavailable","message":"down"}',
    401: '{"code":"unauthenticated","message":"expired"}',
}


def fail_once(context, status):
    context.route(
        WHOAMI,
        lambda route: route.fulfill(
            status=status, content_type="application/json", body=FAILURES[status]
        ),
        times=1,
    )


def refocus(page, status):
    with expect_rpc(page, "loco.user.v1.UserService/WhoAmI", lambda body: True) as info:
        page.evaluate(
            "document.dispatchEvent(new Event('visibilitychange', { bubbles: true }))"
        )
    assert info.value.status == status


with session(repo / "tests/ui/out/shell-flash") as browser:
    for status in (503, 401):
        context, page, errors = open_page(browser, state=state)
        context.add_init_script(WATCH_JS)
        page.goto(state["ui_url"] + "/dashboard")
        expect(page.get_by_role("heading", level=1)).to_have_text("Dashboard")
        fail_once(context, status)
        refocus(page, status)
        refocus(page, 200)
        assert not page.evaluate("window.__interrupted"), status
        expect(page.get_by_role("heading", level=1)).to_have_text("Dashboard")
        assert not errors, errors
        context.close()

    context, page, errors = open_page(browser, state=state)
    fail_once(context, 503)
    page.goto(state["ui_url"] + "/dashboard")
    expect(page.get_by_text("Error loading data")).to_be_visible()
    context.close()
print("Shell flash passed")
