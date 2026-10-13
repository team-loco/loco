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
STARTUP_TIMEOUT = 30
POLL_INTERVAL = 0.1
REQUEST_TIMEOUT = 5
NOT_FOUND_TITLE = 'Page not found | Loco'
NOT_FOUND_PATH = '/config'
SCHEMA_PATH = '/schemas/loco.v1.json'
IMAGE = 'loco-ui-docs-test'
UI_HOST = 'loco.build'
DOCS_HOST = 'docs.loco.build'
API_HOST = 'api.loco.build'
VERSION = 'sha-test'


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
        with error:
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

    def check_browser(self, url):
        screenshots = Path(os.environ.get('DOCS_SCREENSHOT_DIR', tempfile.mkdtemp(prefix='loco-hosted-docs-')))
        screenshots.mkdir(parents=True, exist_ok=True)
        problems = []
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            try:
                for name, host, path, heading in [
                    ('docs', DOCS_HOST, '/', 'Deploy with Loco'),
                    ('docs-subpath', UI_HOST, '/docs/', 'Deploy with Loco'),
                    ('dashboard', UI_HOST, '/', 'Deploy containers,'),
                    ('404-app', UI_HOST, NOT_FOUND_PATH, 'Page not found'),
                    ('404-docs', DOCS_HOST, NOT_FOUND_PATH, 'Page not found'),
                ]:
                    context = browser.new_context()

                    def proxy(route, request, host=host):
                        parsed = urlsplit(request.url)
                        target = parsed.path + ('?' + parsed.query if parsed.query else '')
                        result = route.fetch(url=url + target, headers={**request.headers, 'Host': host})
                        route.fulfill(response=result)

                    context.route('https://' + host + '/**', proxy)
                    page = context.new_page()
                    failures = []
                    page.on('console', lambda message: failures.append(message.text) if 'Content Security Policy' in message.text else None)
                    page.on('requestfailed', lambda request, host=host: failures.append(request.url) if urlsplit(request.url).hostname == host else None)
                    page.on('response', lambda result, host=host: failures.append(f'{result.status} {result.url}') if result.status >= 400 and result.request.resource_type != 'document' and urlsplit(result.url).hostname == host else None)
                    page.goto(f'https://{host}{path}', wait_until='networkidle')
                    expect(page.get_by_role('heading', name=heading)).to_be_visible()
                    page.screenshot(path=str(screenshots / f'{name}.png'), full_page=True)
                    if failures:
                        problems.append(f'{name}: {failures}')
                    context.unroute_all(behavior='ignoreErrors')
                    context.close()
            finally:
                browser.close()
        self.assertFalse(problems, '\n'.join(problems))

    def test_image_serves_the_ui_and_docs(self):
        docker('buildx', 'build', '--load', '-t', IMAGE, '-f', 'web/Dockerfile', '--build-arg', f'PUBLIC_API_URL=https://{API_HOST}', '--build-arg', 'PUBLIC_APP_ENV=PRODUCTION', '--build-arg', f'VERSION={VERSION}', '.')
        container = docker('run', '--rm', '-d', '-p', f'127.0.0.1::{PORT}', IMAGE)
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
            status, ui, ui_headers = response(url, UI_HOST)
            self.assertEqual(status, 200)
            self.assertIn('<div id="root">', ui)
            self.assertNotIn('Deploy with Loco', ui)
            self.assertIn(f'<meta name="loco-version" content="{VERSION}">', ui)
            status, body, version_headers = response(url + '/version.json', UI_HOST)
            self.assertEqual(status, 200)
            self.assertEqual(json.loads(body), {'version': VERSION})
            self.assertEqual(version_headers['Cache-Control'], 'no-cache')
            status, body, schema_headers = response(url + SCHEMA_PATH, UI_HOST)
            self.assertEqual(status, 200)
            self.assertEqual(json.loads(body), json.loads((ROOT / 'schemas/loco.v1.json').read_text()))
            self.assertTrue(schema_headers['Content-Type'].startswith('application/json'), schema_headers['Content-Type'])
            for route in ui_routes():
                path = re.sub(r':[^/]+', 'test', route)
                status, body, _ = response(url + path, UI_HOST)
                self.assertEqual(status, 200, path)
                self.assertEqual(body, ui, path)
                self.assertEqual(response(url + path.rstrip('/') + '/', UI_HOST)[1], ui, path)
            for host, prefix in [(DOCS_HOST, ''), (UI_HOST, '/docs')]:
                base = url + prefix
                status, html, headers = response(base + '/', host)
                self.assertEqual(status, 200)
                self.assertIn('Deploy with Loco', html)
                self.assertIn(f'https://{DOCS_HOST}/', html)
                self.assertEqual(headers['Content-Security-Policy'], ui_headers['Content-Security-Policy'])
                self.assertNotIn("script-src 'self' 'unsafe-inline'", headers['Content-Security-Policy'])
                self.assertIn("'sha256-", headers['Content-Security-Policy'])
                status, body, _ = response(base + '/deployment/modes/', host)
                self.assertEqual(status, 200)
                self.assertIn('Multi-tenant SaaS', body)
                status, _, asset_headers = response(base + '/assets/loco.css', host)
                self.assertEqual(status, 200)
                self.assertEqual(asset_headers['Cache-Control'], 'no-cache')
            for host, path in [(UI_HOST, NOT_FOUND_PATH), (DOCS_HOST, NOT_FOUND_PATH), (UI_HOST, '/docs/missing-doc-page')]:
                status, body, _ = response(url + path, host)
                self.assertEqual(status, 404, f'{host}{path}')
                self.assertIn(f'<title>{NOT_FOUND_TITLE}</title>', body)
                self.assertNotIn('<div id="root">', body)
            self.check_browser(url)
        finally:
            docker('stop', container)


if __name__ == '__main__':
    unittest.main()
