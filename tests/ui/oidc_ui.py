import argparse
import json
import re
import subprocess
import tempfile
from pathlib import Path
from urllib.parse import parse_qs, urlparse
from urllib.request import urlopen

from harness import evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Sign in through an issuer without CORS and refresh the dashboard session"
)
parser.add_argument("--screenshots", type=Path)
args = parser.parse_args()
args.state = None


def cookies(context, state):
    return {cookie["name"]: cookie for cookie in context.cookies(state["ui_url"])}


def check_browser(browser, state, issuer_url):
    context, page, errors = open_page(browser, user=None, state=state)
    requests = []
    context.on(
        "request",
        lambda request: requests.append((request.url, request.resource_type)),
    )
    page.goto(state["ui_url"] + "/login")
    expect(page).to_have_url(
        re.compile(re.escape(state["ui_url"]) + r"/org/[a-f0-9-]+/wks/[a-f0-9-]+$"),
        timeout=20000,
    )
    dashboard = page.url
    expect(page.get_by_role("link", name="Dashboard", exact=True)).to_be_visible()

    previous = cookies(context, state)
    for name, same_site in (
        (evidence.SESSION_COOKIE, "Lax"),
        (evidence.REFRESH_COOKIE, "Strict"),
    ):
        cookie = previous[name]
        assert cookie["httpOnly"] and cookie["secure"] and cookie["path"] == "/"
        assert cookie["sameSite"] == same_site
        assert cookie["expires"] > 0
    assert "__Host-loco_login" not in previous
    assert page.evaluate(
        """() => {
            const keys = [...Object.keys(localStorage), ...Object.keys(sessionStorage)];
            return keys.every(key => !key.startsWith('loco:auth:')) &&
                JSON.stringify([localStorage, sessionStorage]).indexOf('unused-provider-refresh') === -1 &&
                document.cookie === '';
        }"""
    )

    access = previous[evidence.SESSION_COOKIE]["value"]
    user = evidence.rpc(
        state["api_url"], access, "loco.user.v1.UserService/WhoAmI", {}
    )["user"]
    assert user["id"] == state["users"]["owner"]["id"], user
    access_hash = evidence.token_hash(access)
    evidence.psql(
        state["container"],
        f"""UPDATE session_tokens SET access_expires_at = now() - interval '1 second'
        WHERE access_token_hash = {evidence.sql_literal(access_hash)};""",
    )
    other_tab = context.new_page()
    refreshes = []
    context.on(
        "response",
        lambda response: (
            refreshes.append(response.status)
            if response.url.endswith("/loco.auth.v1.AuthService/RefreshWebSession")
            else None
        ),
    )
    page.reload(wait_until="commit")
    other_tab.goto(dashboard, wait_until="commit")
    for tab in (page, other_tab):
        expect(tab.get_by_role("link", name="Dashboard", exact=True)).to_be_visible()
    fresh = cookies(context, state)
    for name in (evidence.SESSION_COOKIE, evidence.REFRESH_COOKIE):
        assert fresh[name]["value"] != previous[name]["value"]
    assert refreshes == [200], refreshes
    other_tab.close()

    with urlopen(issuer_url + "/grants") as response:
        assert json.load(response) == ["authorization_code"]
    upstream = [(url, kind) for url, kind in requests if url.startswith(issuer_url)]
    assert len(upstream) == 1, upstream
    assert upstream[0][1] == "document" and upstream[0][0].startswith(
        issuer_url + "/authorize?"
    )
    browser_fetches = [url for url, kind in requests if kind in ("fetch", "xhr")]
    assert all(url.startswith(state["ui_url"] + "/api/") for url in browser_fetches)
    response = page.request.get(state["ui_url"])
    assert "connect-src 'self';" in response.headers["content-security-policy"]

    if args.screenshots is not None:
        args.screenshots.mkdir(parents=True, exist_ok=True)
        for theme in evidence.THEMES:
            page.emulate_media(color_scheme=theme)
            page.evaluate("(key) => localStorage.removeItem(key)", evidence.MODE_KEY)
            page.reload()
            expect(
                page.get_by_role("link", name="Dashboard", exact=True)
            ).to_be_visible()
            page.wait_for_function(
                "dark => document.documentElement.classList.contains('dark') === dark",
                arg=theme == "dark",
            )
            evidence.settle(page, wait_for_network=False)
            page.screenshot(
                path=args.screenshots / f"sign-in-after-{theme}.png",
                animations="disabled",
            )

    # An invitation can require a fresh provider login even with an active session.
    page.goto(state["ui_url"] + "/login?login_hint=owner%40acme.test")
    invite = page.get_by_role("button", name="Continue as owner@acme.test")
    expect(invite).to_be_visible()
    with page.expect_request(
        lambda request: request.url.startswith(issuer_url + "/authorize?")
    ) as authorization:
        invite.click()
    assert parse_qs(urlparse(authorization.value.url).query)["login_hint"] == [
        "owner@acme.test"
    ]
    expect(page.get_by_role("link", name="Dashboard", exact=True)).to_be_visible()
    assert not errors, errors
    context.close()


with tempfile.TemporaryDirectory(prefix="loco-oidc-issuer-") as directory:
    binary = Path(directory) / "issuer"
    evidence.run(
        [
            "mise",
            "exec",
            "--",
            "go",
            "build",
            "-o",
            str(binary),
            "./auth/testdata/oidc",
        ],
        cwd=repo / "api",
    )
    issuer = subprocess.Popen([str(binary)], stdout=subprocess.PIPE, text=True)
    try:
        issuer_url = issuer.stdout.readline().strip()
        assert issuer_url.startswith("http://127.0.0.1:"), issuer_url
        with urlopen(issuer_url + "/.well-known/openid-configuration") as response:
            assert response.headers.get("Access-Control-Allow-Origin") is None
        state = load_state(
            args,
            "oidc",
            "tests/ui/fixtures/deployment-history/scenario.json",
            issuer=issuer_url,
        )
        owner = state["users"]["owner"]
        evidence.psql(
            state["container"],
            "UPDATE identities SET subject = 'browser-owner' "
            f"WHERE user_id = {evidence.sql_literal(owner['id'])};",
        )
        with session(repo / "tests/ui/out/oidc") as browser:
            check_browser(browser, state, issuer_url)
    finally:
        issuer.terminate()
        issuer.wait(timeout=10)

print("Dashboard sign-in and concurrent refresh passed with a no-CORS issuer")
