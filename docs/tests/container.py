from pathlib import Path
import subprocess
import time
import unittest
from urllib.error import HTTPError, URLError
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parents[2]
PORT = 8080
RUNTIME_USER = "65532:65532"
STARTUP_TIMEOUT = 30
POLL_INTERVAL = 0.1
REQUEST_TIMEOUT = 5


def docker(*args):
    result = subprocess.run(['docker', *args], cwd=ROOT, check=True, capture_output=True, text=True)
    return result.stdout.strip()


def response(url):
    try:
        with urlopen(url, timeout=REQUEST_TIMEOUT) as request:
            return request.status, request.read().decode()
    except HTTPError as error:
        return error.code, error.read().decode()


class ContainerTests(unittest.TestCase):
    def test_runtime_matches_dashboard(self):
        def runtime(path):
            return [line for line in path.read_text().splitlines() if line.startswith('FROM ')][-1]

        self.assertEqual(runtime(ROOT / 'docs/Dockerfile'), runtime(ROOT / 'web/Dockerfile'))

    def test_production_and_staging(self):
        for environment, host in [('production', 'docs.loco.build'), ('staging', 'docs.staging.loco.build')]:
            with self.subTest(environment=environment):
                image = f'loco-docs-test:{environment}'
                docker('buildx', 'build', '--load', '-t', image, '-f', 'docs/Dockerfile', '--build-arg', f'DOCS_ENVIRONMENT={environment}', '.')
                container = docker('run', '--rm', '-d', '-p', f'127.0.0.1::{PORT}', image)
                try:
                    address = docker('port', container, str(PORT)).splitlines()[0]
                    url = f'http://{address}'
                    deadline = time.monotonic() + STARTUP_TIMEOUT
                    while True:
                        try:
                            status, body = response(f'{url}/health')
                            if status == 200:
                                break
                        except (URLError, ConnectionError):
                            pass
                        if time.monotonic() >= deadline:
                            self.fail(docker('logs', container))
                        time.sleep(POLL_INTERVAL)
                    self.assertEqual(body, 'OK')
                    self.assertEqual(docker('inspect', '--format', '{{.Config.User}}', container), RUNTIME_USER)
                    status, body = response(url)
                    self.assertEqual(status, 200)
                    self.assertIn(f'https://{host}/', body)
                    if environment == 'staging':
                        self.assertIn('noindex, nofollow', body)
                    else:
                        self.assertNotIn('noindex, nofollow', body)
                    status, body = response(f'{url}/deployment/modes/')
                    self.assertEqual(status, 200)
                    self.assertIn('Multi-tenant SaaS', body)
                    self.assertEqual(response(f'{url}/assets/loco.css')[0], 200)
                    self.assertIn(f'https://{host}/', response(f'{url}/llms.txt')[1])
                    self.assertIn('Deployment modes', response(f'{url}/llms-full.txt')[1])
                    robots = response(f'{url}/robots.txt')[1]
                    self.assertIn('Disallow: /' if environment == 'staging' else 'Allow: /', robots)
                    status, body = response(f'{url}/missing-doc-page')
                    self.assertEqual(status, 404)
                    self.assertIn('404', body)
                finally:
                    docker('stop', container)


if __name__ == '__main__':
    unittest.main()
