from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from threading import Thread
import unittest

from playwright.sync_api import expect, sync_playwright

DOCS = Path(__file__).resolve().parents[1]
DESKTOP_WIDTH = 1440
NARROW_WIDTH = 390
VIEWPORT_HEIGHT = 1000


class BrowserTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        subprocess.run([sys.executable, str(DOCS / 'scripts/build.py'), 'build'], check=True)
        handler = partial(SimpleHTTPRequestHandler, directory=str(DOCS / 'site'))
        cls.server = ThreadingHTTPServer(('127.0.0.1', 0), handler)
        cls.thread = Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.url = f'http://127.0.0.1:{cls.server.server_port}'
        cls.playwright = sync_playwright().start()
        cls.browser = cls.playwright.chromium.launch()
        cls.screenshots = Path(os.environ.get('DOCS_SCREENSHOT_DIR', tempfile.mkdtemp(prefix='loco-docs-')))
        cls.screenshots.mkdir(parents=True, exist_ok=True)

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.playwright.stop()
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()

    def test_theme_and_navigation(self):
        for scheme, expected in [('light', 'default'), ('dark', 'slate')]:
            for name, width in [('desktop', DESKTOP_WIDTH), ('narrow', NARROW_WIDTH)]:
                with self.subTest(scheme=scheme, viewport=name):
                    page = self.browser.new_page(viewport={'width': width, 'height': VIEWPORT_HEIGHT}, color_scheme=scheme)
                    page.goto(self.url, wait_until='networkidle')
                    expect(page.locator('body')).to_have_attribute('data-md-color-scheme', expected)
                    self.assertTrue(page.evaluate('document.documentElement.scrollWidth <= innerWidth'))
                    page.screenshot(path=str(self.screenshots / f'after-{scheme}-{name}.png'), full_page=True)
                    if name == 'desktop':
                        navigation = page.locator('.md-sidebar--primary')
                        expect(navigation).to_be_visible()
                        expect(navigation.get_by_role('link', name='Install the CLI', exact=True)).to_be_visible()
                        expect(page.locator('.md-sidebar--secondary .md-nav--secondary')).to_be_visible()
                        page.evaluate('window.scrollTo(0, document.body.scrollHeight)')
                        expect(navigation.get_by_role('link', name='Install the CLI', exact=True)).to_be_in_viewport()
                        page.screenshot(path=str(self.screenshots / f'scroll-{scheme}.png'))
                        navigation.get_by_role('link', name='Deployment modes', exact=True).click()
                        expect(page.get_by_role('heading', name='Deployment modes')).to_be_visible()
                        expect(page.locator('.md-sidebar--primary').get_by_role('link', name='Self-hosting', exact=True)).to_be_visible()
                    else:
                        page.locator('.md-header label[for="__drawer"]').click()
                        expect(page.locator('#__drawer')).to_be_checked()
                        page.locator('.md-sidebar--primary').get_by_role('link', name='Deployment modes', exact=True).click()
                        expect(page.get_by_role('heading', name='Deployment modes')).to_be_visible()
                    page.goto(self.url, wait_until='networkidle')
                    page.locator('link[href$="assets/loco.css"]').evaluate('(element) => element.remove()')
                    page.screenshot(path=str(self.screenshots / f'before-{scheme}-{name}.png'), full_page=True)
                    page.close()

    def test_search_and_markdown_copy(self):
        page = self.browser.new_page(viewport={'width': DESKTOP_WIDTH, 'height': VIEWPORT_HEIGHT}, permissions=['clipboard-read', 'clipboard-write'])
        page.goto(self.url, wait_until='networkidle')
        page.get_by_role('button', name='Search', exact=False).click()
        search = page.get_by_role('combobox', name='Search')
        search.press_sequentially('self')
        expect(page.get_by_role('heading', name='Self-hosting', exact=True)).to_be_visible()
        search.fill('')
        search.press_sequentially('no-matching-loco-page-xyz')
        expect(page.locator('ol').first.locator('li')).to_have_count(0)
        page.keyboard.press('Escape')
        page.get_by_role('button', name='Copy as Markdown').click()
        page.wait_for_function('navigator.clipboard.readText().then(text => text.includes("Deploy with Loco"))')
        text = page.evaluate('navigator.clipboard.readText()')
        self.assertIn('Deploy with Loco', text)
        page.close()


if __name__ == '__main__':
    unittest.main()
