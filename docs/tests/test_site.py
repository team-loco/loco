import subprocess
import sys
from html.parser import HTMLParser
from pathlib import Path
from types import SimpleNamespace
from urllib.parse import unquote, urlsplit

import pytest

DOCS = Path(__file__).resolve().parents[1]
BUILD = DOCS / "scripts" / "build.py"
WEB = DOCS.parent / "web"


class Links(HTMLParser):
    def __init__(self):
        super().__init__()
        self.targets = []

    def handle_starttag(self, tag, attributes):
        for name, value in attributes:
            if name in ["href", "src"] and value:
                self.targets.append(value)


@pytest.fixture(
    scope="module",
    params=[("production", "docs.loco.build"), ("staging", "docs.staging.loco.build")],
    ids=["production", "staging"],
)
def built_site(request):
    environment, host = request.param
    subprocess.run([sys.executable, str(BUILD), "build", environment], check=True)
    built_site = SimpleNamespace()
    built_site.environment = environment
    built_site.host = host
    built_site.root = DOCS / "site"
    built_site.html = (built_site.root / "index.html").read_text()
    return built_site


def test_site_links_to_its_own_host(built_site):
    assert f"https://{built_site.host}/" in built_site.html
    assert f"https://{built_site.host}/" in (built_site.root / "llms.txt").read_text()


def test_site_publishes_markdown_for_agents(built_site):
    assert "Deployment modes" in (built_site.root / "llms-full.txt").read_text()
    assert (built_site.root / "index.md").is_file()


def test_site_uses_the_web_favicon(built_site):
    assert (built_site.root / "assets/favicon.svg").read_text() == (
        WEB / "public/favicon.svg"
    ).read_text()


def test_site_logo_is_the_web_logo(built_site):
    logo = (built_site.root / "assets/logo.svg").read_text()
    assert logo == (WEB / "public/logo.svg").read_text()
    assert "assets/logo.svg" in built_site.html


def test_site_has_a_not_found_page(built_site):
    assert (built_site.root / "404.html").is_file()


def assert_site_links_resolve(site, prefix=""):
    for document in site.rglob("*.html"):
        links = Links()
        links.feed(document.read_text())
        for target in links.targets:
            url = urlsplit(target)
            if url.scheme or url.netloc or (not url.path):
                continue
            path = (
                site / unquote(url.path.removeprefix(prefix).lstrip("/"))
                if url.path.startswith("/")
                else document.parent / unquote(url.path)
            )
            if not path.exists() and path.suffix == "":
                path = path / "index.html"
            assert path.exists(), (
                f"{document.relative_to(site)} links to missing {target}"
            )


def test_site_links_resolve(built_site):
    assert_site_links_resolve(built_site.root)


def test_dashboard_mount_has_prerendered_pages_and_resolving_links(built_site):
    site = DOCS / "site-mounted"
    html = (site / "index.html").read_text()
    assert "Deploy with Loco" in html
    assert 'href="/docs/getting-started/install"' in html
    assert 'src="/docs/assets/logo.svg"' in html
    assert_site_links_resolve(site, "/docs")
    for source in (DOCS / "content").rglob("*.md"):
        relative = source.relative_to(DOCS / "content").with_suffix("")
        path = Path("index.html") if relative.as_posix() == "index" else relative / "index.html"
        assert "<h1" in (built_site.root / path).read_text(), source
        assert "<h1" in (site / path).read_text(), source


def test_only_staging_is_hidden_from_crawlers(built_site):
    robots = (built_site.root / "robots.txt").read_text()
    if built_site.environment == "staging":
        assert "noindex, nofollow" in built_site.html
        assert "Disallow: /" in robots
        assert "Staging documentation" in built_site.html
    else:
        assert "noindex, nofollow" not in built_site.html
        assert "Allow: /" in robots


def test_unknown_environment_is_rejected():
    result = subprocess.run(
        [sys.executable, str(BUILD), "build", "preview"], capture_output=True
    )
    assert result.returncode != 0
