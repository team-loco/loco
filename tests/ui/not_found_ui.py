import argparse
import re
import uuid
from functools import partial
from pathlib import Path

from harness import (
    call,
    copied_text,
    evidence,
    expect_rpc,
    load_state,
    open_page as new_page,
    repo,
    session,
    sql,
)
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Test the not-found page for signed-in and signed-out visitors against an isolated API"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/not-found"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "not-found", "tests/ui/fixtures/deployment-history/scenario.json"
)
ui = state["ui_url"]
shared = state["workspaces"]["acme/shared"]
sibling = state["workspaces"]["acme/sibling"]
org = state["orgs"]["acme"]["id"]
base = f"{ui}/org/{org}/wks/{shared}"
WORKSPACE_URL = re.compile(re.escape(base) + r"(\?env=[\w-]+)?$")
issuer = f"{ui}/dex"
TITLE = "Nothing lives at this address"
CONFIG = "**/loco.config.v1.ConfigService/GetConfig*"


open_page = partial(new_page, state=state)


def multi_tenant(route):
    config = route.fetch()
    route.fulfill(
        response=config,
        json={**config.json(), "deploymentMode": "DEPLOYMENT_MODE_MULTI_TENANT"},
    )


def expect_not_found(page, path):
    expect(page.get_by_role("heading", name=TITLE, exact=True)).to_be_visible()
    logo_link = page.get_by_role("link", name="Loco dashboard")
    expect(logo_link).to_be_visible()
    logo = logo_link.bounding_box()
    assert abs(logo["x"] + logo["width"] / 2 - page.viewport_size["width"] / 2) <= 1
    chip = page.get_by_text(re.compile("^" + re.escape(path) + r"(\?env=[\w-]+)?$"))
    expect(chip).to_be_visible()
    expect(chip).to_have_css("border-top-style", "solid")
    for name in ("Resources", "Logs", "Settings"):
        expect(page.get_by_role("link", name=name, exact=True)).to_have_count(0)
    expect(page.get_by_role("button", name="Search and run commands")).to_have_count(0)


def stub_sign_in(context, user):
    def start(route):
        redirect_to = route.request.post_data_json["redirectTo"]
        pending["redirect_to"] = redirect_to
        route.fulfill(json={"authorizationUrl": f"{issuer}/auth"})

    def complete(route):
        evidence.authenticate(context, state, state["users"][user]["token"])
        route.fulfill(json={"redirectTo": pending["redirect_to"]})

    pending = {}
    context.route("**/StartWebLogin", start)
    context.route(
        f"{issuer}/auth",
        lambda route: route.fulfill(
            status=302, headers={"Location": f"{ui}/auth/callback?code=test&state=test"}
        ),
    )
    context.route("**/CompleteWebLogin", complete)


with session(out) as browser:
    cases = []
    context, page, errors = open_page(browser, "owner")
    context.add_init_script("""window.modeSounds = [];
            HTMLMediaElement.prototype.play = function () {
                if (/Mode[.]wav$/.test(this.src)) window.modeSounds.push(this.src.split('/').pop());
                return Promise.resolve();
            };""")
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
    query = palette.get_by_role("combobox", name="Search resources, pages and actions")
    search = page.get_by_role(
        "button", name=re.compile(r"^Command palette (⌘K|Ctrl K)$")
    )
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
    expect(header.get_by_role("button", name="Search and run commands")).to_have_count(
        0
    )
    search_button = page.get_by_role("button", name="Search and run commands")
    expect(search_button).to_be_visible()
    page.get_by_role("button", name="Close sidebar").click()
    top_search = header.get_by_role("button", name="Search and run commands")
    expect(top_search).to_be_visible()
    expect(top_search).to_contain_text("Search or jump to…")
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
    for group in ("This page", "Actions", "Copy", "Go to"):
        expect(results.get_by_role("group", name=group)).to_be_visible()
    expect(results.get_by_role("group", name="Workspaces")).to_have_count(0)
    for option in (
        "Show Map",
        "Show Table",
        "New resource",
        "New token",
        "New webhook",
    ):
        expect(palette.get_by_role("option", name=option)).to_have_attribute(
            "aria-selected", "true"
        )
        page.keyboard.press("ArrowDown")
    page.keyboard.press("Home")
    page.keyboard.press("Enter")
    expect(palette).to_have_count(0)
    expect(page).to_have_url(re.compile(r"/resources\?(.*&)?view=map(&|$)"))
    top_search.click()
    expect(query).to_be_focused()
    query.fill("no such command")
    expect(palette.get_by_text("Nothing matches.", exact=True)).to_be_visible()
    query.fill("sibling")
    expect(
        results.get_by_role("group", name="Workspaces").get_by_role(
            "option", name=re.compile(r"^Switch to sibling")
        )
    ).to_be_visible()
    query.fill("api keys")
    expect(palette.get_by_role("option", name="Tokens", exact=True)).to_have_attribute(
        "aria-selected", "true"
    )
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

    def icon_animation(radio):
        return radio.locator("svg").evaluate(
            "node => getComputedStyle(node).animationName"
        )

    # The sliding thumb sits under the selected icon, not over it.
    assert light.locator("svg").evaluate("""node => {
        const box = node.getBoundingClientRect();
        return node.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2));
    }""")

    page.evaluate("window.modeSounds = []")
    dark.click()
    expect(dark).to_have_attribute("aria-checked", "true")
    assert icon_animation(dark) == "mt-moon"
    expect(page.locator("html")).to_have_class(re.compile(r"\bdark\b"))
    dark.focus()
    page.keyboard.press("ArrowLeft")
    expect(light).to_be_focused()
    expect(light).to_have_attribute("aria-checked", "true")
    assert icon_animation(light) == "mt-sun"
    expect(page.locator("html")).not_to_have_class(re.compile(r"\bdark\b"))
    page.keyboard.press("ArrowLeft")
    expect(system).to_be_focused()
    expect(system).to_have_attribute("aria-checked", "true")
    assert icon_animation(system) == "mt-sys"
    assert page.evaluate("localStorage.getItem('loco:mode:v1')") is None
    assert page.evaluate("window.modeSounds") == [
        "darkMode.wav",
        "lightMode.wav",
    ]
    page.emulate_media(reduced_motion="reduce")
    for radio in (light, dark, system):
        radio.click()
        expect(radio).to_have_attribute("aria-checked", "true")
        assert icon_animation(radio) == "none"
    page.emulate_media(reduced_motion="no-preference")
    page.keyboard.press("Escape")
    top_search.click()
    query.fill("Set mode")
    page.keyboard.press("Enter")
    choose_mode = palette.get_by_role("combobox", name="Choose a mode")
    expect(choose_mode).to_be_focused()
    expect(palette.get_by_role("option", name=re.compile("^System"))).to_have_attribute(
        "aria-selected", "true"
    )
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
    expect(shortcuts).to_contain_text("Previous or next item, with its panel open")
    page.keyboard.press("?")
    expect(shortcuts).not_to_be_visible()
    top_search.click()
    expect(query).to_be_focused()
    page.keyboard.press("?")
    expect(query).to_have_value("?")
    expect(palette).to_be_visible()
    page.keyboard.press("Escape")

    page.goto(ui + "/404.html")
    expect(page.get_by_role("heading", name=TITLE, exact=True)).to_be_visible()
    logo_link = page.get_by_role("link", name="Loco dashboard")
    expect(logo_link).to_be_visible()
    logo = logo_link.bounding_box()
    assert abs(logo["x"] + logo["width"] / 2 - page.viewport_size["width"] / 2) <= 1
    expect(
        page.get_by_role("button", name=re.compile("^Command palette"))
    ).to_have_count(0)

    unknown = f"/org/{uuid.uuid4()}/wks/{uuid.uuid4()}"
    page.goto(ui + unknown)
    expect_not_found(page, unknown)
    assert not errors, errors
    cases.append("owner")
    context.close()

    other_org_name = f"globex-{uuid.uuid4().hex}"
    other_org = call(state, evidence.RPC_CREATE_ORG, {"name": other_org_name})["orgId"]
    context, page, errors = open_page(browser, "owner")
    for path in (
        f"/org/{other_org}/wks/{shared}",
        f"/org/{org}/wks/{shared}/resource/not-a-uuid",
        f"/org/{uuid.uuid4()}/team",
        f"/org/{org}/wks/{uuid.UUID(int=0)}",
        f"/org/{org}/wks/not-a-uuid",
    ):
        page.goto(ui + path)
        expect_not_found(page, path)
    # A remembered workspace equal to the URL's shares its lookup; the failed lookup runs once.
    missing = f"/org/{org}/wks/{uuid.UUID(int=0)}"
    page.evaluate(
        f"localStorage.setItem('loco:active-workspace:v1', '{uuid.UUID(int=0)}')"
    )
    lookups = []
    page.on(
        "request",
        lambda request: (
            lookups.append(request.url)
            if request.url.endswith("/GetWorkspace")
            else None
        ),
    )
    page.goto(ui + missing)
    expect_not_found(page, missing)
    page.wait_for_timeout(1000)
    assert len(lookups) <= 2, len(lookups)
    page.goto(ui + "/team")
    expect(page).to_have_url(re.compile(r"/org/[0-9a-f-]+/team$"))
    page.goto(ui + f"/org/{org}/team")
    expect(page.get_by_role("button", name="Search and run commands")).to_be_visible()
    expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)

    # The owner is the new org's only member and it has no invitations.
    page.goto(ui + f"/org/{other_org}/team")
    owner_row = page.get_by_role("row").filter(
        has_text=state["users"]["owner"]["email"]
    )
    expect(owner_row).to_be_visible()
    for view in ("All", "Members"):
        expect(
            page.get_by_role("button", name=re.compile(rf"^{view}\s*1$"))
        ).to_be_visible()
    expect(page.get_by_role("row")).to_have_count(2)
    prompt = page.get_by_role("region", name="It’s just you so far")
    expect(prompt).to_be_visible()
    expect(prompt.get_by_role("button", name="Invite", exact=True)).to_be_visible()
    expect(prompt.get_by_role("link", name="Docs")).to_be_visible()
    page.get_by_role("button", name=re.compile(r"^Invitations")).click()
    expect(page.get_by_text("No invitations", exact=True)).to_be_visible()
    page.goto(ui + "/organizations")
    page.get_by_role("button", name="New organization", exact=True).click()
    new_org_name = f"initech-{uuid.uuid4().hex}"
    page.get_by_role("dialog", name="New organization").get_by_role(
        "textbox", name="Name"
    ).fill(new_org_name)
    with expect_rpc(
        page, "CreateOrg", lambda body: body.get("name") == new_org_name
    ) as creation:
        page.get_by_role("button", name="Create", exact=True).click()
    created_org = creation.value.json()["orgId"]
    expect(page).to_have_url(
        re.compile(rf"/org/{created_org}/wks/[0-9a-f-]+(\?env=[\w-]+)?$")
    )
    page.goto(ui + f"/org/{created_org}/team")
    expect(page.get_by_role("region", name="It’s just you so far")).to_be_visible()
    expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)
    # Hold the API until the loading overlay shows, so the code below is read while the
    # overlay fades out and must stay the only exposed status.
    held = []
    page.route(
        "**/loco.*/**",
        lambda route: held.append(route) if held is not None else route.continue_(),
    )
    page.goto(ui + "/cli/device?code=ABCD-EFGH")
    expect(page.get_by_role("status")).to_be_visible()
    pending, held = held, None
    for route in pending:
        route.continue_()
    expect(
        page.get_by_text("Check that this matches your terminal", exact=True)
    ).to_be_visible()
    expect(page.get_by_role("status")).to_have_text("ABCD-EFGH")
    expect(page.get_by_role("textbox", name="Device code")).to_have_count(0)
    page.goto(ui + "/cli/device")
    code = page.get_by_role("textbox", name="Device code")
    expect(code).to_be_editable()
    code.fill("WRONG-CODE")
    code.press("Enter")
    expect(page.get_by_text("That code is invalid or has expired.")).to_be_visible()
    code.fill("OTHER-CODE")
    expect(page.get_by_text("That code is invalid or has expired.")).to_have_count(0)
    assert not errors, errors
    cases.append("owner-other-org")
    context.close()

    context, page, errors = open_page(browser, "member")
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
    account = page.locator('[data-slot="account-button"]')
    expect(account).to_have_text(re.compile(r"Max Member$"))
    expect(account).not_to_contain_text(state["users"]["member"]["email"])
    account.click()
    page.get_by_role("menuitem", name="Sign out", exact=True).click()
    expect(page).to_have_url(ui + "/login")
    assert not errors, errors
    cases.append("member")
    context.close()

    # A system admin's scopes cover every organization, so the client's route check passes
    # for any ID and only the API's answer shows the organization is absent.
    sql(
        state,
        "INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) VALUES "
        f"('{state['users']['resource_reader']['id']}', 'admin', 'system', "
        f"'{uuid.UUID(int=0)}');",
    )
    context, page, errors = open_page(browser, "resource_reader")
    for path in (
        f"/org/{uuid.UUID(int=0)}/team",
        f"/org/{uuid.uuid4()}/wks/{uuid.uuid4()}",
        f"/org/{org}/wks/{uuid.UUID(int=0)}",
    ):
        page.goto(ui + path)
        expect_not_found(page, path)
    page.goto(ui + f"/org/{org}/team")
    expect(page.get_by_role("button", name="Search and run commands")).to_be_visible()
    expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)
    assert not errors, errors
    cases.append("system-admin")
    context.close()

    # A self-hosted install, the default, sends / to sign-in, which waits for a click.
    context, page, errors = open_page(browser, None)
    page.goto(ui + "/")
    expect(page).to_have_url(ui + "/login")
    expect(page.get_by_role("button", name="Continue", exact=True)).to_be_visible()
    assert not errors, errors
    cases.append("self-hosted-root")
    context.close()

    context, page, errors = open_page(browser, None)
    held = []
    context.route(CONFIG, multi_tenant)
    context.route(CONFIG, lambda route: held.append(route), times=1)
    page.goto(ui + "/")
    expect(page.get_by_role("status")).to_be_visible()
    assert held
    expect(page).to_have_url(ui + "/")
    expect(page.get_by_role("button", name="Onboard your agent to Loco")).to_have_count(
        0
    )
    multi_tenant(held[0])
    expect(page).to_have_url(ui + "/")
    expect(page.get_by_role("heading", name=TITLE, exact=True)).to_have_count(0)
    account = (
        page.get_by_role("contentinfo").get_by_text("Account", exact=True).locator("..")
    )
    expect(account.get_by_role("button", name="Sign in", exact=True)).to_be_visible()
    expect(account.get_by_role("link", name="Dashboard", exact=True)).to_have_count(0)
    expect(
        page.get_by_role("contentinfo").get_by_role(
            "link", name="Dashboard", exact=True
        )
    ).to_have_count(0)
    prompt = "Deploy my app to Loco: https://loco.build/skills.md"
    assert prompt in (repo / "docs/content/reference/ai.md").read_text()
    onboard = page.get_by_role("button", name="Onboard your agent to Loco", exact=True)
    expect(onboard).to_have_attribute("title", "Copies: " + prompt)
    onboard.click()
    expect(
        page.get_by_role("button", name="Copied. Paste it into your agent", exact=True)
    ).to_be_visible()
    assert copied_text(page) == prompt
    expect(onboard).to_be_visible()
    page.set_viewport_size(evidence.NARROW)
    page.emulate_media(reduced_motion="reduce")
    onboard.focus()
    page.keyboard.press("Enter")
    expect(
        page.get_by_role("button", name="Copied. Paste it into your agent", exact=True)
    ).to_be_visible()
    assert copied_text(page) == prompt
    expect(onboard).to_be_visible()
    page.set_viewport_size(evidence.DESKTOP)

    # Reduced motion holds the deploy demo on its last frame.
    page.reload()
    demo = page.locator("#product")
    build = demo.get_by_text(re.compile(r"^Build \S+ (running|succeeded in 38s)"))
    expect(build).to_have_count(1)
    expect(build).to_contain_text("succeeded in 38s")
    pods = demo.get_by_text("us-ca-1", exact=True).locator("xpath=..").locator("i")
    pods.first.hover()
    expect(page.locator("[data-slot=tooltip-content]")).to_have_text(
        "sha256:4be1c09d, ready"
    )
    page.emulate_media(reduced_motion="no-preference")

    page.goto(ui + "/auth/callback?error=access_denied")
    expect(page).to_have_url(ui + "/login")
    expect(
        page.get_by_role("heading", name="Sign in to loco", exact=True)
    ).to_be_visible()
    expect(page.get_by_role("link", name="loco home", exact=True)).to_have_attribute(
        "href", "/"
    )
    expect(page.locator("aside canvas")).to_be_visible()

    deep = f"/org/{org}/wks/{shared}/no-such-tab?env=production"
    stub_sign_in(context, "owner")
    page.goto(ui + deep)
    expect(page).to_have_url(ui + deep)
    expect_not_found(page, deep)
    assert not errors, errors
    cases.append("signed-out")
    context.close()
print(f"PASS: not-found cases {', '.join(cases)}")
