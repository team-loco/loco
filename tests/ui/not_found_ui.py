from functools import partial
import argparse
import base64
import json
import re
import time
import uuid
from pathlib import Path
from urllib.parse import parse_qs, urlencode, urlparse

from playwright.sync_api import expect
from harness import evidence, load_state, open_page as new_page, repo, session

parser = argparse.ArgumentParser(description="Test the not-found page for signed-in and signed-out visitors against an isolated API")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/not-found"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(args, 'not-found', 'tests/ui/fixtures/deployment-history/scenario.json')
ui = state["ui_url"]
shared = state["workspaces"]["acme/shared"]
sibling = state["workspaces"]["acme/sibling"]
org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{shared}';")
base = f"{ui}/org/{org}/wks/{shared}"
WORKSPACE_URL = re.compile(re.escape(base) + r"(\?env=[\w-]+)?$")
issuer = f"{ui}/dex"
TITLE = "Nothing lives at this address"


open_page = partial(new_page, state=state)


def expect_not_found(page, path):
    expect(page.get_by_role("heading", name=TITLE, exact=True)).to_be_visible()
    logo = page.get_by_role("link", name="Loco dashboard").bounding_box()
    assert abs(logo["x"] + logo["width"] / 2 - page.viewport_size["width"] / 2) <= 1
    expect(page.get_by_text(re.compile("^" + re.escape(path) + r"(\?env=[\w-]+)?$"))).to_be_visible()
    for name in ("Resources", "Logs", "Settings"):
        expect(page.get_by_role("link", name=name, exact=True)).to_have_count(0)
    expect(page.get_by_role("button", name="Search and run commands")).to_have_count(0)


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


with session(out) as browser:
    cases = []
    created_org_names = set()
    try:
        context, page, errors = open_page(browser, "owner")
        context.add_init_script("""window.modeSounds = [];
            HTMLMediaElement.prototype.play = function () {
                if (/Mode[.]wav$/.test(this.src)) window.modeSounds.push(this.src.split('/').pop());
                return Promise.resolve();
            };""")
        try:
            page.goto(ui + "/no/such/page?x=1")
            expect_not_found(page, "/no/such/page?x=1")
            page.get_by_role("link", name="Go to dashboard", exact=True).click()
            expect(page).to_have_url(re.compile(rf"/org/{org}/wks/[0-9a-f-]+(\?env=[\w-]+)?$"))
            expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)

            page.goto(base + "/no-such-tab")
            expect_not_found(page, f"/org/{org}/wks/{shared}/no-such-tab")
            page.get_by_role("link", name="Go to dashboard", exact=True).click()
            expect(page).to_have_url(WORKSPACE_URL)
            expect(page.get_by_role("link", name="Resources", exact=True)).to_be_visible()

            missing = str(uuid.uuid4())
            page.goto(base + f"/resource/{missing}")
            expect_not_found(page, f"/org/{org}/wks/{shared}/resource/{missing}")
            palette = page.get_by_role("dialog", name="Command palette")
            results = palette.get_by_role("listbox", name="Results")
            query = palette.get_by_role("combobox", name="Search pages, resources and actions")
            search = page.get_by_role("button", name=re.compile(r"^Search (⌘K|Ctrl K)$"))
            search.click()
            expect(query).to_be_focused()
            page.keyboard.press("Escape")
            expect(palette).to_have_count(0)
            expect(search).to_be_focused()
            page.keyboard.press("ControlOrMeta+k")
            expect(query).to_be_focused()
            page.keyboard.press("Escape")
            expect(palette).to_have_count(0)
            expect(search).to_be_focused()

            page.goto(base + "/resources")
            header = page.locator('[data-slot="top-bar"]')
            expect(header.get_by_role("button", name="Search and run commands")).to_have_count(0)
            search_button = page.get_by_role("button", name="Search and run commands")
            expect(search_button).to_be_visible()
            page.get_by_role("button", name="Close sidebar").click()
            top_search = header.get_by_role("button", name="Search and run commands")
            expect(top_search).to_be_visible()
            header.get_by_role("button", name="Open sidebar").click()
            expect(top_search).to_have_count(0)
            top_search = search_button
            top_search.focus()
            page.keyboard.press("Enter")
            expect(query).to_be_focused()
            page.keyboard.press("Escape")
            expect(palette).to_have_count(0)
            expect(top_search).to_be_focused()

            top_search.click()
            expect(query).to_be_focused()
            for group in ("Actions", "Pages"):
                expect(results.get_by_role("group", name=group)).to_be_visible()
            expect(results.get_by_role("group", name="Workspaces")).to_have_count(0)
            expect(palette.get_by_role("option", name="New resource")).to_have_attribute("aria-selected", "true")
            page.keyboard.press("ArrowDown")
            expect(palette.get_by_role("option", name="New webhook")).to_have_attribute("aria-selected", "true")
            page.keyboard.press("ArrowDown")
            expect(palette.get_by_role("option", name="New token")).to_have_attribute("aria-selected", "true")
            query.fill("no such command")
            expect(palette.get_by_text("Nothing matches.", exact=True)).to_be_visible()
            query.fill("sibling")
            expect(results.get_by_role("group", name="Workspaces").get_by_role("option", name=re.compile(r"^Switch to sibling"))).to_be_visible()
            query.fill("api keys")
            expect(palette.get_by_role("option", name="Tokens", exact=True)).to_have_attribute("aria-selected", "true")
            page.keyboard.press("Enter")
            expect(palette).to_have_count(0)
            expect(page).to_have_url(re.compile(r"/tokens$"))
            expect(top_search).to_be_focused()

            nav_link = page.get_by_role("link", name="Resources", exact=True)
            nav_link.focus()
            page.keyboard.press("ControlOrMeta+k")
            expect(query).to_be_focused()
            page.keyboard.press("Escape")
            expect(palette).to_have_count(0)
            expect(nav_link).to_be_focused()

            page.get_by_role("button").filter(has_text="Olivia Owner").click()
            mode = page.get_by_role("radiogroup", name="Mode", exact=True)
            light = mode.get_by_role("radio", name="Light", exact=True)
            dark = mode.get_by_role("radio", name="Dark", exact=True)
            system = mode.get_by_role("radio", name="System", exact=True)
            expect(light).to_have_attribute("aria-checked", "true")
            icon = mode.locator(".mt-thumb .theme-icon")
            expect(icon).to_have_attribute("data-mode", "sun")
            icon.evaluate("""node => {
                window.modeIcon = node;
                window.modeTransitions = [];
                node.addEventListener('transitionrun', event => window.modeTransitions.push(event.propertyName));
            }""")
            page.evaluate("window.modeSounds = []")
            dark.click()
            expect(dark).to_have_attribute("aria-checked", "true")
            expect(icon).to_have_attribute("data-mode", "moon")
            page.wait_for_function("window.modeTransitions.includes('transform')")
            expect(page.locator("html")).to_have_class(re.compile(r"\bdark\b"))
            dark.focus()
            page.keyboard.press("ArrowLeft")
            expect(light).to_be_focused()
            expect(icon).to_have_attribute("data-mode", "sun")
            expect(light).to_have_attribute("aria-checked", "true")
            expect(page.locator("html")).not_to_have_class(re.compile(r"\bdark\b"))
            page.keyboard.press("ArrowLeft")
            expect(system).to_be_focused()
            expect(icon).to_have_attribute("data-mode", "monitor")
            expect(system).to_have_attribute("aria-checked", "true")
            assert page.evaluate("localStorage.getItem('loco:mode:v1')") is None
            assert page.evaluate("window.modeSounds") == ["darkMode.wav", "lightMode.wav"]
            assert icon.evaluate("node => node === window.modeIcon")
            page.emulate_media(reduced_motion="reduce")
            page.evaluate("window.modeTransitions = []")
            for radio, glyph in ((light, "sun"), (dark, "moon"), (system, "monitor")):
                radio.click()
                expect(icon).to_have_attribute("data-mode", glyph)
                expect(icon).to_have_css("transition-duration", "0s")
                assert icon.evaluate("node => getComputedStyle(node, '::before').transitionDuration") == "0s"
                assert icon.evaluate("node => getComputedStyle(node, '::after').transitionDuration") == "0s"
            assert page.evaluate("window.modeTransitions") == []
            assert icon.evaluate("node => node === window.modeIcon")
            page.emulate_media(reduced_motion="no-preference")
            page.keyboard.press("Escape")
            top_search.click()
            query.fill("Set mode")
            page.keyboard.press("Enter")
            choose_mode = palette.get_by_role("combobox", name="Choose a mode")
            expect(choose_mode).to_be_focused()
            expect(palette.get_by_role("option", name=re.compile("^System"))).to_have_attribute("aria-selected", "true")
            choose_mode.fill("dark")
            page.keyboard.press("Backspace")
            expect(choose_mode).to_have_value("dar")
            page.keyboard.press("Escape")
            expect(query).to_be_focused()
            expect(query).to_have_value("")
            query.fill("Set theme")
            page.keyboard.press("Enter")
            choose_theme = palette.get_by_role("combobox", name="Choose a theme")
            expect(choose_theme).to_be_focused()
            page.keyboard.press("Backspace")
            expect(query).to_be_focused()
            query.fill("Set theme")
            page.keyboard.press("Enter")
            palette.get_by_role("button", name="Back to all commands", exact=True).click()
            expect(query).to_be_focused()
            query.fill("Set mode")
            page.keyboard.press("Enter")
            choose_mode.fill("dark")
            page.keyboard.press("Enter")
            expect(palette).to_have_count(0)
            expect(page.locator("html")).to_have_class(re.compile(r"\bdark\b"))
            top_search.click()
            query.fill("Keyboard shortcuts")
            page.keyboard.press("Enter")
            shortcuts = page.get_by_role("dialog", name="Keyboard shortcuts", exact=True)
            expect(shortcuts).to_be_visible()
            expect(shortcuts).to_contain_text("Search and commands")
            page.keyboard.press("Escape")
            expect(shortcuts).not_to_be_visible()
            page.get_by_role("button").filter(has_text="Olivia Owner").click()
            page.get_by_role("menuitem", name="Keyboard shortcuts", exact=True).click()
            expect(shortcuts).to_be_visible()
            shortcuts.get_by_role("button", name="Close", exact=True).click()
            page.locator("body").click(position={"x": 700, "y": 500})
            page.keyboard.press("?")
            expect(shortcuts).to_be_visible()
            page.keyboard.press("Escape")
            top_search.click()
            expect(query).to_be_focused()
            page.keyboard.press("?")
            expect(query).to_have_value("?")
            expect(palette).to_be_visible()
            page.keyboard.press("Escape")

            page.goto(ui + "/404.html")
            expect(page.get_by_role("heading", name=TITLE, exact=True)).to_be_visible()
            logo = page.get_by_role("link", name="Loco dashboard").bounding_box()
            assert abs(logo["x"] + logo["width"] / 2 - page.viewport_size["width"] / 2) <= 1
            expect(page.get_by_role("button", name=re.compile("^Search"))).to_have_count(0)

            unknown = f"/org/{uuid.uuid4()}/wks/{uuid.uuid4()}"
            page.goto(ui + unknown)
            expect_not_found(page, unknown)
            assert not errors, errors
            cases.append("owner")
        except BaseException:
            page.screenshot(path=out / "failure-owner.png", animations="disabled")
            raise
        finally:
            context.close()

        other_org_name = f"globex-{uuid.uuid4().hex}"
        created_org_names.add(other_org_name)
        other_org = evidence.rpc(state["api_url"], state["users"]["owner"]["token"], evidence.RPC_CREATE_ORG, {"name": other_org_name})["orgId"]
        context, page, errors = open_page(browser, "owner")
        try:
            for path in (f"/org/{other_org}/wks/{shared}", f"/org/{org}/wks/{shared}/resource/not-a-uuid", f"/org/{uuid.uuid4()}/team"):
                page.goto(ui + path)
                expect_not_found(page, path)
            page.goto(ui + f"/org/{org}/team")
            expect(page.get_by_role("button", name="Search and run commands")).to_be_visible()
            expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)

            # The owner is the new org's only member and it has no invitations.
            page.goto(ui + f"/org/{other_org}/team")
            owner_row = page.get_by_role("row").filter(has_text=state["users"]["owner"]["email"])
            expect(owner_row).to_be_visible()
            for view in ("All", "Members"):
                expect(page.get_by_role("button", name=re.compile(rf"^{view}\s*1$"))).to_be_visible()
            expect(page.get_by_role("row")).to_have_count(2)
            prompt = page.get_by_role("region", name="It's just you so far")
            expect(prompt).to_be_visible()
            expect(prompt.get_by_role("button", name="Invite", exact=True)).to_be_visible()
            expect(prompt.get_by_role("link", name="Docs")).to_be_visible()
            page.get_by_role("button", name=re.compile(r"^Invitations")).click()
            expect(page.get_by_text("No invitations", exact=True)).to_be_visible()
            page.goto(ui + "/organizations")
            page.get_by_role("button", name="New organization", exact=True).click()
            new_org_name = f"initech-{uuid.uuid4().hex}"
            created_org_names.add(new_org_name)
            page.get_by_role("dialog", name="New organization").get_by_role("textbox", name="Name").fill(new_org_name)
            with page.expect_response("**/CreateOrg") as creation:
                page.get_by_role("button", name="Create", exact=True).click()
            created_org = creation.value.json()["orgId"]
            expect(page).to_have_url(re.compile(rf"/org/{created_org}/wks/[0-9a-f-]+(\?env=[\w-]+)?$"))
            page.goto(ui + f"/org/{created_org}/team")
            expect(page.get_by_role("region", name="It's just you so far")).to_be_visible()
            expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)
            # Hold the API until the loading overlay shows, so the code below is read while the
            # overlay fades out and must stay the only exposed status.
            held = []
            page.route("**/loco.*/**", lambda route: held.append(route) if held is not None else route.continue_())
            page.goto(ui + "/cli/device?code=ABCD-EFGH")
            expect(page.get_by_role("status")).to_be_visible()
            pending, held = held, None
            for route in pending:
                route.continue_()
            expect(page.get_by_text("Check that this matches your terminal", exact=True)).to_be_visible()
            expect(page.get_by_role("status")).to_have_text("ABCD-EFGH")
            expect(page.get_by_role("textbox", name="Device code")).to_have_count(0)
            page.goto(ui + "/cli/device")
            expect(page.get_by_role("textbox", name="Device code")).to_be_editable()
            assert not errors, errors
            cases.append("owner-other-org")
        except BaseException:
            page.screenshot(path=out / "failure-owner-other-org.png", animations="disabled")
            raise
        finally:
            context.close()

        context, page, errors = open_page(browser, "member")
        try:
            foreign = f"/org/{uuid.uuid4()}/wks/{sibling}"
            page.goto(ui + foreign)
            expect_not_found(page, foreign)
            page.get_by_role("link", name="Go to dashboard", exact=True).click()
            expect(page).to_have_url(WORKSPACE_URL)
            expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)
            page.goto(base + f"/resource/{uuid.uuid4()}")
            expect(page.get_by_role("heading", name=TITLE, exact=True)).to_be_visible()
            page.goto(base)
            expect(page).to_have_url(WORKSPACE_URL)
            expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)
            page.get_by_role("button", name=re.compile(re.escape(state["users"]["member"]["email"]))).click()
            page.get_by_role("menuitem", name="Sign out", exact=True).click()
            expect(page).to_have_url(ui + "/")
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
            context.grant_permissions(["clipboard-read", "clipboard-write"], origin=ui)
            prompt = "Deploy my app to Loco: https://loco.build/skills.md"
            assert prompt in (repo / "docs/content/reference/ai.md").read_text()
            onboard = page.get_by_role("button", name="Onboard your agent to Loco", exact=True)
            expect(onboard).to_have_attribute("title", "Copies: " + prompt)
            onboard.click()
            expect(page.get_by_role("button", name="Copied. Paste it into your agent", exact=True)).to_be_visible()
            assert page.evaluate("navigator.clipboard.readText()") == prompt
            expect(onboard).to_be_visible()
            page.set_viewport_size(evidence.NARROW)
            page.emulate_media(reduced_motion="reduce")
            onboard.focus()
            page.keyboard.press("Enter")
            expect(page.get_by_role("button", name="Copied. Paste it into your agent", exact=True)).to_be_visible()
            assert page.evaluate("navigator.clipboard.readText()") == prompt
            expect(onboard).to_be_visible()
            page.set_viewport_size(evidence.DESKTOP)
            page.emulate_media(reduced_motion="no-preference")

            page.goto(ui + "/auth/callback?error=access_denied")
            expect(page).to_have_url(ui + "/login")
            expect(page.get_by_role("heading", name="Sign in to loco", exact=True)).to_be_visible()
            expect(page.get_by_role("link", name="loco home", exact=True)).to_have_attribute("href", "/")
            expect(page.locator("aside canvas")).to_be_visible()

            deep = f"/org/{org}/wks/{shared}/no-such-tab?env=production"
            fake_idp(context, "owner")
            page.goto(ui + deep)
            expect(page).to_have_url(ui + deep)
            expect_not_found(page, deep)
            assert not errors, errors
            cases.append("signed-out")
        except BaseException:
            page.screenshot(path=out / "failure-signed-out.png", animations="disabled")
            raise
        finally:
            context.close()
    finally:
        try:
            if created_org_names:
                owner = state["users"]["owner"]
                organizations = evidence.rpc(state["api_url"], owner["token"], "loco.org.v1.OrgService/ListUserOrgs", {"userId": owner["id"]})["orgs"]
                for organization in organizations:
                    if organization["name"] in created_org_names:
                        evidence.rpc(state["api_url"], owner["token"], "loco.org.v1.OrgService/DeleteOrg", {"orgId": organization["id"]})
        finally:
            browser.close()
print(f"PASS: not-found cases {', '.join(cases)}")
