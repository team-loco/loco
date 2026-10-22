import re
import subprocess
import sys
import tomllib
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urlsplit

DOCS = Path(__file__).resolve().parents[1]
BUILD = DOCS / "scripts" / "build.py"


class Links(HTMLParser):
    def __init__(self):
        super().__init__()
        self.targets = []

    def handle_starttag(self, tag, attributes):
        for name, value in attributes:
            if name in ["href", "src"] and value:
                self.targets.append(value)


def test_environment_builds(subtests):
    for environment, host in [
        ("production", "docs.loco.build"),
        ("staging", "docs.staging.loco.build"),
    ]:
        with subtests.test(environment=environment):
            subprocess.run(
                [sys.executable, str(BUILD), "build", environment], check=True
            )
            site = DOCS / "site"
            html = (site / "index.html").read_text()
            assert f"https://{host}/" in html
            assert "md-sidebar--primary" in html
            assert "Copy as Markdown" in html
            assert f"https://{host}/" in (site / "llms.txt").read_text()
            assert "Deployment modes" in (site / "llms-full.txt").read_text()
            assert (site / "index.md").is_file()
            web = DOCS.parent / "web"
            assert (site / "assets/favicon.svg").read_text() == (
                web / "public/favicon.svg"
            ).read_text()
            strokes = re.findall(
                '\\["([^"]+)", ([\\d.]+)\\]',
                (web / "src/components/design/logo-strokes.ts").read_text(),
            )
            logo = (DOCS / "overrides/partials/logo.html").read_text()
            for path, width in strokes:
                assert f'<path d="{path}" stroke-width="{width}"/>' in logo
            assert logo.count("<path ") == len(strokes)
            assert logo.strip() in html
            assert (site / "404.html").is_file()
            for document in site.rglob("*.html"):
                links = Links()
                links.feed(document.read_text())
                for target in links.targets:
                    url = urlsplit(target)
                    if url.scheme or url.netloc or (not url.path):
                        continue
                    path = (
                        site / unquote(url.path.lstrip("/"))
                        if url.path.startswith("/")
                        else document.parent / unquote(url.path)
                    )
                    assert path.exists(), (
                        f"{document.relative_to(site)} links to missing {target}"
                    )
            assert not (site / "notes.html").exists()
            assert not (site / "checklist.html").exists()
            assert not (site / "tdd").exists()
            assert not (site / "pyproject.toml").exists()
            if environment == "staging":
                assert "noindex, nofollow" in html
                assert "Disallow: /" in (site / "robots.txt").read_text()
                assert "Staging documentation" in html
            else:
                assert "noindex, nofollow" not in html
                assert "Allow: /" in (site / "robots.txt").read_text()


def test_navigation_stays_expanded():
    config = tomllib.loads((DOCS / "zensical.toml").read_text())
    features = config["project"]["theme"]["features"]
    assert "navigation.expand" in features
    assert "navigation.prune" not in features
    assert "navigation.tabs" not in features
    assert "header.autohide" not in features
    for page in (DOCS / "content").rglob("*.md"):
        assert "hide:" not in page.read_text(), str(page)


def test_unknown_environment_is_rejected():
    result = subprocess.run(
        [sys.executable, str(BUILD), "build", "preview"], capture_output=True
    )
    assert result.returncode != 0
