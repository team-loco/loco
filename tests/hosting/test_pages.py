import json
import os
import re
import shutil
import subprocess
import threading
from functools import partial
from html import unescape
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest
from playwright.sync_api import expect, sync_playwright
from test_static_hosting import ROOT, navigate, response

PAGES = "https://docs.loco.build"
SITE = ROOT / "docs/site"


def git(*args):
    return subprocess.run(
        ["git", *args], cwd=ROOT, check=True, capture_output=True, text=True
    ).stdout


CURRENT, PREVIOUS = git("tag", "--list", "v*", "--sort=-v:refname").split()[:2]
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


def distinct_passage(tag, other):
    """Returns a page of tag's content and a passage of its prose that other's content lacks."""
    paths = git("ls-tree", "-r", "--name-only", f"{tag}:docs/content").split()
    for path in (path for path in paths if path.endswith(".md")):
        theirs = subprocess.run(
            ["git", "show", f"{other}:docs/content/{path}"],
            cwd=ROOT,
            capture_output=True,
            text=True,
        ).stdout
        code = False
        for line in git("show", f"{tag}:docs/content/{path}").splitlines():
            code ^= line.startswith("```")
            if code or line.startswith(("---", "title:")):
                continue
            for fragment in re.split(r"[`\[\]()*_|#>]", line):
                fragment = fragment.strip(" -.")
                if len(fragment) >= 30 and fragment not in theirs:
                    slug = re.sub(r"(^|/)index$", "", path.removesuffix(".md"))
                    return f"{slug}/" if slug else "", fragment
    pytest.skip(f"{tag} and {other} have the same content")


@pytest.mark.parametrize(
    "tag,other", [(CURRENT, PREVIOUS), (PREVIOUS, CURRENT)], ids=["current", "previous"]
)
def test_tag_paths_render_their_own_content(pages, tag, other):
    page, passage = distinct_passage(tag, other)
    status, body, _ = response(f"{pages}/{tag}/{page}")
    assert status == 200
    assert passage in unescape(body)


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
    page.get_by_role("option").filter(has_text=re.compile(r"Webhooks$")).first.click()
    expect(page).to_have_url(
        re.compile(re.escape(f"{pages}{prefix}operations/webhooks") + "/?$")
    )
    expect(page.get_by_role("heading", level=1)).to_have_text("Webhooks")


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


def test_a_failed_previous_version_fails_the_build(pages, tmp_path):
    # The previous tag's content has no title, which the renderer rejects; the latest is valid.
    content = tmp_path / "docs/content"
    content.mkdir(parents=True)
    (content / "index.md").write_text("---\ndescription: no title\n---\n\nBody\n")
    repo = ["git", "-C", tmp_path, "-c", "user.name=t", "-c", "user.email=t@t"]

    def tag(name):
        subprocess.run([*repo, "add", "-A"], check=True)
        subprocess.run([*repo, "commit", "-qm", name], check=True)
        subprocess.run([*repo, "tag", name], check=True)

    subprocess.run(["git", "init", "-q", tmp_path], check=True)
    tag("v0.0.1")
    shutil.rmtree(content)
    shutil.copytree(ROOT / "docs/content", content)
    tag("v0.0.2")
    versions = (SITE / "versions.json").read_text()

    result = build_pages({**os.environ, "GIT_DIR": str(tmp_path / ".git")})

    assert result.returncode != 0
    assert (SITE / "versions.json").read_text() == versions
