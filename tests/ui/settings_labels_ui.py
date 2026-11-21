import argparse
import json
import re
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from harness import expect_rpc, load_state, open_page, repo, session, sql
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check settings field names, focus and descriptions in the production UI"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-labels"))
args = parser.parse_args()
state = load_state(
    args, "settings-labels", "tests/ui/fixtures/settings-labels/scenario.json"
)
org_id = state["orgs"]["acme"]["id"]
workspace_id = state["workspaces"]["acme/shared"]
base = f"/org/{org_id}/wks/{workspace_id}/settings"
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
results = []


def descriptions(field):
    return field.evaluate(
        "(node) => (node.getAttribute('aria-describedby') || '').split(/\\s+/).filter(Boolean).map(id => { const hint = document.getElementById(id); if (!hint) throw new Error('Missing describedby target'); return hint.textContent; })"
    )


def check_time_preferences(page):
    section = page.get_by_role("region", name="Date and time", exact=True)
    zone = section.get_by_role("group", name="Time zone", exact=True)
    zone.get_by_role("button", name="UTC", exact=True).click()
    clock = section.get_by_role("group", name="Clock", exact=True)
    clock.get_by_role("button", name="12-hour", exact=True).click()
    expect(section.locator("time")).to_have_text(
        re.compile(r".*\d+:\d{2}:\d{2}\.\d{3} (AM|PM) UTC")
    )
    assert page.evaluate("localStorage.getItem('loco-tz')") == "utc"
    assert page.evaluate("localStorage.getItem('loco-clock')") == "12"
    page.reload()
    expect(section.get_by_role("button", name="UTC", exact=True)).to_have_attribute(
        "aria-pressed", "true"
    )
    expect(section.get_by_role("button", name="12-hour", exact=True)).to_have_attribute(
        "aria-pressed", "true"
    )
    clock.get_by_role("button", name="24-hour", exact=True).click()
    expect(section.locator("time")).to_have_text(
        re.compile(r".*\d{2}:\d{2}:\d{2}\.\d{3} UTC")
    )


def check_webhooks(page):
    new_webhook = page.get_by_role("button", name="New webhook", exact=True).first
    new_webhook.click()
    page.keyboard.press("Escape")
    expect(new_webhook).to_be_focused()
    page.keyboard.press("Enter")
    page.get_by_role("dialog").get_by_role("button", name="Cancel", exact=True).click()
    expect(new_webhook).to_be_focused()
    new_webhook.click()
    url = page.get_by_role("textbox", name="Endpoint URL", exact=True)
    assert url.evaluate("node => node.labels.length") == 1
    expect(
        page.get_by_role("textbox", name="Name (optional)", exact=True)
    ).to_be_focused()
    assert descriptions(url) == []
    create = page.get_by_role("button", name="Create webhook", exact=True)
    url.fill("https://user:secret@hooks.invalid/loco")
    create.click()
    rejection = "Use an https URL with a host name."
    page.get_by_text(rejection, exact=True).wait_for()
    assert url.get_attribute("aria-invalid") == "true"
    assert rejection in descriptions(url)
    endpoint = "https://hooks.invalid/loco"
    url.fill(endpoint)
    assert url.get_attribute("aria-invalid") == "false"
    assert rejection not in descriptions(url)
    page.get_by_role("button", name="Choose events", exact=True).click()
    assert create.is_disabled()
    expect(page.get_by_text("None chosen yet", exact=True)).to_be_visible()
    picker = page.get_by_role("group", name="Events to send", exact=True)
    builds = picker.get_by_role("checkbox", name=re.compile(r"^Builds "))
    deployments = picker.get_by_role("checkbox", name=re.compile(r"^Deployments "))
    expect(builds).to_have_accessible_name(re.compile(r"^Builds \d+ events$"))
    expect(builds).not_to_be_checked()
    builds.click()
    expect(builds).to_be_checked()
    picker.get_by_role("button", name="Show builds events", exact=True).click()
    failed = picker.get_by_role("checkbox", name="build.failed", exact=True)
    expect(failed).to_be_checked()
    failed.click()
    expect(failed).not_to_be_checked()
    expect(builds).to_have_attribute("aria-checked", "mixed")
    expect(builds).to_have_accessible_name(re.compile(r"^Builds \d+ of \d+$"))
    chosen, total = (
        builds.get_attribute("aria-label").removeprefix("Builds ").split(" of ")
    )
    assert int(chosen) == int(total) - 1
    expect(page.get_by_text(re.compile(rf"^{chosen} of \d+ events$"))).to_be_visible()
    show_deployments = picker.get_by_role(
        "button", name="Show deployments events", exact=True
    )
    show_deployments.click()
    expect(failed).to_be_hidden()
    expect(
        picker.get_by_role("button", name="Hide deployments events", exact=True)
    ).to_have_attribute("aria-expanded", "true")
    deployments.click()
    expect(deployments).to_be_checked()
    deployments.click()
    expect(deployments).not_to_be_checked()
    expect(builds).to_have_attribute("aria-checked", "mixed")
    create.click()
    page.get_by_role("dialog", name="Signing secret").wait_for()
    page.get_by_role("button", name="Copy signing secret", exact=True).wait_for()
    page.get_by_role("button", name="Done", exact=True).click()
    expect(new_webhook).to_be_focused()
    page.get_by_role("button", name=f"Deliveries for {endpoint}", exact=True).click()
    drawer = page.get_by_role("dialog", name="hooks.invalid")
    drawer.get_by_text("build.created", exact=True).wait_for()
    drawer.get_by_role("button", name=re.compile(r"^Failing")).wait_for()
    page.keyboard.press("Escape")
    drawer.wait_for(state="hidden")
    page.get_by_role("button", name=f"Actions for {endpoint}", exact=True).click()
    page.get_by_role("menuitem", name="Delete", exact=True).click()
    page.get_by_role("alertdialog", name="Delete webhook?").get_by_role(
        "button", name="Delete webhook", exact=True
    ).click()
    page.get_by_role("heading", name="No webhooks yet", exact=True).wait_for()


def check_environments(page):
    environments = page.get_by_role("region", name="Environments", exact=True)
    environments.get_by_role("button", name="Edit production", exact=True).click()
    dialog = page.get_by_role("dialog", name="Edit production", exact=True)
    name = dialog.get_by_role("textbox", name="Name", exact=True)
    expect(name).to_have_value("production")
    save = dialog.get_by_role("button", name="Save", exact=True)
    expect(save).to_be_disabled()
    description = dialog.get_by_role("textbox", name="Description", exact=True)
    description.fill("😀")
    expect(dialog.get_by_text("1 / 256", exact=True)).to_be_visible()
    description.fill("Customer traffic")
    expect(save).to_be_enabled()
    description.fill("")
    expect(save).to_be_disabled()
    description.fill("Customer traffic")
    name.fill("Production EU")
    save.click()
    expect(dialog).to_be_hidden()
    expect(environments.get_by_text("Customer traffic", exact=True)).to_be_visible()
    expect(environments.get_by_text("Production EU", exact=True)).to_be_visible()
    row = environments.get_by_role("row").filter(has_text="Production EU")
    settings_url = page.url
    workspace_url = settings_url.split("/settings")[0]
    row.get_by_role("link", name="Manage secrets", exact=True).click()
    page.wait_for_url(workspace_url + "/secrets?env=production")
    page.go_back()
    row.get_by_role("link", name="Manage resources", exact=True).click()
    page.wait_for_url(workspace_url + "/resources?env=production")
    switcher = page.get_by_title("Switch environment", exact=True)
    expect(switcher).to_have_text("Production EU")
    page.wait_for_url(
        lambda url: parse_qs(urlparse(url).query).get("env") == ["production"]
    )
    page.goto(settings_url)
    environments = page.get_by_role("region", name="Environments", exact=True)

    page.get_by_role("button", name="New environment", exact=True).click()
    dialog = page.get_by_role("dialog", name=re.compile(r"^New environment in "))
    name = dialog.get_by_role("textbox", name="Name", exact=True)
    create = dialog.get_by_role("button", name="Create", exact=True)
    for typed, error in [
        ("production eu", "production eu already exists in this workspace"),
        ("Production", "Too close to an existing name in this workspace"),
        ("🚀", "Include a letter from a to z"),
    ]:
        name.fill(typed)
        expect(dialog.get_by_text(error, exact=True)).to_be_visible()
        expect(create).to_be_disabled()
    name.fill("QA Preview 🚀")
    with expect_rpc(
        page, "CreateEnvironment", lambda body: body.get("name") == "QA Preview 🚀"
    ) as created:
        create.click()
    assert created.value.ok, created.value.text()
    assert "slug" not in created.value.request.post_data_json
    expect(dialog).to_be_hidden()
    expect(environments.get_by_text("QA Preview 🚀", exact=True)).to_be_visible()
    stored = sql(state, "SELECT slug FROM environments WHERE name='QA Preview 🚀';")
    assert stored == "qa-preview", stored


def check_create_dialogs(page):
    for path, button, field in (
        ("/organizations", "New organization", "Name"),
        (base + "?scope=org", "New workspace", "Name"),
        ("/tokens", "New token", "Name"),
    ):
        page.goto(state["ui_url"] + path)
        opener = page.get_by_role("button", name=button, exact=True).first
        opener.click()
        expect(
            page.get_by_role("dialog").get_by_role("textbox", name=field, exact=True)
        ).to_be_focused()
        if button == "New workspace":
            page.get_by_role(
                "dialog", name="New workspace in acme", exact=True
            ).get_by_text(
                "A workspace is a project inside acme.", exact=False
            ).wait_for()
        page.keyboard.press("Escape")
        expect(opener).to_be_focused()
    page.goto(state["ui_url"] + "/tokens?new=1")
    drawer = page.get_by_role("dialog", name="New token", exact=True)
    name = drawer.get_by_role("textbox", name="Name", exact=True)
    name.fill("CI Deploy")
    expect(name).to_have_value("CI Deploy")
    expect(drawer.get_by_text("Saved as ci-deploy")).to_be_visible()
    expect(
        drawer.get_by_role("button", name="Create token", exact=True)
    ).to_be_disabled()
    drawer.get_by_role("button", name="Custom", exact=True).click()
    expect(
        drawer.get_by_role("button", name="Expires at date", exact=True)
    ).to_be_visible()
    page.keyboard.press("Escape")
    expect(page).not_to_have_url(re.compile(r"new=1"))


with session(out) as browser:
    for user in ("owner", "member"):
        context, page, errors = open_page(browser, user, state=state)
        if user == "owner":
            context.add_init_script(
                f"localStorage.setItem('loco:active-org:v1', {json.dumps(org_id)});"
                f"localStorage.setItem('loco:active-workspace:v1', {json.dumps(workspace_id)});"
            )
            beta_id = state["orgs"]["beta"]["id"]
            beta_workspace = state["workspaces"]["beta/beta-workspace"]
            org_list_rpc = "**/loco.org.v1.OrgService/ListUserOrgs"
            page.route(org_list_rpc, lambda route: route.abort())
            page.goto(f"{state['ui_url']}/org/{beta_id}/settings")
            page.wait_for_url(
                f"{state['ui_url']}/org/{beta_id}/wks/{beta_workspace}/settings?scope=org"
            )
            assert (
                page.get_by_role("textbox", name="Name", exact=True).input_value()
                == "beta"
            )
            page.unroute(org_list_rpc)
            page.goto(f"{state['ui_url']}/org/{state['orgs']['empty']['id']}/settings")
            page.wait_for_url(f"{state['ui_url']}/organizations")
            page.goto(state["ui_url"] + base + "?scope=org&q=zzz")
            expect(
                page.get_by_placeholder("Search workspaces", exact=True)
            ).to_have_value("zzz")
            workspace_scope = page.get_by_role(
                "group", name="Settings for"
            ).get_by_role("button", name="Workspace", exact=True)
            workspace_scope.click()
            page.wait_for_url(
                lambda url: (
                    parse_qs(urlparse(url).query).get("scope", ["workspace"])
                    == ["workspace"]
                    and "q=" not in url
                )
            )
            expect(workspace_scope).to_have_attribute("aria-pressed", "true")
            expect(page.get_by_placeholder("storefront", exact=True)).to_be_visible()
        for tab in ("ws", "org"):
            page.goto(
                state["ui_url"]
                + base
                + "?scope="
                + ("workspace" if tab == "ws" else "org")
            )
            page.get_by_placeholder(
                "storefront" if tab == "ws" else "team-loco"
            ).wait_for()
            name = page.get_by_role("textbox", name="Name", exact=True)
            assert name.count() == 1
            expect(page.get_by_text("Created", exact=True).first).to_be_visible()
            assert name.evaluate("node => node.labels.length") == 1
            assert descriptions(name) == []
            if user == "member":
                assert name.is_disabled()
            else:
                original = name.input_value()
                page.locator(
                    f"label[for={json.dumps(name.get_attribute('id'))}]"
                ).click()
                assert name.evaluate("node => node === document.activeElement")
                required = "Use 1–63 characters." if tab == "ws" else "Name required"
                name.fill("")
                page.get_by_text(required, exact=True).wait_for()
                assert name.get_attribute("aria-invalid") == "true"
                assert required in descriptions(name)
                page.get_by_role("button", name="Discard", exact=True).click()
                assert name.input_value() == original
                assert name.get_attribute("aria-invalid") == "false"
                assert required not in descriptions(name)
            if tab == "ws":
                description = page.get_by_role(
                    "textbox", name="Description", exact=True
                )
                assert (
                    description.count() == 1
                    and description.evaluate("node => node.labels.length") == 1
                )
                assert "256" in " ".join(descriptions(description))
                if user != "member":
                    original_description = description.input_value()
                    description.fill("😀😀")
                    expect(page.get_by_text("2 / 256", exact=True)).to_be_visible()
                    page.get_by_role("button", name="Discard", exact=True).click()
                    assert description.input_value() == original_description
                assert description.get_attribute("id") != name.get_attribute("id")
                if user == "member":
                    assert description.is_disabled()
                else:
                    page.locator(
                        f"label[for={json.dumps(description.get_attribute('id'))}]"
                    ).click()
                    assert description.evaluate(
                        "node => node === document.activeElement"
                    )
                    name.focus()
                    page.keyboard.press("Tab")
                    assert description.evaluate(
                        "node => node === document.activeElement"
                    )
            if tab == "org":
                workspaces = page.get_by_role("region", name="Workspaces", exact=True)
                expect(
                    workspaces.get_by_role("link", name="shared", exact=True)
                ).to_be_visible()
                row = workspaces.get_by_role("link", name="shared", exact=True).locator(
                    "xpath=ancestor::tr"
                )
                background = row.evaluate(
                    "node => getComputedStyle(node).backgroundColor"
                )
                row.hover()
                expect(row).not_to_have_css("background-color", background)
                page.mouse.move(0, 0)
                expect(
                    workspaces.get_by_role("button", name="Next page", exact=True)
                ).to_be_disabled()
                row.click()
                page.wait_for_url(
                    lambda url: (
                        parse_qs(urlparse(url).query).get("scope", ["workspace"])
                        == ["workspace"]
                    )
                )
                expect(
                    page.get_by_placeholder("storefront", exact=True)
                ).to_be_visible()
            if tab == "ws" and user == "owner":
                settings_url = page.url
                page.goto(settings_url.split("/settings")[0] + "/webhooks")
                expect(
                    page.get_by_role("heading", name="Webhooks", exact=True)
                ).to_be_visible()
                check_webhooks(page)
                page.goto(settings_url)
                expect(
                    page.get_by_role("region", name="Environments", exact=True)
                ).to_be_visible()
                check_environments(page)
                check_create_dialogs(page)
            assert not errors, errors
            results.append({"user": user, "tab": tab, "errors": list(errors)})
        if user == "owner":
            for theme in ("neobrutal", "catppuccin"):
                for mode in ("light", "dark"):
                    page.evaluate(
                        "theme => localStorage.setItem('loco:color:v1', theme)",
                        theme,
                    )
                    page.goto(state["ui_url"] + base + "?scope=workspace")
                    page.get_by_role("columnheader", name="Name", exact=True).wait_for()
                    if mode == "dark":
                        page.get_by_role("button").filter(
                            has_text="Olivia Owner"
                        ).click()
                        page.get_by_role("radiogroup", name="Mode").get_by_role(
                            "radio", name="Dark", exact=True
                        ).click()
                        page.keyboard.press("Escape")
                        expect(page.locator("html")).to_have_class(
                            re.compile(r"\bdark\b")
                        )
                    else:
                        expect(page.locator("html")).not_to_have_class(
                            re.compile(r"\bdark\b")
                        )
        page.goto(state["ui_url"] + base)
        page.wait_for_url(
            lambda url: parse_qs(urlparse(url).query).get("scope") == ["workspace"]
        )
        page.goto(state["ui_url"] + base + "?scope=personal")
        group = page.get_by_role("group", name="Settings for", exact=True)
        expect(
            group.get_by_role("button", name="Personal", exact=True)
        ).to_have_attribute("aria-pressed", "true")
        group.get_by_role("button", name="Workspace", exact=True).click()
        expect(
            group.get_by_role("button", name="Workspace", exact=True)
        ).to_have_attribute("aria-pressed", "true")
        expect(page).not_to_have_url(re.compile(r"scope=personal"))
        group.get_by_role("button", name="Personal", exact=True).click()
        expect(page).to_have_url(re.compile(r"scope=personal"))
        check_time_preferences(page)
        context.close()
with session(out) as browser:
    context, page, errors = open_page(browser, "member", state=state)

    def without_org_scopes(route):
        body = route.fetch().json()
        body["scopes"] = [
            item
            for item in body.get("scopes", [])
            if item.get("entityType") != "ENTITY_TYPE_ORGANIZATION"
        ]
        route.fulfill(json=body)

    context.route("**/loco.token.v1.TokenService/GetScopes", without_org_scopes)
    page.goto(state["ui_url"] + base + "?scope=org")
    expect(
        page.get_by_text("Organization settings need admin access", exact=True)
    ).to_be_visible()
    page.wait_for_url(
        lambda url: parse_qs(urlparse(url).query).get("scope") == ["workspace"]
    )
    expect(
        page.get_by_role("group", name="Settings for").get_by_role(
            "button", name="Organization", exact=True
        )
    ).to_have_count(0)
    assert not errors, errors
    context.close()
with session(out) as browser:
    context, page, errors = open_page(browser, "nameless", state=state)
    page.goto(state["ui_url"] + "/profile")
    name = page.get_by_role("textbox", name="Name", exact=True)
    expect(name).to_have_attribute("placeholder", "Your name")
    expect(name).to_have_value("nameless@acme.test")
    expect(page.get_by_text("Unsaved changes")).to_have_count(0)
    context.close()
    browser.close()
(out / "results.json").write_text(json.dumps(results, indent=2))
print(f"PASS: {len(results)} settings field-label cases", flush=True)
