import contextlib
import argparse
import json
import re
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import expect
from harness import evidence, load_state, open_page, repo, session

parser = argparse.ArgumentParser(description="Check settings field names, focus and descriptions in the production UI")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-labels"))
args = parser.parse_args()
state = load_state(args, 'settings-labels', 'tests/ui/fixtures/settings-labels/scenario.json')
orgs = evidence.rpc(state["api_url"], state["users"]["owner"]["token"], "loco.org.v1.OrgService/ListUserOrgs", {"userId": state["users"]["owner"]["id"]})
org_ids = {org["name"]: org["id"] for org in orgs["orgs"]}
org_id = org_ids["acme"]
workspace_id = state["workspaces"]["acme/shared"]
base = f"/org/{org_id}/wks/{workspace_id}/settings"
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
results = []


def descriptions(field):
    return field.evaluate("(node) => (node.getAttribute('aria-describedby') || '').split(/\\s+/).filter(Boolean).map(id => { const hint = document.getElementById(id); if (!hint) throw new Error('Missing describedby target'); return hint.textContent; })")


def check_table_geometry(page):
    for table in page.get_by_role("table").all():
        if not table.is_visible():
            continue
        head = table.get_by_role("columnheader").first
        assert abs(head.bounding_box()["height"] - 40) <= 1
        assert head.evaluate("node => parseFloat(getComputedStyle(node).paddingLeft)") == 20
        geometry = table.evaluate("node => { const table = node.getBoundingClientRect(); const card = node.closest('.rounded-card').getBoundingClientRect(); return {left: table.left - card.left, right: card.right - table.right}; }")
        assert geometry["left"] <= 2 and geometry["right"] <= 2, geometry
        rows = table.locator("tbody tr")
        for row in rows.all():
            if row.inner_text().strip() == "History kept":
                assert abs(row.bounding_box()["height"] - 40) <= 1
            else:
                assert row.bounding_box()["height"] >= 51


def check_time_preferences(page):
    section = page.get_by_role("region", name="Date and time", exact=True)
    zone = section.get_by_role("group", name="Time zone", exact=True)
    zone.get_by_role("button", name="UTC", exact=True).click()
    clock = section.get_by_role("group", name="Clock", exact=True)
    clock.get_by_role("button", name="12-hour", exact=True).click()
    expect(section.locator("time")).to_have_text(re.compile(r".*\d+:\d{2}:\d{2}\.\d{3} (AM|PM) UTC"))
    assert page.evaluate("localStorage.getItem('loco-tz')") == "utc"
    assert page.evaluate("localStorage.getItem('loco-clock')") == "12"
    page.reload()
    expect(section.get_by_role("button", name="UTC", exact=True)).to_have_attribute("aria-pressed", "true")
    expect(section.get_by_role("button", name="12-hour", exact=True)).to_have_attribute("aria-pressed", "true")
    clock.get_by_role("button", name="24-hour", exact=True).click()
    expect(section.locator("time")).to_have_text(re.compile(r".*\d{2}:\d{2}:\d{2}\.\d{3} UTC"))


def check_webhooks(page):
    new_webhook = page.get_by_role("button", name="New webhook", exact=True)
    new_webhook.click()
    page.keyboard.press("Escape")
    expect(new_webhook).to_be_focused()
    page.keyboard.press("Enter")
    page.get_by_role("dialog").get_by_role("button", name="Cancel", exact=True).click()
    expect(new_webhook).to_be_focused()
    new_webhook.click()
    url = page.get_by_role("textbox", name="Endpoint URL", exact=True)
    assert url.evaluate("node => node.labels.length") == 1
    expect(url).to_be_focused()
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
    groups = page.get_by_role("group", name="Event groups", exact=True)
    builds = groups.get_by_role("button", name=re.compile(r"^Builds "))
    deployments = groups.get_by_role("button", name=re.compile(r"^Deployments "))
    expect(builds).to_have_attribute("aria-pressed", "false")
    builds.click()
    expect(builds).to_have_attribute("aria-pressed", "true")
    expect(builds).to_have_accessible_name("Builds all")
    expect(builds.get_by_text("all", exact=True)).to_be_visible()
    build_events = page.get_by_role("group", name="Builds events", exact=True)
    failed = build_events.get_by_role("button", name="build.failed", exact=True)
    expect(failed).to_have_attribute("aria-pressed", "true")
    failed.click()
    expect(failed).to_have_attribute("aria-pressed", "false")
    expect(builds).to_have_attribute("aria-pressed", "true")
    expect(builds).to_have_accessible_name(re.compile(r"^Builds \d+/\d+$"))
    chosen, total = builds.get_attribute("aria-label").removeprefix("Builds ").split("/")
    assert int(chosen) == int(total) - 1
    expect(builds.get_by_text(f"{chosen}/{total}", exact=True)).to_be_visible()
    deployments.click()
    expect(deployments).to_have_attribute("aria-pressed", "true")
    deployment_events = page.get_by_role("group", name="Deployments events", exact=True)
    deployment_events.wait_for()
    expect(build_events).to_be_hidden()
    builds.click()
    expect(build_events).to_be_visible()
    expect(deployment_events).to_be_hidden()
    expect(builds).to_have_accessible_name(f"Builds {chosen}/{total}")
    expect(failed).to_have_attribute("aria-pressed", "false")
    deployments.click()
    expect(deployment_events).to_be_visible()
    expect(deployments).to_have_attribute("aria-pressed", "true")
    deployments.click()
    expect(deployments).to_have_attribute("aria-pressed", "false")
    expect(deployment_events).to_be_hidden()
    deployments.click()
    pressed_events = deployment_events.get_by_role("button", pressed=True)
    for _ in range(pressed_events.count()):
        pressed_events.first.click()
    expect(deployment_events).to_be_hidden()
    expect(deployments).to_be_focused()
    expect(deployments).to_have_attribute("aria-pressed", "false")
    expect(builds).to_have_attribute("aria-pressed", "true")
    create.click()
    page.get_by_role("dialog", name="Signing secret").wait_for()
    page.get_by_role("button", name="Copy signing secret", exact=True).wait_for()
    page.get_by_role("button", name="Done", exact=True).click()
    expect(new_webhook).to_be_focused()
    page.get_by_role("button", name=f"Deliveries for {endpoint}", exact=True).click()
    drawer = page.get_by_role("dialog", name=endpoint)
    drawer.get_by_text("build.created", exact=True).wait_for()
    drawer.get_by_role("button", name="Failed", exact=True).wait_for()
    page.keyboard.press("Escape")
    drawer.wait_for(state="hidden")
    page.get_by_role("button", name=f"Actions for {endpoint}", exact=True).click()
    page.get_by_role("menuitem", name="Delete", exact=True).click()
    page.get_by_role("alertdialog", name="Delete webhook?").get_by_role("button", name="Delete webhook", exact=True).click()
    page.get_by_text("No webhooks yet.", exact=True).wait_for()


def check_environments(page):
    environments = page.get_by_role("region", name="Environments", exact=True)
    environments.get_by_role("button", name="Edit production", exact=True).click()
    dialog = page.get_by_role("dialog", name="Edit production", exact=True)
    dialog.get_by_role("textbox", name="Description", exact=True).fill("Customer traffic")
    dialog.get_by_role("button", name="Save", exact=True).click()
    expect(dialog).to_be_hidden()
    expect(environments.get_by_text("Customer traffic", exact=True)).to_be_visible()
    expect(environments.get_by_text("0 resources", exact=True).first).to_be_visible()


def check_create_dialogs(page):
    for path, button, field in (("/organizations", "New organization", "Name"), (base + "?scope=org", "New workspace", "Name"), ("/tokens", "New token", "Name")):
        page.goto(state["ui_url"] + path)
        opener = page.get_by_role("button", name=button, exact=True).first
        opener.click()
        expect(page.get_by_role("dialog").get_by_role("textbox", name=field, exact=True)).to_be_focused()
        if button == "New workspace":
            page.get_by_role("dialog", name="New workspace in acme", exact=True).get_by_text("A workspace is a project inside acme.", exact=False).wait_for()
        page.keyboard.press("Escape")
        expect(opener).to_be_focused()
    page.goto(state["ui_url"] + "/tokens?new=1")
    drawer = page.get_by_role("dialog", name="New token", exact=True)
    name = drawer.get_by_role("textbox", name="Name", exact=True)
    name.fill("CI Deploy")
    expect(name).to_have_value("ci-deploy")
    expect(drawer.get_by_role("button", name="Create token", exact=True)).to_be_disabled()
    drawer.get_by_role("button", name="Custom", exact=True).click()
    expect(drawer.get_by_role("button", name="Expires at date", exact=True)).to_be_visible()
    page.keyboard.press("Escape")
    expect(page).not_to_have_url(re.compile(r"new=1"))


with session(out) as browser:
    try:
        for user in ("owner", "member"):
            context, page, errors = open_page(browser, user, state=state)
            if user == "owner":
                context.add_init_script(
                    f"localStorage.setItem('loco:active-org:v1', {json.dumps(org_id)});"
                    f"localStorage.setItem('loco:active-workspace:v1', {json.dumps(workspace_id)});"
                )
                beta_id = org_ids["beta"]
                beta_workspace = state["workspaces"]["beta/beta-workspace"]
                org_list_rpc = "**/loco.org.v1.OrgService/ListUserOrgs"
                page.route(org_list_rpc, lambda route: route.abort())
                page.goto(f"{state['ui_url']}/org/{beta_id}/settings")
                page.wait_for_url(f"{state['ui_url']}/org/{beta_id}/wks/{beta_workspace}/settings?scope=org")
                assert page.get_by_role("textbox", name="Name", exact=True).input_value() == "beta"
                page.unroute(org_list_rpc)
                page.goto(f"{state['ui_url']}/org/{org_ids['empty']}/settings")
                page.wait_for_url(f"{state['ui_url']}/organizations")
                page.goto(state["ui_url"] + base + "?scope=org&q=zzz")
                expect(page.get_by_placeholder("Search workspaces", exact=True)).to_have_value("zzz")
                workspace_scope = page.get_by_role("group", name="Settings for").get_by_role("button", name="Workspace", exact=True)
                workspace_scope.click()
                page.wait_for_url(lambda url: parse_qs(urlparse(url).query).get("scope", ["workspace"]) == ["workspace"] and "q=" not in url)
                expect(workspace_scope).to_have_attribute("aria-pressed", "true")
                expect(page.get_by_placeholder("storefront", exact=True)).to_be_visible()
            for tab in ("ws", "org"):
                try:
                    page.goto(state["ui_url"] + base + "?scope=" + ("workspace" if tab == "ws" else "org"))
                    page.get_by_placeholder("storefront" if tab == "ws" else "team-loco").wait_for()
                    name = page.get_by_role("textbox", name="Name", exact=True)
                    assert name.count() == 1
                    assert page.get_by_text("Created", exact=True).first.evaluate("node => node.tagName") == "DT"
                    assert name.evaluate("node => node.labels.length") == 1
                    assert descriptions(name) == []
                    if user == "member":
                        assert name.is_disabled()
                    else:
                        original = name.input_value()
                        page.locator(f'label[for={json.dumps(name.get_attribute("id"))}]').click()
                        assert name.evaluate("node => node === document.activeElement")
                        name.fill("")
                        page.get_by_text("Name required", exact=True).wait_for()
                        assert name.get_attribute("aria-invalid") == "true"
                        assert "Name required" in descriptions(name)
                        page.get_by_role("button", name="Discard", exact=True).click()
                        assert name.input_value() == original
                        assert name.get_attribute("aria-invalid") == "false"
                        assert "Name required" not in descriptions(name)
                    if tab == "ws":
                        description = page.get_by_role("textbox", name="Description", exact=True)
                        assert description.count() == 1 and description.evaluate("node => node.labels.length") == 1
                        assert "256" in " ".join(descriptions(description))
                        assert description.get_attribute("id") != name.get_attribute("id")
                        if user == "member":
                            assert description.is_disabled()
                        else:
                            page.locator(f'label[for={json.dumps(description.get_attribute("id"))}]').click()
                            assert description.evaluate("node => node === document.activeElement")
                            name.focus()
                            page.keyboard.press("Tab")
                            assert description.evaluate("node => node === document.activeElement")
                    if tab == "org":
                        workspaces = page.get_by_role("region", name="Workspaces", exact=True)
                        expect(workspaces.get_by_role("link", name="shared", exact=True)).to_be_visible()
                        row = workspaces.get_by_role("link", name="shared", exact=True).locator("xpath=ancestor::tr")
                        assert row.evaluate("node => getComputedStyle(node).cursor") == "pointer"
                        background = row.evaluate("node => getComputedStyle(node).backgroundColor")
                        row.hover()
                        expect(row).not_to_have_css("background-color", background)
                        page.mouse.move(0, 0)
                        # The list fits the smallest page size, so it has no pager.
                        expect(workspaces.get_by_role("button", name="Next page", exact=True)).to_have_count(0)
                    if tab == "ws" and user == "owner":
                        check_webhooks(page)
                        check_environments(page)
                        check_create_dialogs(page)
                    check_table_geometry(page)
                    assert not errors, errors
                    results.append({"user": user, "tab": tab, "errors": list(errors)})
                except BaseException as error:
                    with contextlib.suppress(Exception):
                        page.screenshot(path=out / f"failure-{user}-{tab}.png", animations="disabled")
                    (out / "failure.json").write_text(json.dumps({"user": user, "tab": tab, "error": repr(error), "errors": errors, "completed": results}, indent=2))
                    raise
            if user == "owner":
                for theme in ("neobrutal", "catppuccin"):
                    for mode in ("light", "dark"):
                        page.evaluate("theme => localStorage.setItem('loco:color:v1', theme)", theme)
                        page.goto(state["ui_url"] + base + "?scope=workspace")
                        page.get_by_role("columnheader", name="Name", exact=True).wait_for()
                        if mode == "dark":
                            page.get_by_role("button").filter(has_text="Olivia Owner").click()
                            page.get_by_role("menuitem", name="Dark mode", exact=True).click()
                            page.keyboard.press("Escape")
                            expect(page.locator("html")).to_have_class(re.compile(r"\bdark\b"))
                        else:
                            expect(page.locator("html")).not_to_have_class(re.compile(r"\bdark\b"))
                        check_table_geometry(page)
            page.goto(state["ui_url"] + base + "?scope=account")
            check_time_preferences(page)
            context.close()
    finally:
        browser.close()
(out / "results.json").write_text(json.dumps(results, indent=2))
print(f"PASS: {len(results)} settings field-label cases", flush=True)
