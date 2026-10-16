import os
import subprocess
import sys
import tempfile
from contextlib import ExitStack
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from types import SimpleNamespace

import pytest
from playwright.sync_api import expect, sync_playwright

DOCS = Path(__file__).resolve().parents[1]
DESKTOP_WIDTH = 1440
NARROW_WIDTH = 390
VIEWPORT_HEIGHT = 1000


@pytest.fixture(scope="module")
def docs_browser():
    with ExitStack() as cleanup:
        docs_browser = SimpleNamespace()
        subprocess.run(
            [sys.executable, str(DOCS / "scripts/build.py"), "build"], check=True
        )
        handler = partial(SimpleHTTPRequestHandler, directory=str(DOCS / "site"))
        docs_browser.server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
        cleanup.callback(docs_browser.server.server_close)
        docs_browser.thread = Thread(
            target=docs_browser.server.serve_forever, daemon=True
        )
        docs_browser.thread.start()
        cleanup.callback(docs_browser.thread.join)
        cleanup.callback(docs_browser.server.shutdown)
        docs_browser.url = f"http://127.0.0.1:{docs_browser.server.server_port}"
        docs_browser.playwright = sync_playwright().start()
        cleanup.callback(docs_browser.playwright.stop)
        docs_browser.browser = docs_browser.playwright.chromium.launch()
        cleanup.callback(docs_browser.browser.close)
        docs_browser.screenshots = Path(
            os.environ.get("DOCS_SCREENSHOT_DIR", tempfile.mkdtemp(prefix="loco-docs-"))
        )
        docs_browser.screenshots.mkdir(parents=True, exist_ok=True)
        yield docs_browser


def test_theme_and_navigation(docs_browser, subtests):
    for scheme, expected in [("light", "default"), ("dark", "slate")]:
        for name, width in [("desktop", DESKTOP_WIDTH), ("narrow", NARROW_WIDTH)]:
            with subtests.test(scheme=scheme, viewport=name):
                page = docs_browser.browser.new_page(
                    viewport={"width": width, "height": VIEWPORT_HEIGHT},
                    color_scheme=scheme,
                )
                page.goto(docs_browser.url, wait_until="networkidle")
                expect(page.locator("body")).to_have_attribute(
                    "data-md-color-scheme", expected
                )
                assert page.evaluate(
                    "document.documentElement.scrollWidth <= innerWidth"
                )
                page.screenshot(
                    path=str(docs_browser.screenshots / f"after-{scheme}-{name}.png"),
                    full_page=True,
                )
                if name == "desktop":
                    navigation = page.locator(".md-sidebar--primary")
                    expect(navigation).to_be_visible()
                    expect(
                        navigation.get_by_role(
                            "link", name="Install the CLI", exact=True
                        )
                    ).to_be_visible()
                    expect(
                        page.locator(".md-sidebar--secondary .md-nav--secondary")
                    ).to_be_visible()
                    page.evaluate("window.scrollTo(0, document.body.scrollHeight)")
                    expect(
                        navigation.get_by_role(
                            "link", name="Install the CLI", exact=True
                        )
                    ).to_be_in_viewport()
                    page.screenshot(
                        path=str(docs_browser.screenshots / f"scroll-{scheme}.png")
                    )
                    navigation.get_by_role(
                        "link", name="Deployment modes", exact=True
                    ).click()
                    expect(
                        page.get_by_role("heading", name="Deployment modes")
                    ).to_be_visible()
                    expect(
                        page.locator(".md-sidebar--primary").get_by_role(
                            "link", name="Self-hosting", exact=True
                        )
                    ).to_be_visible()
                else:
                    page.locator('.md-header label[for="__drawer"]').click()
                    expect(page.locator("#__drawer")).to_be_checked()
                    page.locator(".md-sidebar--primary").get_by_role(
                        "link", name="Deployment modes", exact=True
                    ).click()
                    expect(
                        page.get_by_role("heading", name="Deployment modes")
                    ).to_be_visible()
                page.goto(docs_browser.url, wait_until="networkidle")
                page.locator('link[href$="assets/loco.css"]').evaluate(
                    "(element) => element.remove()"
                )
                page.screenshot(
                    path=str(docs_browser.screenshots / f"before-{scheme}-{name}.png"),
                    full_page=True,
                )
                page.close()


def test_search_and_markdown_copy(docs_browser):
    page = docs_browser.browser.new_page(
        viewport={"width": DESKTOP_WIDTH, "height": VIEWPORT_HEIGHT},
        permissions=["clipboard-read", "clipboard-write"],
    )
    page.goto(docs_browser.url, wait_until="networkidle")
    page.get_by_role("button", name="Search", exact=False).click()
    search = page.get_by_role("combobox", name="Search")
    search.press_sequentially("self")
    expect(page.get_by_role("heading", name="Self-hosting", exact=True)).to_be_visible()
    search.fill("")
    search.press_sequentially("no-matching-loco-page-xyz")
    expect(page.locator("ol").first.locator("li")).to_have_count(0)
    page.keyboard.press("Escape")
    page.get_by_role("button", name="Copy as Markdown").click()
    page.wait_for_function(
        'navigator.clipboard.readText().then(text => text.includes("Deploy with Loco"))'
    )
    text = page.evaluate("navigator.clipboard.readText()")
    assert "Deploy with Loco" in text
    page.close()
