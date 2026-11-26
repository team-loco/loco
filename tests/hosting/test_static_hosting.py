import json
import re
import subprocess
import time
from html import unescape
from pathlib import Path
from typing import NamedTuple
from unittest.mock import Mock
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

import pytest
from playwright.sync_api import Error, expect, sync_playwright

ROOT = Path(__file__).resolve().parents[2]
PORT = 8080
RUNTIME_USER = "65532:65532"
STARTUP_TIMEOUT = 30
POLL_INTERVAL = 0.1
REQUEST_TIMEOUT = 5
NAVIGATION_ATTEMPTS = 3
NOT_FOUND_TITLE = "Page not found | Loco"
NOT_FOUND_HEADING = "Page not found"
NOT_FOUND_VIEWPORTS = {"desktop": (1440, 900), "mobile": (390, 844)}
NOT_FOUND_SCHEMES = ["light", "dark"]
UNKNOWN_PATH = "/foo"
NOINDEX = '<meta name="robots" content="noindex">'
MISSING_ASSET = "/assets/missing-abc.js"
SCHEMA_PATH = "/schemas/loco.v1.json"
IMAGE = "loco-ui-docs-test"
UI_HOST = "loco.build"
DOCS_URL = "https://docs.loco.build/"
API_HOST = "api.loco.build"
HTTP_ISSUER_HOST = "dex.loco.localhost:5556"
VERSION = "sha-test"
RUNTIME_CONFIG = 'window.locoConfig = {"apiURL":"/api"};\n'
APP_PATHS = [
    "/",
    "/login",
    "/auth/callback",
    "/cli/login",
    "/cli/device",
    "/onboarding",
    "/dashboard",
    "/organizations",
    "/profile",
    "/tokens",
    "/team",
    "/org/test/team",
    "/org/test/audit",
    "/org/test/settings",
    "/org/test/wks/test",
    "/org/test/wks/test/resource/example",
]
DOCS = "/docs"
DOCS_PAGE = f"http://{UI_HOST}{DOCS}/getting-started/install/"


def docker(*args):
    result = subprocess.run(
        ["docker", *args], cwd=ROOT, check=True, capture_output=True, text=True
    )
    return result.stdout.strip()


def response(url, host=None):
    headers = {"Host": host} if host else {}
    request = Request(url, headers=headers)
    try:
        with urlopen(request, timeout=REQUEST_TIMEOUT) as result:
            return (result.status, result.read().decode(), result.headers)
    except HTTPError as error:
        with error:
            return (error.code, error.read().decode(), error.headers)


def navigate(page, url):
    for attempt in range(NAVIGATION_ATTEMPTS):
        try:
            return page.goto(url)
        except Error as error:
            if (
                "net::ERR_NETWORK_CHANGED" not in str(error)
                or attempt == NAVIGATION_ATTEMPTS - 1
            ):
                raise
            time.sleep(POLL_INTERVAL)


@pytest.fixture(scope="module")
def image():
    docker(
        "buildx",
        "build",
        "--load",
        "-t",
        IMAGE,
        "-f",
        "web/Dockerfile",
        "--build-arg",
        f"PUBLIC_API_URL=https://{API_HOST}",
        "--build-arg",
        "PUBLIC_APP_ENV=PRODUCTION",
        "--build-arg",
        f"VERSION={VERSION}",
        ".",
    )
    return IMAGE


@pytest.fixture(scope="module")
def container(image, tmp_path_factory):
    runtime_config = tmp_path_factory.mktemp("runtime") / "runtime-config.js"
    runtime_config.write_text(RUNTIME_CONFIG)
    runtime_config.chmod(0o644)
    # The pod settings charts/loco-control-plane/templates/ui.yaml runs the
    # image with: no capabilities, no privilege escalation and its own
    # runtime-config.js mounted over the image's.
    container = docker(
        "run",
        "--rm",
        "-d",
        "--cap-drop",
        "ALL",
        "--security-opt",
        "no-new-privileges",
        "-v",
        f"{runtime_config}:/public/runtime-config.js:ro",
        "-p",
        f"127.0.0.1::{PORT}",
        image,
    )
    yield container
    docker("stop", container)


@pytest.fixture(scope="module")
def address(container):
    address = docker("port", container, str(PORT)).splitlines()[0]
    deadline = time.monotonic() + STARTUP_TIMEOUT
    while True:
        try:
            if response(f"http://{address}/health")[0] == 200:
                return address
        except URLError, ConnectionError:
            pass
        if time.monotonic() >= deadline:
            pytest.fail(docker("logs", container))
        time.sleep(POLL_INTERVAL)


@pytest.fixture(scope="module")
def url(address):
    return f"http://{address}"


@pytest.fixture(scope="module")
def ui(url):
    return response(url, UI_HOST)


@pytest.fixture(scope="module")
def spa(ui):
    # The splash at / stays indexable; every other path gets the same page with noindex.
    return ui[1].replace("</head>", NOINDEX + "</head>")


@pytest.fixture(scope="module")
def browser(address):
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            args=[f"--host-resolver-rules=MAP {UI_HOST} {address}"]
        )
        yield browser
        browser.close()


@pytest.fixture
def docs_page(browser):
    page = browser.new_page(viewport={"width": 1440, "height": 1024})
    navigate(page, DOCS_PAGE)
    # Controls respond once the page has hydrated.
    page.wait_for_load_state("networkidle")
    yield page
    page.close()


@pytest.fixture
def narrow_docs_page(docs_page, tmp_path):
    # A text-heavy guide must reflow without losing navigation or page actions.
    docs_page.set_viewport_size({"width": 390, "height": 844})
    docs_page.screenshot(path=tmp_path / "docs-install-narrow.png")
    return docs_page


def test_health_answers_ok(url):
    assert response(f"{url}/health")[:2] == (200, "OK")


def test_image_runs_as_the_unprivileged_user(container):
    assert docker("inspect", "--format", "{{.Config.User}}", container) == RUNTIME_USER


def test_ui_serves_the_app_shell(ui):
    status, body, _ = ui
    assert status == 200
    assert '<div id="root">' in body
    assert "Deploy with Loco" not in body


def test_ui_loads_the_runtime_config(ui):
    assert '<script src="/runtime-config.js">' in ui[1]


def test_ui_reports_its_version(ui):
    assert f'<meta name="loco-version" content="{VERSION}">' in ui[1]


def test_ui_splash_is_indexable(ui):
    assert 'name="robots"' not in ui[1]


def test_ui_may_call_the_built_api(ui):
    policy = ui[2]["Content-Security-Policy"]
    assert f"connect-src 'self' https://{API_HOST};" in policy


def test_runtime_config_is_the_mounted_file(url):
    assert response(url + "/runtime-config.js", UI_HOST)[:2] == (200, RUNTIME_CONFIG)


def test_version_json_reports_the_build_version(url):
    status, body, _ = response(url + "/version.json", UI_HOST)
    assert status == 200
    assert json.loads(body) == {"version": VERSION}


@pytest.mark.parametrize("path", ["/runtime-config.js", "/version.json"])
def test_runtime_files_are_revalidated(url, path):
    assert response(url + path, UI_HOST)[2]["Cache-Control"] == "no-cache"


def test_schema_is_the_repository_schema(url):
    status, body, _ = response(url + SCHEMA_PATH, UI_HOST)
    assert status == 200
    assert json.loads(body) == json.loads((ROOT / "schemas/loco.v1.json").read_text())


def test_schema_is_served_as_json(url):
    content_type = response(url + SCHEMA_PATH, UI_HOST)[2]["Content-Type"]
    assert content_type.startswith("application/json"), content_type


@pytest.mark.parametrize("path", APP_PATHS)
def test_app_paths_serve_the_ui(url, ui, spa, path):
    status, body, _ = response(url + path, UI_HOST)
    assert status == 200
    assert body == (ui[1] if path == "/" else spa)


@pytest.mark.parametrize("path", APP_PATHS)
def test_app_paths_ignore_a_trailing_slash(url, ui, spa, path):
    body = response(url + path.rstrip("/") + "/", UI_HOST)[1]
    assert body == (ui[1] if path == "/" else spa)


def test_docs_serve_the_home_page(url):
    status, html, _ = response(url + DOCS + "/", UI_HOST)
    assert status == 200
    assert "Deploy with Loco" in html
    assert DOCS_URL in html


def test_docs_share_the_ui_content_security_policy(url, ui):
    headers = response(url + DOCS + "/", UI_HOST)[2]
    assert headers["Content-Security-Policy"] == ui[2]["Content-Security-Policy"]


@pytest.mark.parametrize("path", ["/", "/dashboard", DOCS + "/"])
def test_pages_refuse_framing(url, path):
    headers = response(url + path, UI_HOST)[2]
    assert headers["X-Frame-Options"] == "DENY"
    assert "frame-ancestors 'none'" in headers["Content-Security-Policy"]


def test_docs_allow_only_hashed_inline_scripts(url):
    policy = response(url + DOCS + "/", UI_HOST)[2]["Content-Security-Policy"]
    assert "script-src 'self' 'unsafe-inline'" not in policy
    assert "'sha256-" in policy


def test_docs_serve_guides(url):
    status, body, _ = response(url + DOCS + "/deployment/modes/", UI_HOST)
    assert status == 200
    assert "Multi-tenant SaaS" in body


def test_docs_logo_is_cached_immutably(url):
    html = response(url + DOCS + "/", UI_HOST)[1]
    logo = re.search(r'mask-image:url\("([^\"]+)"\)', unescape(html)).group(1)
    status, _, headers = response(url + logo, UI_HOST)
    assert status == 200
    assert "immutable" in headers["Cache-Control"]


@pytest.mark.parametrize("prefix", [DOCS, ""], ids=["docs", "root"])
def test_agent_skill_is_served_as_markdown(url, prefix):
    status, skill, headers = response(url + prefix + "/skills.md", UI_HOST)
    assert status == 200
    assert skill == (ROOT / "docs/content/skills.md").read_text()
    assert headers["Content-Type"].startswith("text/markdown")


def test_install_script_is_served_as_text(url):
    status, script, headers = response(url + "/install.sh", UI_HOST)
    assert status == 200
    assert script.startswith("#!")
    assert headers["Content-Type"].startswith("text/plain")


@pytest.mark.parametrize(
    "path", [UNKNOWN_PATH, UNKNOWN_PATH + "/bar", "/dashboard/missing"]
)
def test_unknown_app_paths_serve_the_ui(url, spa, path):
    # The UI renders its own NotFound for these.
    assert response(url + path, UI_HOST)[:2] == (200, spa)


@pytest.mark.parametrize(
    "path",
    [
        MISSING_ASSET,
        "/missing.json",
        DOCS + "/missing-doc-page",
        DOCS + "/deployment/missing/",
    ],
)
def test_missing_files_are_static_not_found_pages(url, path):
    status, body, _ = response(url + path, UI_HOST)
    assert status == 404
    assert f"<title>{NOT_FOUND_TITLE}</title>" in body
    assert '<div id="root">' not in body
    assert NOT_FOUND_HEADING in body


class NotFound(NamedTuple):
    page: object
    width: int
    problems: list


@pytest.fixture(
    params=[
        (path, viewport, scheme)
        for path in [MISSING_ASSET, DOCS + UNKNOWN_PATH]
        for viewport in NOT_FOUND_VIEWPORTS
        for scheme in NOT_FOUND_SCHEMES
    ],
    ids="-".join,
)
def not_found(browser, request):
    path, viewport, scheme = request.param
    width, height = NOT_FOUND_VIEWPORTS[viewport]
    page = browser.new_page(
        viewport={"width": width, "height": height}, color_scheme=scheme
    )
    url = f"http://{UI_HOST}{path}"
    problems = []
    page.on(
        "console",
        lambda message: (
            message.type == "error"
            and message.location["url"] != url
            and problems.append(message.text)
        ),
    )
    page.on(
        "requestfailed",
        lambda request: (
            not (
                request.is_navigation_request()
                and request.failure == "net::ERR_NETWORK_CHANGED"
            )
            and problems.append(f"{request.url} failed")
        ),
    )
    page.on(
        "response",
        lambda reply: (
            reply.request.resource_type != "document"
            and reply.status >= 400
            and problems.append(f"{reply.url} {reply.status}")
        ),
    )
    navigate(page, url)
    yield NotFound(page, width, problems)
    page.close()


def test_not_found_page_loads_without_errors(not_found):
    # The 404 file is served at every depth, so it must load no subresource and
    # break no CSP rule.
    assert not_found.problems == []


def test_not_found_page_shows_the_way_back(not_found, tmp_path):
    page = not_found.page
    assert page.title() == NOT_FOUND_TITLE
    assert page.get_by_text(NOT_FOUND_HEADING).is_visible()
    assert page.get_by_role("link", name="Go to dashboard").is_visible()
    page.screenshot(path=tmp_path / "not-found.png")


def test_not_found_page_fits_the_viewport(not_found):
    scroll_width = not_found.page.evaluate("document.documentElement.scrollWidth")
    assert scroll_width <= not_found.width


@pytest.mark.parametrize("path", [UNKNOWN_PATH, UNKNOWN_PATH + "/bar"])
def test_unknown_paths_send_signed_out_users_to_sign_in(browser, path):
    page = browser.new_page()
    # The image has no API behind /api. Reply as an API would for a signed-out browser.
    page.route(
        "**/api/**",
        lambda route: route.fulfill(status=401, json={"code": "unauthenticated"}),
    )
    errors = []
    page.on(
        "console",
        lambda message: (
            message.type == "error"
            and errors.append(f"{message.location['url']}: {message.text}")
        ),
    )
    page.on("pageerror", lambda error: errors.append(f"Uncaught: {error}"))
    navigate(page, f"http://{UI_HOST}{path}")
    try:
        page.wait_for_url(f"http://{UI_HOST}/login")
    except Error as error:
        console = "\n".join(errors) or "(none)"
        pytest.fail(
            f"{path} stopped at {page.url}\nConsole errors:\n{console}\n{error}"
        )
    expect(page, path).to_have_title("Sign in | Loco")
    page.close()


@pytest.fixture
def dashboard(browser):
    origin = f"http://{UI_HOST}"
    page = browser.new_page()
    navigate(page, origin)
    yield page
    page.close()


def test_dashboard_reaches_its_own_api(dashboard):
    api = f"http://{UI_HOST}/api/readyz"
    dashboard.route(api, lambda route: route.fulfill(body="api available"))
    assert (
        dashboard.evaluate("url => fetch(url).then(reply => reply.text())", api)
        == "api available"
    )


def test_dashboard_connects_to_no_other_origin(dashboard):
    discovery = f"http://{HTTP_ISSUER_HOST}/dex/.well-known/openid-configuration"
    dashboard.route(
        discovery,
        lambda route: route.fulfill(
            json={"issuer": discovery},
            headers={"Access-Control-Allow-Origin": f"http://{UI_HOST}"},
        ),
    )
    assert dashboard.evaluate(
        "url => fetch(url).then(() => false, () => true)", discovery
    )


def test_docs_link_the_api_reference(docs_page):
    expect(docs_page.get_by_role("link", name="API reference").first).to_have_attribute(
        "href", f"https://{API_HOST}"
    )


def test_docs_use_the_geist_fonts(docs_page):
    docs_page.evaluate("document.fonts.ready")
    assert (
        docs_page.locator("body")
        .evaluate("el => getComputedStyle(el).fontFamily")
        .startswith('"Geist Variable"')
    )
    for family in ("Geist Variable", "Geist Mono Variable"):
        assert docs_page.evaluate(
            "family => [...document.fonts].some(font => font.family.replaceAll(/['\"]/g, '') === family && font.status === 'loaded')",
            family,
        ), family


def test_docs_sidebar_starts_at_the_left_edge(docs_page):
    assert docs_page.locator("#nd-sidebar").bounding_box()["x"] == 0


@pytest.mark.parametrize("label", ["API reference", "Dashboard"])
def test_docs_header_links_show_icons(docs_page, label):
    expect(
        docs_page.get_by_role("link", name=label, exact=True).first.locator("svg")
    ).to_be_visible()


def test_docs_theme_buttons_switch_the_theme(docs_page):
    docs_page.get_by_role("button", name="Dark", exact=True).first.click()
    expect(docs_page.locator("html")).to_have_class(re.compile(r"dark"))
    docs_page.get_by_role("button", name="Light", exact=True).first.click()
    expect(docs_page.locator("html")).not_to_have_class(re.compile(r"dark"))


def search(page):
    trigger = page.get_by_role("button", name=re.compile(r"^Search")).first
    trigger.click()
    return trigger, page.get_by_role("combobox", name="Search", exact=True)


def test_docs_search_focuses_its_input(docs_page, tmp_path):
    _, search_input = search(docs_page)
    expect(search_input).to_be_focused()
    expect(search_input).to_have_css("outline-style", "none")
    expect(docs_page.get_by_role("dialog")).to_have_css("outline-style", "solid")
    docs_page.screenshot(path=tmp_path / "docs-search-light.png", animations="disabled")


def test_docs_search_finds_pages(docs_page):
    _, search_input = search(docs_page)
    search_input.fill("install")
    expect(docs_page.get_by_role("option").first).to_be_visible()


def test_docs_search_returns_focus_when_closed(docs_page):
    trigger, search_input = search(docs_page)
    search_input.press("Escape")
    expect(search_input).to_be_hidden()
    expect(trigger).to_be_focused()


def test_docs_article_keeps_a_readable_width(docs_page, tmp_path):
    assert docs_page.locator("#nd-page").bounding_box()["width"] <= 780
    docs_page.screenshot(path=tmp_path / "docs-install-light.png")


def test_docs_logo_fits_the_header(docs_page):
    box = docs_page.get_by_role("img", name="Loco", exact=True).first.bounding_box()
    assert box["width"] <= 64
    assert box["height"] <= 32


def test_docs_follow_the_dark_color_scheme(docs_page, tmp_path):
    docs_page.emulate_media(color_scheme="dark")
    expect(docs_page.locator("html")).to_have_class(re.compile(r"dark"))
    docs_page.screenshot(path=tmp_path / "docs-install-dark.png")


def test_narrow_docs_collapse_the_sidebar_column(narrow_docs_page):
    expect(narrow_docs_page.locator("#nd-notebook-layout")).to_have_css(
        "--fd-sidebar-col", "0px"
    )


def test_narrow_docs_do_not_scroll_sideways(narrow_docs_page):
    assert narrow_docs_page.evaluate(
        "document.documentElement.scrollWidth <= window.innerWidth"
    )


def test_narrow_docs_article_uses_the_width(narrow_docs_page):
    # The article widens as the layout reflows to the new viewport.
    narrow_docs_page.wait_for_function(
        "() => document.querySelector('#nd-page').getBoundingClientRect().width >= 350"
    )


def test_narrow_docs_keep_the_heading_and_page_actions(narrow_docs_page):
    expect(
        narrow_docs_page.get_by_role("heading", name="Install the CLI", exact=True)
    ).to_be_visible()
    expect(
        narrow_docs_page.get_by_role("link", name="View Markdown", exact=True)
    ).to_be_visible()


def test_narrow_docs_sidebar_navigates(narrow_docs_page):
    narrow_docs_page.get_by_role("button", name="Open Sidebar", exact=True).click()
    expect(
        narrow_docs_page.get_by_role("link", name="API reference").last
    ).to_be_visible()
    narrow_docs_page.get_by_role(
        "link", name="Connect to Loco", exact=True
    ).last.click()
    expect(narrow_docs_page).to_have_url(re.compile(r"/getting-started/connect/?$"))
    expect(
        narrow_docs_page.get_by_role("heading", name="Connect to Loco", exact=True)
    ).to_be_visible()


@pytest.mark.parametrize("failures", [1, NAVIGATION_ATTEMPTS])
def test_navigation_retries_network_changes(failures, monkeypatch):
    failure = Error("Page.goto: net::ERR_NETWORK_CHANGED")
    page = Mock()
    page.goto.side_effect = [failure] * failures + ["response"]
    monkeypatch.setattr(time, "sleep", lambda _: None)
    if failures == NAVIGATION_ATTEMPTS:
        with pytest.raises(Error) as caught:
            navigate(page, "http://docs.test/foo")
        assert caught.value is failure
    else:
        assert navigate(page, "http://docs.test/foo") == "response"
    assert page.goto.call_count == min(failures + 1, NAVIGATION_ATTEMPTS)


def test_navigation_preserves_other_errors():
    failure = Error("Page.goto: net::ERR_CONNECTION_REFUSED")
    page = Mock()
    page.goto.side_effect = failure
    with pytest.raises(Error) as caught:
        navigate(page, "http://docs.test/foo")
    assert caught.value is failure
