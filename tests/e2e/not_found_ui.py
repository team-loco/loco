import argparse
import atexit
import base64
import contextlib
import importlib.util
import io
import json
import os
import re
import time
import uuid
from pathlib import Path
from urllib.parse import parse_qs, urlencode, urlparse

from playwright.sync_api import expect, sync_playwright

repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description="Test the not-found page for signed-in and signed-out visitors against an isolated API")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/e2e/out/not-found"))
args = parser.parse_args()
os.chdir(repo)
module_spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(module_spec)
module_spec.loader.exec_module(evidence)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="not-found", scenario=str(repo / "tests/e2e/fixtures/deployment-history/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
ui = state["ui_url"]
shared = state["workspaces"]["acme/shared"]
sibling = state["workspaces"]["acme/sibling"]
org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{shared}';")
base = f"{ui}/org/{org}/wks/{shared}"
issuer = f"{ui}/dex"
TITLE = "This page doesn’t exist"


def open_page(browser, user, theme="light", narrow=False):
    context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme)
    script = f"localStorage.setItem({json.dumps(evidence.MODE_KEY)},{json.dumps(theme)});"
    if user is not None:
        auth = {"idToken": state["users"][user]["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
        script += f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));"
    context.add_init_script(script)
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    return context, page, errors


def expect_not_found(page, path):
    expect(page.get_by_role("heading", name=TITLE, exact=True)).to_be_visible()
    expect(page.get_by_text(path, exact=True)).to_be_visible()
    expect(page.get_by_role("button", name="Search and run commands")).to_be_visible()


def b64(value):
    return base64.urlsafe_b64encode(json.dumps(value).encode()).rstrip(b"=").decode()


def fake_idp(context, user):
    fake_token = None
    nonce = {}

    def authorize(route):
        query = parse_qs(urlparse(route.request.url).query)
        nonce["value"] = query["nonce"][0]
        callback = query["redirect_uri"][0] + "?" + urlencode({"code": "e2e", "state": query["state"][0]})
        route.fulfill(status=302, headers={"Location": callback})

    def token(route):
        nonlocal fake_token
        now = int(time.time())
        claims = {"iss": issuer, "aud": "loco", "sub": user, "iat": now, "exp": now + 3600, "nonce": nonce["value"]}
        fake_token = f"{b64({'alg': 'RS256', 'typ': 'JWT'})}.{b64(claims)}.e2e"
        route.fulfill(json={"access_token": "e2e", "token_type": "bearer", "id_token": fake_token})

    def api(route):
        headers = dict(route.request.headers)
        if fake_token is not None and headers.get("authorization") == f"Bearer {fake_token}":
            headers["authorization"] = f"Bearer {state['users'][user]['token']}"
        route.continue_(headers=headers)

    context.route(f"{issuer}/auth*", authorize)
    context.route(f"{issuer}/token", token)
    context.route(re.compile(r"/loco\.[a-z0-9.]+\.v\d+\.\w+/\w+$"), api)


with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    cases = []
    try:
        for theme, narrow in (("light", False), ("dark", True)):
            case = f"owner-{theme}{'-narrow' if narrow else ''}"
            context, page, errors = open_page(browser, "owner", theme, narrow)
            try:
                page.goto(ui + "/no/such/page?x=1")
                expect_not_found(page, "/no/such/page?x=1")
                page.screenshot(path=out / f"{case}.png", animations="disabled")
                page.get_by_role("link", name="Go to dashboard", exact=True).click()
                expect(page).to_have_url(re.compile(rf"/org/{org}/wks/[0-9a-f-]+$"))
                expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)

                page.goto(base + "/no-such-tab")
                expect_not_found(page, f"/org/{org}/wks/{shared}/no-such-tab")
                page.get_by_role("link", name="Go to dashboard", exact=True).click()
                expect(page).to_have_url(base)

                missing = str(uuid.uuid4())
                page.goto(base + f"/resource/{missing}")
                expect_not_found(page, f"/org/{org}/wks/{shared}/resource/{missing}")
                page.get_by_role("button", name=re.compile(r"^Search (⌘K|Ctrl K)$")).click()
                expect(page.get_by_role("dialog", name="Command palette")).to_be_visible()
                page.keyboard.press("Escape")

                unknown = str(uuid.uuid4())
                page.goto(f"{ui}/org/{org}/wks/{unknown}")
                expect_not_found(page, f"/org/{org}/wks/{unknown}")
                assert not errors, errors
                cases.append(case)
            except BaseException:
                page.screenshot(path=out / f"failure-{case}.png", animations="disabled")
                raise
            finally:
                context.close()

        context, page, errors = open_page(browser, "member")
        try:
            page.goto(f"{ui}/org/{org}/wks/{sibling}")
            expect_not_found(page, f"/org/{org}/wks/{sibling}")
            page.get_by_role("link", name="Go to dashboard", exact=True).click()
            expect(page).to_have_url(base)
            expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)
            page.goto(base + f"/resource/{uuid.uuid4()}")
            expect(page.get_by_role("heading", name=TITLE, exact=True)).to_be_visible()
            page.goto(base)
            expect(page).to_have_url(base)
            expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)
            assert not errors, errors
            cases.append("member")
        except BaseException:
            page.screenshot(path=out / "failure-member.png", animations="disabled")
            raise
        finally:
            context.close()

        context, page, errors = open_page(browser, None)
        try:
            page.goto(ui + "/")
            expect(page).to_have_url(ui + "/")
            expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)

            deep = f"/org/{org}/wks/{shared}/no-such-tab?env=production"
            fake_idp(context, "owner")
            page.goto(ui + deep)
            expect(page).to_have_url(ui + "/login")
            page.get_by_role("button", name="Continue", exact=True).click()
            expect(page).to_have_url(ui + deep)
            expect_not_found(page, deep)
            page.screenshot(path=out / "signed-out-return.png", animations="disabled")
            assert not errors, errors
            cases.append("signed-out")
        except BaseException:
            page.screenshot(path=out / "failure-signed-out.png", animations="disabled")
            raise
        finally:
            context.close()
    finally:
        browser.close()
print(f"PASS: not-found cases {', '.join(cases)}")
