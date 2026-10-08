import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
import tomllib
import unittest
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen
from urllib.parse import urlsplit

from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[2]
PORT = 8080
RUNTIME_USER = '65532:65532'
DESKTOP_WIDTH = 1440
NARROW_WIDTH = 390
VIEWPORT_HEIGHT = 1000
STARTUP_TIMEOUT = 30
POLL_INTERVAL = 0.1
REQUEST_TIMEOUT = 5


def docker(*args):
    result = subprocess.run(['docker', *args], cwd=ROOT, check=True, capture_output=True, text=True)
    return result.stdout.strip()


def response(url, host=None):
    headers = {'Host': host} if host else {}
    request = Request(url, headers=headers)
    try:
        with urlopen(request, timeout=REQUEST_TIMEOUT) as result:
            return result.status, result.read().decode(), result.headers
    except HTTPError as error:
        return error.code, error.read().decode(), error.headers


def ui_routes():
    route_source = (ROOT / 'web/src/App.tsx').read_text()
    return re.findall(r'<Route path="(/[^\"]*)"', route_source)


class ContainerTests(unittest.TestCase):
    def test_rewrites_cover_exactly_the_ui_routes(self):
        config = tomllib.loads((ROOT / 'web/sws.toml').read_text())
        sections = {route.split('/')[1] for route in ui_routes()} - {''}
        for rewrite in config['advanced']['rewrites']:
            match = re.fullmatch(r'/\{([^}]*)\}(/\*\*)?', rewrite['source'])
            self.assertIsNotNone(match, rewrite['source'])
            self.assertEqual(set(match.group(1).split(',')), sections, rewrite['source'])
            self.assertEqual(rewrite['destination'], '/index.html')

    def check_browser(self, url, environment, ui_host, docs_host):
        screenshots = Path(os.environ.get('DOCS_SCREENSHOT_DIR', tempfile.mkdtemp(prefix='loco-hosted-docs-')))
        screenshots.mkdir(parents=True, exist_ok=True)
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            try:
                for host, prefix, entry in [(docs_host, '', 'hostname'), (ui_host, '/docs', 'subpath')]:
                    for scheme, width, viewport in [('light', DESKTOP_WIDTH, 'desktop'), ('dark', DESKTOP_WIDTH, 'desktop'), ('light', NARROW_WIDTH, 'narrow'), ('dark', NARROW_WIDTH, 'narrow')]:
                        context = browser.new_context(viewport={'width': width, 'height': VIEWPORT_HEIGHT}, color_scheme=scheme, permissions=['clipboard-read', 'clipboard-write'])

                        def proxy(route):
                            parsed = urlsplit(route.request.url)
                            path = parsed.path + ('?' + parsed.query if parsed.query else '')
                            headers = {**route.request.headers, 'Host': host}
                            result = route.fetch(url=url + path, headers=headers)
                            route.fulfill(response=result)

                        context.route('https://' + host + '/**', proxy)
                        page = context.new_page()
                        violations = []
                        page.on('console', lambda message: violations.append(message.text) if 'Content Security Policy' in message.text else None)
                        page.goto(f'https://{host}{prefix}/', wait_until='networkidle')
                        expect(page.get_by_role('heading', name='Deploy with Loco')).to_be_visible()
                        self.assertTrue(page.evaluate('document.documentElement.scrollWidth <= innerWidth'))
                        page.screenshot(path=str(screenshots / f'{environment}-{entry}-{scheme}-{viewport}.png'), full_page=True)
                        if viewport == 'narrow':
                            page.locator('.md-header label[for="__search"]').click()
                        else:
                            page.get_by_role('button', name='Search', exact=False).click()
                        page.get_by_role('combobox', name='Search').press_sequentially('self')
                        expect(page.get_by_role('heading', name='Self-hosting', exact=True)).to_be_visible()
                        page.keyboard.press('Escape')
                        page.get_by_role('button', name='Copy as Markdown').click()
                        page.wait_for_function('navigator.clipboard.readText().then(text => text.includes("Deploy with Loco"))')
                        if viewport == 'narrow':
                            page.locator('.md-header label[for="__drawer"]').click()
                        navigation = page.locator('.md-sidebar--primary')
                        navigation.get_by_role('link', name='Deployment modes', exact=True).click()
                        expect(page.get_by_role('heading', name='Deployment modes')).to_be_visible()
                        self.assertEqual(urlsplit(page.url).path, prefix + '/deployment/modes/')
                        self.assertFalse(violations, violations)
                        context.unroute_all(behavior='wait')
                        context.close()
                context = browser.new_context()

                def dashboard_proxy(route):
                    parsed = urlsplit(route.request.url)
                    result = route.fetch(url=url + parsed.path, headers={**route.request.headers, 'Host': ui_host})
                    route.fulfill(response=result)

                context.route('https://' + ui_host + '/**', dashboard_proxy)
                page = context.new_page()
                page.goto('https://' + ui_host + '/', wait_until='networkidle')
                expect(page.get_by_role('heading', name='Deploy containers,')).to_be_visible()
                context.unroute_all(behavior='wait')
                context.close()
            finally:
                browser.close()

    def test_production_and_staging_share_the_ui(self):
        for environment, ui_host, docs_host, version in [
            ('production', 'loco.build', 'docs.loco.build', 'sha-test'),
            ('staging', 'staging.loco.build', 'docs.staging.loco.build', 'sha-test-staging'),
        ]:
            with self.subTest(environment=environment):
                image = f'loco-ui-docs-test:{environment}'
                api_host = 'api.staging.loco.build' if environment == 'staging' else 'api.loco.build'
                docker('buildx', 'build', '--load', '-t', image, '-f', 'web/Dockerfile', '--build-arg', f'DOCS_ENVIRONMENT={environment}', '--build-arg', f'VITE_API_URL=https://{api_host}', '--build-arg', 'VITE_APP_ENV=PRODUCTION', '--build-arg', f'VERSION={version}', '.')
                container = docker('run', '--rm', '-d', '-p', f'127.0.0.1::{PORT}', image)
                try:
                    address = docker('port', container, str(PORT)).splitlines()[0]
                    url = f'http://{address}'
                    deadline = time.monotonic() + STARTUP_TIMEOUT
                    while True:
                        try:
                            status, body, _ = response(f'{url}/health')
                            if status == 200:
                                break
                        except (URLError, ConnectionError):
                            pass
                        if time.monotonic() >= deadline:
                            self.fail(docker('logs', container))
                        time.sleep(POLL_INTERVAL)
                    self.assertEqual(body, 'OK')
                    self.assertEqual(docker('inspect', '--format', '{{.Config.User}}', container), RUNTIME_USER)
                    status, ui, ui_headers = response(url, ui_host)
                    self.assertEqual(status, 200)
                    self.assertIn('<div id="root">', ui)
                    self.assertNotIn('Deploy with Loco', ui)
                    self.assertIn(f'<meta name="loco-version" content="{version}">', ui)
                    status, body, version_headers = response(url + '/version.json', ui_host)
                    self.assertEqual(status, 200)
                    self.assertEqual(json.loads(body), {'version': version})
                    self.assertEqual(version_headers['Cache-Control'], 'no-cache')
                    for route in ui_routes():
                        path = re.sub(r':[^/]+', 'test', route)
                        status, body, _ = response(url + path, ui_host)
                        self.assertEqual(status, 200, path)
                        self.assertEqual(body, ui, path)
                        self.assertEqual(response(url + path.rstrip('/') + '/', ui_host)[1], ui, path)
                    for host, prefix in [(docs_host, ''), (ui_host, '/docs')]:
                        base = url + prefix
                        status, html, headers = response(base + '/', host)
                        self.assertEqual(status, 200)
                        self.assertIn('Deploy with Loco', html)
                        self.assertIn(f'https://{docs_host}/', html)
                        self.assertEqual(headers['Content-Security-Policy'], ui_headers['Content-Security-Policy'])
                        self.assertNotIn("script-src 'self' 'unsafe-inline'", headers['Content-Security-Policy'])
                        self.assertIn("'sha256-", headers['Content-Security-Policy'])
                        if environment == 'staging':
                            self.assertIn('noindex, nofollow', html)
                        else:
                            self.assertNotIn('noindex, nofollow', html)
                        status, body, _ = response(base + '/deployment/modes/', host)
                        self.assertEqual(status, 200)
                        self.assertIn('Multi-tenant SaaS', body)
                        status, _, asset_headers = response(base + '/assets/loco.css', host)
                        self.assertEqual(status, 200)
                        self.assertEqual(asset_headers['Cache-Control'], 'no-cache')
                        self.assertIn(f'https://{docs_host}/', response(base + '/llms.txt', host)[1])
                        self.assertIn('Deployment modes', response(base + '/llms-full.txt', host)[1])
                        robots = response(base + '/robots.txt', host)[1]
                        self.assertIn('Disallow: /' if environment == 'staging' else 'Allow: /', robots)
                        status, body, _ = response(base + '/missing-doc-page', host)
                        self.assertEqual(status, 404)
                        self.assertIn('404', body)
                        self.assertNotIn('<div id="root">', body)
                    self.check_browser(url, environment, ui_host, docs_host)
                finally:
                    docker('stop', container)


if __name__ == '__main__':
    unittest.main()
