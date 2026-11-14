import argparse
import atexit
import contextlib
import importlib.util
import io
import json
import os
import re
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description="Check the audit log page for organization admins, workspace admins and members")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/audit-ui"))
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="audit", scenario=str(repo / "tests/ui/fixtures/audit/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    print("Preview state:", args.state, flush=True)
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
api = state["api_url"]
users = state["users"]
org = evidence.rpc(api, users["owner"]["token"], "loco.org.v1.OrgService/ListUserOrgs", {"userId": users["owner"]["id"]})["orgs"][0]["id"]
shared = state["workspaces"]["acme/shared"]
sibling = state["workspaces"]["acme/sibling"]
private = state["workspaces"]["acme/private"]
if "audit" not in state:
    spec_body = {"service": {"routing": {"port": 8080}, "regions": {"us-east-1": {"enabled": True, "cpu": "100m", "memory": "128Mi"}}}}

    def create(workspace, name):
        body = {"workspaceId": workspace, "name": name, "type": "RESOURCE_TYPE_SERVICE", "spec": spec_body}
        return evidence.rpc(api, users["owner"]["token"], "loco.resource.v1.ResourceService/CreateResource", body)["resourceId"]

    checkout = create(shared, "checkout")
    inventory = create(sibling, "inventory")
    # More than one page of events, so the pager has an older page.
    for index in range(55):
        evidence.rpc(api, users["owner"]["token"], "loco.resource.v1.ResourceService/UpdateResource", {"resourceId": checkout, "description": f"Checkout revision {index}"})
    create(private, "ledger")
    evidence.rpc(api, users["workspace_admin"]["token"], "loco.workspace.v1.WorkspaceService/UpdateWorkspace", {"workspaceId": sibling, "description": "Stock levels"})
    state["audit"] = {"checkout": checkout, "inventory": inventory}
    evidence.write_json(Path(args.state), state)
audit_path = f"/org/{org}/audit"
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)


def open_page(browser, user, theme, path):
    context = browser.new_context(viewport=evidence.DESKTOP, color_scheme=theme)
    auth = {"idToken": users[user]["token"], "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
    context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)}, JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.MODE_KEY)}, {json.dumps(theme)});")
    page = context.new_page()
    calls = []

    def record(response):
        if "/loco." in response.url:
            calls.append({"method": response.url.rsplit("/", 1)[-1], "body": response.request.post_data_json, "status": response.status})

    page.on("response", record)
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.goto(state["ui_url"] + path)
    return context, page, calls, errors


def list_events(calls):
    return [call for call in calls if call["method"] == "ListEvents"]


with sync_playwright() as p:
    browser = p.chromium.launch()
    for theme in ("light", "dark"):
        # Organization admins read the whole organization from the sidebar entry.
        context, page, calls, errors = open_page(browser, "owner", theme, f"/org/{org}/wks/{shared}")
        page.get_by_role("link", name="Audit log", exact=True).click()
        page.wait_for_url("**" + audit_path)
        rows = page.get_by_role("list", name="Audit events").get_by_role("button")
        expect(rows).to_have_count(50)
        evidence.settle(page)
        assert all(call["body"]["orgId"] == org and not call["body"].get("workspaceId") for call in list_events(calls)), list_events(calls)
        expect(page.get_by_role("list", name="Audit events")).to_contain_text("ledger")
        page.screenshot(path=out / f"org-admin-{theme}.png", animations="disabled")

        with page.expect_response(lambda response: response.url.endswith("/ListEvents")) as older:
            page.get_by_role("button", name="Older events", exact=True).click()
        assert older.value.request.post_data_json.get("beforeSeq"), older.value.request.post_data_json
        expect(rows).not_to_have_count(50)
        page.get_by_role("button", name="Newest events", exact=True).click()
        expect(rows).to_have_count(50)

        page.get_by_role("button", name="Event", exact=True).click()
        page.get_by_role("option", name=re.compile(r"^Created a resource")).click()
        page.keyboard.press("Escape")
        expect(page).to_have_url(re.compile(r"[?&]types=resource\.created"))
        expect(rows).to_have_count(3)
        assert list_events(calls)[-1]["body"]["types"] == ["resource.created"]

        rows.first.click()
        drawer = page.get_by_role("dialog")
        expect(drawer.get_by_role("heading", name="Created a resource", exact=True)).to_be_visible()
        expect(drawer.get_by_role("button", name="Newer event", exact=True)).to_be_disabled()
        expect(drawer.get_by_title("Copy request ID", exact=True)).to_be_visible()
        request_id = drawer.get_by_title("Copy request ID", exact=True).inner_text().strip()
        expect(drawer.get_by_role("button", name=re.compile(r"^More by "))).to_be_visible()
        evidence.settle(page)
        page.screenshot(path=out / f"drawer-{theme}.png", animations="disabled")
        drawer.get_by_role("button", name="Older event", exact=True).click()
        request_id = drawer.get_by_title("Copy request ID", exact=True).inner_text().strip()
        expect(drawer.get_by_role("button", name="Newer event", exact=True)).to_be_enabled()
        page.keyboard.press("Escape")
        expect(page).not_to_have_url(re.compile(r"[?&]event="))

        search = page.get_by_role("textbox", name="Filter this page", exact=True)
        before_filter = len(list_events(calls))
        search.fill("l")
        expect(rows).to_have_count(3)
        search.fill("ledger")
        expect(rows).to_have_count(1)
        evidence.settle(page)
        assert len(list_events(calls)) == before_filter, list_events(calls)
        page.screenshot(path=out / f"page-filter-{theme}.png", animations="disabled")
        page.get_by_role("button", name="Clear search", exact=True).click()
        expect(rows).to_have_count(3)
        page.get_by_role("button", name="Clear event filter", exact=True).click()
        expect(rows).to_have_count(50)
        expect(page.get_by_role("list", name="Audit events")).not_to_contain_text("inventory")
        with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("requestId") == request_id) as searched:
            search.fill(request_id)
        assert not searched.value.request.post_data_json.get("beforeSeq")
        assert searched.value.request.post_data_json["requestId"] == request_id
        expect(rows).to_have_count(1)
        expect(page).to_have_url(re.compile(r"[?&]q=" + request_id))
        evidence.settle(page)
        page.screenshot(path=out / f"search-{theme}.png", animations="disabled")
        page.set_viewport_size({"width": 390, "height": 844})
        expect(search).to_be_visible()
        evidence.settle(page)
        page.screenshot(path=out / f"search-{theme}-narrow.png", animations="disabled")
        page.set_viewport_size(evidence.DESKTOP)
        page.get_by_role("button", name="Clear search", exact=True).click()
        expect(rows).to_have_count(50)
        page.get_by_role("button", name="Event", exact=True).click()
        page.get_by_role("option", name=re.compile(r"^Created a resource")).click()
        page.keyboard.press("Escape")
        expect(rows).to_have_count(3)
        page.get_by_role("button", name="Actor", exact=True).click()
        expect(page.get_by_role("option", name="Loco", exact=True)).to_be_visible()
        expect(page.get_by_role("option", name="Cluster agent", exact=True)).to_be_visible()
        evidence.settle(page)
        page.screenshot(path=out / f"actor-filter-{theme}.png", animations="disabled")
        with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("actorTypes") == ["system"]) as system_events:
            page.get_by_role("option", name="Loco", exact=True).click()
        assert system_events.value.request.post_data_json["actorTypes"] == ["system"]
        expect(page.get_by_text("No events match these filters", exact=True)).to_be_visible()
        page.get_by_role("button", name="Clear actor filter", exact=True).click()
        expect(rows).to_have_count(3)
        rows.first.click()
        with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("actorIds") == [users["owner"]["id"]]) as by_actor:
            page.get_by_role("dialog").get_by_role("button", name=re.compile(r"^More by ")).click()
        assert by_actor.value.request.post_data_json["actorIds"] == [users["owner"]["id"]]
        expect(page.get_by_role("dialog")).not_to_be_visible()
        expect(page).to_have_url(re.compile(r"[?&]actor=" + users["owner"]["id"]))
        page.reload()
        expect(rows).to_have_count(3)
        assert list_events(calls)[-1]["body"]["actorIds"] == [users["owner"]["id"]]

        # Settings no longer carries the audit log.
        page.goto(state["ui_url"] + f"/org/{org}/wks/{shared}/settings?scope=org")
        page.get_by_role("heading", name="Workspaces").first.wait_for()
        evidence.settle(page)
        expect(page.get_by_role("heading", name="Audit log", exact=True)).to_have_count(0)
        assert not errors, errors
        context.close()

        # Workspace admins read only the workspaces they administer, one at a time.
        context, page, calls, errors = open_page(browser, "workspace_admin", theme, f"/org/{org}/wks/{shared}")
        page.get_by_role("link", name="Audit log", exact=True).click()
        rows = page.get_by_role("list", name="Audit events").get_by_role("button")
        expect(rows.first).to_be_visible()
        evidence.settle(page)
        workspace_filter = page.get_by_role("button", name=re.compile(r"^(shared|sibling)$"))
        expect(workspace_filter).to_have_count(1)
        expect(page.get_by_role("button", name="Clear workspace filter", exact=True)).to_have_count(0)
        expect(page.get_by_role("list", name="Audit events")).not_to_contain_text("ledger")
        page.screenshot(path=out / f"workspace-admin-{theme}.png", animations="disabled")
        workspace_filter.click()
        page.get_by_role("option", name="sibling", exact=True).click()
        expect(page.get_by_role("list", name="Audit events")).to_contain_text("Wren Workspace")
        expect(page.get_by_role("list", name="Audit events")).not_to_contain_text("checkout")
        reads = list_events(calls)
        assert reads and all(call["body"].get("workspaceId") in (shared, sibling) for call in reads), reads
        assert {call["body"]["workspaceId"] for call in reads} == {shared, sibling}, reads
        assert all(call["status"] == 200 for call in reads), reads
        page.get_by_role("button", name="Actor", exact=True).click()
        with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("actorIds") == [users["workspace_admin"]["id"]]):
            page.get_by_role("option", name=re.compile("Wren Workspace")).click()
        expect(page.get_by_role("button", name="Clear actor filter", exact=True)).to_be_visible()
        assert list_events(calls)[-1]["body"]["actorIds"] == [users["workspace_admin"]["id"]]
        assert list_events(calls)[-1]["body"]["workspaceId"] == sibling

        # Workspace settings link to the log filtered to that workspace, and a resource filter in the URL applies on first load.
        page.goto(state["ui_url"] + f"/org/{org}/wks/{sibling}/settings?scope=workspace")
        page.get_by_role("link", name="Activity in audit log", exact=True).click()
        page.wait_for_url(f"**{audit_path}?range=90d&ws={sibling}")
        expect(page.get_by_role("button", name="Past 90 days", exact=True)).to_be_visible()
        expect(page.get_by_role("button", name="sibling", exact=True)).to_be_visible()
        calls.clear()
        page.goto(state["ui_url"] + f"{audit_path}?ws={sibling}&res={state['audit']['inventory']}")
        expect(page.get_by_role("button", name="inventory", exact=True)).to_be_visible()
        expect(page.get_by_role("list", name="Audit events")).not_to_contain_text("Wren Workspace")
        first = list_events(calls)[0]["body"]
        assert first["workspaceId"] == sibling and first["resourceId"] == state["audit"]["inventory"], first
        assert not errors, errors
        context.close()

        # Members see no entry, and the page itself says only admins can read it.
        context, page, calls, errors = open_page(browser, "member", theme, f"/org/{org}/wks/{shared}")
        page.get_by_role("link", name="Team", exact=True).wait_for()
        evidence.settle(page)
        expect(page.get_by_role("link", name="Audit log", exact=True)).to_have_count(0)
        page.screenshot(path=out / f"member-sidebar-{theme}.png", clip={"x": 0, "y": 0, "width": 320, "height": 800}, animations="disabled")
        page.goto(state["ui_url"] + audit_path)
        expect(page.get_by_text("Only admins can see the audit log", exact=True)).to_be_visible()
        evidence.settle(page)
        page.screenshot(path=out / f"member-denied-{theme}.png", animations="disabled")
        assert not errors, errors
        context.close()
    browser.close()
print("audit log page passed for organization admins, workspace admins and members", flush=True)
