import argparse
import json
import re
import subprocess
import tempfile
from pathlib import Path
from urllib.request import urlopen

from harness import evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Sign in and refresh through an issuer without CORS"
)
args = parser.parse_args()
args.state = None
args.keep = False

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

        api_env = evidence.api_env

        def issuer_env(*ports):
            env = api_env(*ports)
            env["AUTH_ISSUERS"] = json.dumps(
                [
                    {
                        "issuer": issuer_url,
                        "audience": "loco",
                        "web": {"clientId": "loco", "scopes": "openid email profile"},
                    }
                ]
            )
            return env

        evidence.api_env = issuer_env
        state = load_state(
            args, "oidc", "tests/ui/fixtures/deployment-history/scenario.json"
        )
        owner = state["users"]["owner"]
        evidence.psql(
            state["container"],
            f"""INSERT INTO identities
            (user_id, issuer, subject, email, email_verified)
            VALUES ('{owner["id"]}', {evidence.sql_literal(issuer_url)},
                    'browser-owner', {evidence.sql_literal(owner["email"])}, true);""",
        )
        with session(repo / "tests/ui/out/oidc") as browser:
            context, page, errors = open_page(browser, user=None, state=state)
            requests = []
            page.on(
                "request",
                lambda request: requests.append((request.url, request.resource_type)),
            )
            page.goto(state["ui_url"] + "/login")
            expect(page).to_have_url(
                re.compile(
                    re.escape(state["ui_url"]) + r"/org/[a-f0-9-]+/wks/[a-f0-9-]+$"
                ),
                timeout=20000,
            )
            expect(
                page.get_by_role("link", name="Dashboard", exact=True)
            ).to_be_visible()
            tokens = json.loads(
                page.evaluate("localStorage.getItem('loco:auth:oidc:v1')")
            )
            user = evidence.rpc(
                state["api_url"],
                tokens["idToken"],
                "loco.user.v1.UserService/WhoAmI",
                {},
            )["user"]
            assert user["id"] == state["users"]["owner"]["id"], user
            page.evaluate("""() => {
              const tokens = JSON.parse(localStorage.getItem('loco:auth:oidc:v1'));
              tokens.expiresAt = 0;
              localStorage.setItem('loco:auth:oidc:v1', JSON.stringify(tokens));
            }""")
            page.reload()
            expect(
                page.get_by_role("link", name="Dashboard", exact=True)
            ).to_be_visible()
            page.wait_for_function(
                """previous => {
              const tokens = JSON.parse(localStorage.getItem('loco:auth:oidc:v1'));
              return tokens && tokens.refreshToken !== previous;
            }""",
                arg=tokens["refreshToken"],
            )
            with urlopen(issuer_url + "/grants") as response:
                assert json.load(response) == ["authorization_code", "refresh_token"]
            upstream = [
                (url, kind) for url, kind in requests if url.startswith(issuer_url)
            ]
            assert len(upstream) == 1, upstream
            assert upstream[0][1] == "document" and upstream[0][0].startswith(
                issuer_url + "/authorize?"
            ), upstream
            browser_fetches = [
                url for url, kind in requests if kind in ("fetch", "xhr")
            ]
            assert all(
                url.startswith(state["ui_url"] + "/") for url in browser_fetches
            ), browser_fetches
            assert any("/api/auth/oidc/token" in url for url in browser_fetches), (
                browser_fetches
            )
            response = page.request.get(state["ui_url"])
            assert "connect-src 'self';" in response.headers["content-security-policy"]
            assert not errors, errors
            context.close()
    finally:
        issuer.terminate()
        issuer.wait(timeout=10)

print(
    "OIDC sign-in and refresh passed without issuer CORS; browser fetches stay on the dashboard origin"
)
