import json
import os
import re
import subprocess
import threading
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest
from playwright.sync_api import expect, sync_playwright
from test_static_hosting import ROOT, navigate, response

PAGES = "https://docs.loco.build"
SITE = ROOT / "docs/site"
CURRENT, PREVIOUS = subprocess.run(
    ["git", "tag", "--list", "v*", "--sort=-v:refname"],
    cwd=ROOT,
    check=True,
    capture_output=True,
    text=True,
).stdout.split()[:2]
VERSIONS = pytest.mark.parametrize(
    "prefix,name",
    [
        ("/", CURRENT),
        (f"/{CURRENT}/", CURRENT),
        (f"/{PREVIOUS}/", PREVIOUS),
        ("/staging/", "staging"),
    ],
)


def build_pages(env=None):
    return subprocess.run(
        ["bun", "docs/build.ts", "pages"],
        cwd=ROOT,
        env=env,
        capture_output=True,
        text=True,
    )


class PagesHandler(SimpleHTTPRequestHandler):
    """Serves files as they are, index.html for directories and 404.html for misses, as Pages does."""

    def send_error(self, code, message=None, explain=None):
        if code != 404:
            return super().send_error(code, message, explain)
        body = (Path(self.directory) / "404.html").read_bytes()
        self.send_response(404)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        pass


@pytest.fixture(scope="module")
def pages():
    result = build_pages()
    assert result.returncode == 0, result.stderr
    server = ThreadingHTTPServer(
        ("127.0.0.1", 0), partial(PagesHandler, directory=SITE)
    )
    threading.Thread(target=server.serve_forever, daemon=True).start()
    yield f"http://127.0.0.1:{server.server_address[1]}"
    server.shutdown()


@pytest.fixture(scope="module")
def browser():
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch()
        yield browser
        browser.close()


@pytest.fixture
def page(browser, pages):
    page = browser.new_page(viewport={"width": 1440, "height": 1024})
    yield page
    page.close()


def open_guide(page, pages, prefix):
    navigate(page, f"{pages}{prefix}getting-started/install/")
    page.wait_for_load_state("networkidle")


def test_versions_list_current_previous_and_staging(pages):
    assert json.loads(response(pages + "/versions.json")[1]) == [
        {"name": CURRENT, "url": "/"},
        {"name": PREVIOUS, "url": f"/{PREVIOUS}/"},
        {"name": "staging", "url": "/staging/"},
    ]


@VERSIONS
def test_version_serves_guides(pages, prefix, name):
    status, body, _ = response(f"{pages}{prefix}getting-started/install/")
    assert status == 200
    assert f'rel="canonical" href="{PAGES}{prefix}getting-started/install/"' in body


@VERSIONS
def test_version_llms_links_stay_in_the_version(pages, prefix, name):
    status, body, _ = response(f"{pages}{prefix}llms.txt")
    assert status == 200
    links = re.findall(r"\]\((https?://[^)]+)\)", body)
    assert links
    assert all(link.startswith(PAGES + prefix) for link in links), links


@VERSIONS
def test_version_search_opens_pages_in_the_version(page, pages, prefix, name):
    open_guide(page, pages, prefix)
    page.get_by_role("button", name=re.compile(r"^Search")).first.click()
    page.get_by_role("combobox", name="Search", exact=True).fill("webhooks")
    page.get_by_role("option").first.click()
    expect(page).to_have_url(re.compile(re.escape(pages + prefix) + r"\w"))


@VERSIONS
def test_version_menu_lists_every_version(page, pages, prefix, name):
    open_guide(page, pages, prefix)
    page.get_by_role("button", name=name, exact=True).click()
    for version, url in [(CURRENT, "/"), (PREVIOUS, f"/{PREVIOUS}/")]:
        link = page.get_by_role("link", name=version, exact=True)
        expect(link).to_have_attribute("href", url)
        expect(link).to_have_attribute("data-active", str(version == name).lower())
    expect(page.get_by_role("link", name="staging", exact=True)).to_have_attribute(
        "href", "/staging/"
    )


def test_only_the_root_has_robots_txt(pages):
    status, robots, _ = response(pages + "/robots.txt")
    assert status == 200
    assert "Allow: /" in robots
    for prefix in [f"/{CURRENT}/", f"/{PREVIOUS}/", "/staging/"]:
        assert response(f"{pages}{prefix}robots.txt")[0] == 404


def test_staging_pages_ask_not_to_be_indexed(pages):
    body = response(pages + "/staging/getting-started/install/")[1]
    assert 'name="robots" content="noindex' in body


@pytest.mark.parametrize("path", ["/foo", f"/{PREVIOUS}/missing/", "/staging/missing/"])
def test_missing_paths_answer_not_found(pages, path):
    status, body, _ = response(pages + path)
    assert status == 404
    assert "Page not found" in body


def test_a_failed_version_fails_the_build(pages, tmp_path):
    # Two tags whose content has no title, which the renderer rejects.
    content = tmp_path / "docs/content"
    content.mkdir(parents=True)
    (content / "index.md").write_text("---\ndescription: no title\n---\n\nBody\n")
    git = ["git", "-C", tmp_path, "-c", "user.name=t", "-c", "user.email=t@t"]
    subprocess.run(["git", "init", "-q", tmp_path], check=True)
    subprocess.run([*git, "add", "."], check=True)
    subprocess.run([*git, "commit", "-qm", "broken"], check=True)
    for tag in ["v0.0.1", "v0.0.2"]:
        subprocess.run([*git, "tag", tag], check=True)
    versions = (SITE / "versions.json").read_text()

    result = build_pages({**os.environ, "GIT_DIR": str(tmp_path / ".git")})

    assert result.returncode != 0
    assert (SITE / "versions.json").read_text() == versions
