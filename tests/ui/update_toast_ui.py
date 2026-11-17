from functools import partial
import argparse
import json
from pathlib import Path

from playwright.sync_api import expect
from harness import load_state, open_page as new_page, repo, session

parser = argparse.ArgumentParser(
    description="Test the toast that offers a reload once a newer dashboard build is served"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/update-toast"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "update_toast_ui", "tests/ui/fixtures/deployment-history/scenario.json"
)
ui = state["ui_url"]
TITLE = "Loco has been updated"


open_page = partial(new_page, state=state)


def serve(page, version):
    page.unroute("**/version.json")
    page.route(
        "**/version.json",
        lambda route: route.fulfill(
            content_type="application/json", body=json.dumps({"version": version})
        ),
    )


def check(page):
    # Coming back to the tab is one of the moments the dashboard checks for a newer build.
    with page.expect_response("**/version.json"):
        page.evaluate("document.dispatchEvent(new Event('visibilitychange'))")


with session(out) as browser:
    try:
        context, page, errors = open_page(
            browser, "owner", storage={"loco:color:v1": "neobrutal"}
        )
        try:
            page.goto(ui + "/dashboard")
            expect(
                page.get_by_role("button", name="Search and run commands")
            ).to_be_visible()
            running = page.locator('meta[name="loco-version"]').get_attribute("content")
            toast = page.locator("[data-sonner-toast]").filter(has_text=TITLE)
            serve(page, running)
            check(page)
            expect(toast).to_have_count(0)
            serve(page, "sha-newer")
            check(page)
            expect(toast).to_be_visible()
            reload = toast.get_by_role("button", name="Reload")
            expect(reload).to_be_visible()
            expect(reload).to_have_css("border-top-width", "2px")
            assert reload.evaluate("node => getComputedStyle(node).boxShadow") != "none"
            check(page)
            expect(toast).to_have_count(1)
            # Dismissed, it stays away until a different build is served.
            toast.get_by_role("button", name="Close toast").click()
            expect(toast).to_have_count(0)
            check(page)
            expect(toast).to_have_count(0)
            serve(page, "sha-newest")
            check(page)
            expect(toast).to_be_visible()
            # A rollback to the build this tab runs takes the toast away.
            serve(page, running)
            check(page)
            expect(toast).to_have_count(0)
            serve(page, "sha-newer")
            check(page)
            page.evaluate("window.beforeReload = true")
            with page.expect_navigation():
                reload.click()
            assert page.evaluate("window.beforeReload") is None
            assert not errors, errors
        except BaseException:
            page.screenshot(path=out / "failure.png", animations="disabled")
            raise
        finally:
            context.close()
    finally:
        browser.close()
print("PASS: update toast")
