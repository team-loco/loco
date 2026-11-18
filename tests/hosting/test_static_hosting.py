import json
import re
import subprocess
import time
from html import unescape
from pathlib import Path
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
NOT_FOUND_HEADING = "Nothing lives at this address"
NOT_FOUND_VIEWPORTS = {"desktop": (1440, 900), "mobile": (390, 844)}
NOT_FOUND_SCHEMES = ["light", "dark"]
UNKNOWN_PATH = "/foo"
NOINDEX = '<meta name="robots" content="noindex">'
MISSING_ASSET = "/assets/missing-abc.js"
SCHEMA_PATH = "/schemas/loco.v1.json"
IMAGE = "loco-ui-docs-test"
UI_HOST = "loco.build"
DOCS_HOST = "docs.loco.build"
API_HOST = "api.loco.build"
HTTP_ISSUER_HOST = "dex.loco.localhost:5556"
VERSION = "sha-test"
RUNTIME_CONFIG = 'window.locoConfig = {"apiURL":"/api"};\n'


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


def check_not_found_renders(address, pages, out_dir):
    # The 404 file is served for every host, so it must load no subresource (the
    # docs host has no /assets) and break no CSP rule.
    problems = []
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            args=[
                "--host-resolver-rules="
                + ", ".join(f"MAP {host} {address}" for host in {p[0] for p in pages})
            ]
        )
        for host, path in pages:
            for viewport, size in NOT_FOUND_VIEWPORTS.items():
                for scheme in NOT_FOUND_SCHEMES:
                    page = browser.new_page(
                        viewport={"width": size[0], "height": size[1]},
                        color_scheme=scheme,
                    )
                    label = f"{host}{path} {viewport} {scheme}"
                    page.on(
                        "console",
                        lambda message, label=label, url=f"http://{host}{path}": (
                            message.type == "error"
                            and message.location["url"] != url
                            and problems.append(f"{label}: {message.text}")
                        ),
                    )
                    page.on(
                        "requestfailed",
                        lambda request, label=label: (
                            not (
                                request.is_navigation_request()
                                and request.failure == "net::ERR_NETWORK_CHANGED"
                            )
                            and problems.append(f"{label}: {request.url} failed")
                        ),
                    )
                    page.on(
                        "response",
                        lambda reply, label=label: (
                            reply.request.resource_type != "document"
                            and reply.status >= 400
                            and problems.append(f"{label}: {reply.url} {reply.status}")
                        ),
                    )
                    navigate(page, f"http://{host}{path}")
                    assert page.title() == NOT_FOUND_TITLE, label
                    assert page.get_by_text(NOT_FOUND_HEADING).is_visible(), label
                    assert page.get_by_role("link", name="Go to dashboard").is_visible()
                    assert (
                        page.evaluate("document.documentElement.scrollWidth") <= size[0]
                    ), label
                    page.screenshot(
                        path=out_dir
                        / f"{host}{path.replace('/', '_')}-{viewport}-{scheme}.png"
                    )
                    page.close()
        browser.close()
    assert problems == []


def check_path_opens_ui(page, path):
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


def check_unknown_path_opens_ui(address, paths):
    # Without a session the UI sends every protected path, including an unknown
    # one, to sign-in. Its NotFound page needs a session, so tests/ui covers it.
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            args=[f"--host-resolver-rules=MAP {UI_HOST} {address}"]
        )
        for path in paths:
            page = browser.new_page()
            check_path_opens_ui(page, path)
            page.close()
        browser.close()


def check_dashboard_reaches_http_issuer(address):
    # Local and lab installs run Dex over plain HTTP on its own origin, and the
    # dashboard fetches the issuer's discovery document from the browser.
    discovery = f"http://{HTTP_ISSUER_HOST}/dex/.well-known/openid-configuration"
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            args=[f"--host-resolver-rules=MAP {UI_HOST} {address}"]
        )
        page = browser.new_page()
        page.route(
            discovery,
            lambda route: route.fulfill(
                json={"issuer": f"http://{HTTP_ISSUER_HOST}/dex"},
                headers={"Access-Control-Allow-Origin": f"http://{UI_HOST}"},
            ),
        )
        navigate(page, f"http://{UI_HOST}/login")
        issuer = page.evaluate(
            "url => fetch(url).then(reply => reply.json()).then(body => body.issuer)",
            discovery,
        )
        assert issuer == f"http://{HTTP_ISSUER_HOST}/dex"
        browser.close()


def check_docs_reading_layout(address, out_dir):
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            args=[f"--host-resolver-rules=MAP {DOCS_HOST} {address}"]
        )
        page = browser.new_page(viewport={"width": 1440, "height": 1024})
        navigate(page, f"http://{DOCS_HOST}/getting-started/install/")
        expect(page.get_by_role("link", name="API reference").first).to_have_attribute(
            "href", f"https://{API_HOST}"
        )
        page.evaluate("document.fonts.ready")
        assert (
            page.locator("body")
            .evaluate("el => getComputedStyle(el).fontFamily")
            .startswith('"Geist Variable"')
        )
        for family in ("Geist Variable", "Geist Mono Variable"):
            assert page.evaluate(
                "family => [...document.fonts].some(font => font.family.replaceAll(/['\"]/g, '') === family && font.status === 'loaded')",
                family,
            )
        assert page.locator("#nd-sidebar").bounding_box()["x"] == 0
        for label in ("API reference", "Dashboard"):
            expect(
                page.get_by_role("link", name=label, exact=True).first.locator("svg")
            ).to_be_visible()
        page.get_by_role("button", name="Dark", exact=True).first.click()
        expect(page.locator("html")).to_have_class(re.compile(r"dark"))
        page.get_by_role("button", name="Light", exact=True).first.click()
        expect(page.locator("html")).not_to_have_class(re.compile(r"dark"))
        page.get_by_role("button", name="System", exact=True).first.click()
        search_trigger = page.get_by_role("button", name=re.compile(r"^Search")).first
        search_trigger.click()
        search_input = page.get_by_role("combobox", name="Search", exact=True)
        expect(search_input).to_be_focused()
        expect(search_input).to_have_css("outline-style", "none")
        search_dialog = page.get_by_role("dialog")
        expect(search_dialog).to_have_css("outline-style", "solid")
        page.screenshot(path=out_dir / "docs-search-light.png", animations="disabled")
        search_input.fill("install")
        expect(page.get_by_role("option").first).to_be_visible()
        search_input.press("Escape")
        expect(search_input).to_be_hidden()
        expect(search_trigger).to_be_focused()
        article = page.locator("#nd-page")
        assert article.bounding_box()["width"] <= 780
        logo = page.get_by_role("img", name="Loco", exact=True).first
        assert logo.bounding_box()["width"] <= 64
        assert logo.bounding_box()["height"] <= 32
        page.screenshot(path=out_dir / "docs-install-light.png")
        page.emulate_media(color_scheme="dark")
        expect(page.locator("html")).to_have_class(re.compile(r"dark"))
        page.screenshot(path=out_dir / "docs-install-dark.png")
        # A text-heavy guide must reflow without losing navigation or page actions.
        page.set_viewport_size({"width": 390, "height": 844})
        expect(page.locator("#nd-notebook-layout")).to_have_css(
            "--fd-sidebar-col", "0px"
        )
        page.screenshot(path=out_dir / "docs-install-narrow.png")
        assert page.evaluate(
            "document.documentElement.scrollWidth <= window.innerWidth"
        )
        assert article.bounding_box()["width"] >= 350
        expect(
            page.get_by_role("heading", name="Install the CLI", exact=True)
        ).to_be_visible()
        expect(
            page.get_by_role("link", name="View Markdown", exact=True)
        ).to_be_visible()
        page.get_by_role("button", name="Open Sidebar", exact=True).click()
        expect(page.get_by_role("link", name="API reference").last).to_be_visible()
        page.get_by_role("link", name="Connect to Loco", exact=True).last.click()
        expect(page).to_have_url(re.compile(r"/getting-started/connect/?$"))
        expect(
            page.get_by_role("heading", name="Connect to Loco", exact=True)
        ).to_be_visible()
        browser.close()


def test_image_serves_the_ui_and_docs(tmp_path):
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
    runtime_config = tmp_path / "runtime-config.js"
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
        IMAGE,
    )
    try:
        address = docker("port", container, str(PORT)).splitlines()[0]
        url = f"http://{address}"
        deadline = time.monotonic() + STARTUP_TIMEOUT
        while True:
            try:
                status, body, _ = response(f"{url}/health")
                if status == 200:
                    break
            except URLError, ConnectionError:
                pass
            if time.monotonic() >= deadline:
                pytest.fail(docker("logs", container))
            time.sleep(POLL_INTERVAL)
        assert body == "OK"
        assert (
            docker("inspect", "--format", "{{.Config.User}}", container) == RUNTIME_USER
        )
        status, ui, ui_headers = response(url, UI_HOST)
        assert status == 200
        assert '<div id="root">' in ui
        assert '<script src="/runtime-config.js">' in ui
        assert "Deploy with Loco" not in ui
        assert f'<meta name="loco-version" content="{VERSION}">' in ui
        # The splash at / stays indexable; every other path gets the same page with noindex.
        assert 'name="robots"' not in ui
        spa = ui.replace("</head>", NOINDEX + "</head>")
        status, body, config_headers = response(url + "/runtime-config.js", UI_HOST)
        assert status == 200
        assert body == RUNTIME_CONFIG
        assert config_headers["Cache-Control"] == "no-cache"
        status, body, version_headers = response(url + "/version.json", UI_HOST)
        assert status == 200
        assert json.loads(body) == {"version": VERSION}
        assert version_headers["Cache-Control"] == "no-cache"
        status, body, schema_headers = response(url + SCHEMA_PATH, UI_HOST)
        assert status == 200
        assert json.loads(body) == json.loads(
            (ROOT / "schemas/loco.v1.json").read_text()
        )
        assert schema_headers["Content-Type"].startswith("application/json"), (
            schema_headers["Content-Type"]
        )
        for path in (
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
        ):
            status, body, _ = response(url + path, UI_HOST)
            assert status == 200, path
            page = ui if path == "/" else spa
            assert body == page, path
            assert response(url + path.rstrip("/") + "/", UI_HOST)[1] == page, path
        for host, prefix in [(DOCS_HOST, ""), (UI_HOST, "/docs")]:
            base = url + prefix
            status, html, headers = response(base + "/", host)
            assert status == 200
            assert "Deploy with Loco" in html
            assert f"https://{DOCS_HOST}/" in html
            assert (
                headers["Content-Security-Policy"]
                == ui_headers["Content-Security-Policy"]
            )
            assert (
                "script-src 'self' 'unsafe-inline'"
                not in headers["Content-Security-Policy"]
            )
            assert "'sha256-" in headers["Content-Security-Policy"]
            status, body, _ = response(base + "/deployment/modes/", host)
            assert status == 200
            assert "Multi-tenant SaaS" in body
            logo = re.search(r'mask-image:url\("([^\"]+)"\)', unescape(html)).group(1)
            status, _, asset_headers = response(url + logo, host)
            assert status == 200
            assert "immutable" in asset_headers["Cache-Control"]
            status, skill, skill_headers = response(base + "/skills.md", host)
            assert status == 200
            assert skill == (ROOT / "docs/content/skills.md").read_text()
            assert skill_headers["Content-Type"].startswith("text/markdown")
        status, skill, skill_headers = response(url + "/skills.md", UI_HOST)
        assert status == 200
        assert skill == (ROOT / "docs/content/skills.md").read_text()
        assert skill_headers["Content-Type"].startswith("text/markdown")
        status, script, install_headers = response(url + "/install.sh", UI_HOST)
        assert status == 200
        assert script.startswith("#!")
        assert install_headers["Content-Type"].startswith("text/plain")
        # Unknown app paths open the UI, which renders its own NotFound.
        for path in [UNKNOWN_PATH, UNKNOWN_PATH + "/bar", "/dashboard/missing"]:
            status, body, _ = response(url + path, UI_HOST)
            assert status == 200, path
            assert body == spa, path
        # Missing files and unknown documentation paths are real 404s with the
        # static page, never the UI.
        for host, path in [
            (UI_HOST, MISSING_ASSET),
            (UI_HOST, "/missing.json"),
            (UI_HOST, "/docs/missing-doc-page"),
            (UI_HOST, "/docs/deployment/missing/"),
            (DOCS_HOST, UNKNOWN_PATH),
            (DOCS_HOST, "/deployment/missing/"),
            (DOCS_HOST, MISSING_ASSET),
        ]:
            status, body, _ = response(url + path, host)
            assert status == 404, f"{host}{path}"
            assert f"<title>{NOT_FOUND_TITLE}</title>" in body
            assert '<div id="root">' not in body
            assert NOT_FOUND_HEADING in body
        check_not_found_renders(
            address,
            [
                (UI_HOST, MISSING_ASSET),
                (DOCS_HOST, UNKNOWN_PATH),
            ],
            tmp_path,
        )
        check_unknown_path_opens_ui(address, [UNKNOWN_PATH, UNKNOWN_PATH + "/bar"])
        check_dashboard_reaches_http_issuer(address)
        check_docs_reading_layout(address, tmp_path)
    finally:
        docker("stop", container)


@pytest.mark.parametrize("failures", [1, NAVIGATION_ATTEMPTS])
def test_navigation_retries_network_changes(failures, monkeypatch):
    from unittest.mock import Mock

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
    from unittest.mock import Mock

    failure = Error("Page.goto: net::ERR_CONNECTION_REFUSED")
    page = Mock()
    page.goto.side_effect = failure
    with pytest.raises(Error) as caught:
        navigate(page, "http://docs.test/foo")
    assert caught.value is failure
    page.goto.assert_called_once_with("http://docs.test/foo")
