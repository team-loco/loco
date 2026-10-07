from html.parser import HTMLParser
from pathlib import Path
import subprocess
import sys
import tomllib
import unittest
from urllib.parse import unquote, urlsplit

DOCS = Path(__file__).resolve().parents[1]
BUILD = DOCS / 'scripts' / 'build.py'


class Links(HTMLParser):
    def __init__(self):
        super().__init__()
        self.targets = []

    def handle_starttag(self, tag, attributes):
        for name, value in attributes:
            if name in ['href', 'src'] and value:
                self.targets.append(value)


class SiteTests(unittest.TestCase):
    def test_environment_builds(self):
        for environment, host in [('production', 'docs.loco.build'), ('staging', 'docs.staging.loco.build')]:
            with self.subTest(environment=environment):
                subprocess.run([sys.executable, str(BUILD), 'build', environment], check=True)
                site = DOCS / 'site'
                html = (site / 'index.html').read_text()
                self.assertIn(f'https://{host}/', html)
                self.assertIn('md-sidebar--primary', html)
                self.assertIn('Copy as Markdown', html)
                self.assertIn(f'https://{host}/', (site / 'llms.txt').read_text())
                self.assertIn('Deployment modes', (site / 'llms-full.txt').read_text())
                self.assertTrue((site / 'index.md').is_file())
                self.assertTrue((site / '404.html').is_file())
                for document in site.rglob('*.html'):
                    links = Links()
                    links.feed(document.read_text())
                    for target in links.targets:
                        url = urlsplit(target)
                        if url.scheme or url.netloc or not url.path:
                            continue
                        path = site / unquote(url.path.lstrip('/')) if url.path.startswith('/') else document.parent / unquote(url.path)
                        self.assertTrue(path.exists(), f'{document.relative_to(site)} links to missing {target}')
                self.assertFalse((site / 'notes.md').exists())
                self.assertFalse((site / 'tdd').exists())
                self.assertFalse((site / 'pyproject.toml').exists())
                if environment == 'staging':
                    self.assertIn('noindex, nofollow', html)
                    self.assertIn('Disallow: /', (site / 'robots.txt').read_text())
                    self.assertIn('Staging documentation', html)
                else:
                    self.assertNotIn('noindex, nofollow', html)
                    self.assertIn('Allow: /', (site / 'robots.txt').read_text())

    def test_navigation_stays_expanded(self):
        config = tomllib.loads((DOCS / 'zensical.toml').read_text())
        features = config['project']['theme']['features']
        self.assertIn('navigation.expand', features)
        self.assertNotIn('navigation.prune', features)
        self.assertNotIn('navigation.tabs', features)
        self.assertNotIn('header.autohide', features)
        for page in (DOCS / 'content').rglob('*.md'):
            self.assertNotIn('hide:', page.read_text(), str(page))

    def test_unknown_environment_is_rejected(self):
        result = subprocess.run([sys.executable, str(BUILD), 'build', 'preview'], capture_output=True)
        self.assertNotEqual(result.returncode, 0)


if __name__ == '__main__':
    unittest.main()
