import argparse
from pathlib import Path

from harness import load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check that a signed-in user never sees the full-screen loader or sign-in page after the shell renders"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/shell-flash"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "shell-flash", "tests/ui/fixtures/deployment-history/scenario.json"
)

# Records each time the full-screen loader, or a page other than the shell, shows after the sidebar has rendered.
WATCH_JS = """
window.__flashes = [];
window.__loaders = 0;
const seen = new Set();
const shellUp = () => document.querySelector('[data-sidebar="sidebar"], [data-slot="sidebar"]') !== null;
let shellSeen = false;
const check = () => {
  shellSeen = shellSeen || shellUp();
  const loader = document.querySelector('[data-slot="app-loader"]:not([aria-hidden="true"])');
  if (loader && !seen.has(loader)) {
    seen.add(loader);
    window.__loaders += 1;
    if (shellSeen) window.__flashes.push({ what: 'loader', at: performance.now(), path: location.pathname });
  }
  if (shellSeen && location.pathname === '/login') {
    window.__flashes.push({ what: 'login', at: performance.now(), path: location.pathname });
  }
};
new MutationObserver(check).observe(document, { childList: true, subtree: true, attributes: true });
"""


WHOAMI = "**/loco.user.v1.UserService/WhoAmI"


def unauth(route):
    route.fulfill(
        status=401,
        content_type="application/json",
        body='{"code":"unauthenticated","message":"expired"}',
    )


def unavailable(route):
    route.fulfill(
        status=503,
        content_type="application/json",
        body='{"code":"unavailable","message":"down"}',
    )


def once(handler):
    fired = []

    def run(route):
        if fired:
            route.fallback()
            return
        fired.append(1)
        handler(route)

    return run


def scenario(browser, name, trigger):
    context, page, errors = open_page(browser, state=state)
    context.add_init_script(WATCH_JS)
    page.goto(state["ui_url"] + "/")
    expect(page.get_by_role("heading", level=1)).to_have_text("Dashboard")
    page.wait_for_timeout(1000)
    trigger(page)
    page.wait_for_timeout(2000)
    result = page.evaluate("({ flashes: window.__flashes, loaders: window.__loaders })")
    assert not result["flashes"], (name, result)
    expect(page.get_by_role("link", name="Loco dashboard")).to_be_visible()
    context.close()


def refocus(page):
    page.evaluate(
        "document.dispatchEvent(new Event('visibilitychange', { bubbles: true })); window.dispatchEvent(new Event('focus'))"
    )


def open_menu(page):
    page.locator('[data-slot="sidebar-footer"] button').first.click()
    expect(page.get_by_role("menuitem", name="Sign out")).to_be_visible()


with session(out) as browser:
    scenario(browser, "menu", open_menu)
    scenario(
        browser,
        "refocus_unavailable",
        lambda p: (p.context.route(WHOAMI, once(unavailable)), refocus(p)),
    )
    scenario(
        browser,
        "refocus_expired",
        lambda p: (p.context.route(WHOAMI, once(unauth)), refocus(p)),
    )
print("Shell flash passed")
