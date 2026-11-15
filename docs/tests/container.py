import json
import re
import subprocess
import time
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

import pytest
from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[2]
PORT = 8080
RUNTIME_USER = "65532:65532"
STARTUP_TIMEOUT = 30
POLL_INTERVAL = 0.1
REQUEST_TIMEOUT = 5
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
                        lambda request, label=label: problems.append(
                            f"{label}: {request.url} failed"
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
                    page.goto(f"http://{host}{path}")
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


def check_unknown_path_opens_ui(address, paths):
    # Without a session the UI sends every protected path, including an unknown
    # one, to sign-in. Its NotFound page needs a session, so tests/ui covers it.
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            args=[f"--host-resolver-rules=MAP {UI_HOST} {address}"]
        )
        for path in paths:
            page = browser.new_page()
            page.goto(f"http://{UI_HOST}{path}")
            page.wait_for_url(f"http://{UI_HOST}/login")
            assert page.title() == "Sign in | Loco", path
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
        page.goto(f"http://{UI_HOST}/login")
        issuer = page.evaluate(
            "url => fetch(url).then(reply => reply.json()).then(body => body.issuer)",
            discovery,
        )
        assert issuer == f"http://{HTTP_ISSUER_HOST}/dex"
        browser.close()


def ui_routes():
    route_source = (ROOT / "web/src/App.tsx").read_text()
    return re.findall('<Route\\s+path="(/[^\\"]*)"', route_source)


def check_docs_renders(address, out_dir):
    problems = []
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(args=[
            f"--host-resolver-rules=MAP {UI_HOST} {address}, MAP {DOCS_HOST} {address}"
        ])
        for host, prefix in [(DOCS_HOST, ""), (UI_HOST, "/docs")]:
            for scheme in NOT_FOUND_SCHEMES:
                for narrow in [False, True]:
                    width = 390 if narrow else 1440
                    page = browser.new_page(viewport={"width": width, "height": 1000}, color_scheme=scheme)
                    page.on("pageerror", lambda error: problems.append(str(error)))
                    page.on("console", lambda message: message.type == "error" and problems.append(message.text))
                    page.on("requestfailed", lambda request: problems.append(f"{request.url} failed"))
                    page.goto(f"http://{host}{prefix}/")
                    page.get_by_role("heading", name="Deploy with Loco", exact=True).wait_for()
                    page.wait_for_function("document.documentElement.classList.contains('dark') === " + str(scheme == "dark").lower())
                    assert page.evaluate("document.documentElement.scrollWidth") <= width
                    page.screenshot(path=out_dir / f"{host}-after-{scheme}{'-narrow' if narrow else ''}.png", full_page=True)
                    # Article links and router data must keep the active hosting path.
                    page.get_by_role("link", name="Choose a deployment mode", exact=True).click()
                    page.get_by_role("heading", name="Deployment modes", exact=True).wait_for()
                    assert page.url.startswith(f"http://{host}{prefix}/deployment/modes")
                    page.reload()
                    page.get_by_role("heading", name="Deployment modes", exact=True).wait_for()
                    page.keyboard.press("ControlOrMeta+k")
                    search = page.get_by_role("dialog")
                    search.get_by_role("textbox").fill("loco.yaml")
                    search.get_by_role("link").first.wait_for()
                    search.get_by_role("textbox").fill("zzzznonexistentquery")
                    search.get_by_text("No results found").wait_for()
                    page.keyboard.press("Escape")
                    assert not search.is_visible()
                    page.close()
        browser.close()
    assert problems == []


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
            except (URLError, ConnectionError):
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
        for route in ui_routes():
            path = re.sub(":[^/]+", "test", route)
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
            status, _, asset_headers = response(base + "/assets/logo.svg", host)
            assert status == 200
            assert asset_headers["Cache-Control"] == "no-cache"
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
        check_docs_renders(address, tmp_path)
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
    finally:
        docker("stop", container)
