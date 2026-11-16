import argparse
import base64
import re
from pathlib import Path

from playwright.sync_api import expect
from harness import evidence, load_state, open_page as new_page, repo, session

parser = argparse.ArgumentParser(description="Check the audit log page for organization admins, workspace admins and members")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/audit-ui"))
args = parser.parse_args()
state = load_state(args, 'audit', 'tests/ui/fixtures/audit/scenario.json')
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


def open_page(browser, user, path):
    context, page, errors = new_page(browser, user, state=state)
    calls = []

    def record(response):
        if "/loco." in response.url:
            calls.append({"method": response.url.rsplit("/", 1)[-1], "body": response.request.post_data_json, "status": response.status})

    page.on("response", record)
    page.goto(state["ui_url"] + path)
    return context, page, calls, errors


def list_events(calls):
    return [call for call in calls if call["method"] == "ListEvents"]


with session(out) as browser:
    # Organization admins read the whole organization from the sidebar entry.
    context, page, calls, errors = open_page(browser, "owner", f"/org/{org}/wks/{shared}")
    page.get_by_role("link", name="Audit log", exact=True).click()
    page.wait_for_url("**" + audit_path)
    rows = page.get_by_role("list", name="Audit events").get_by_role("button")
    expect(rows).to_have_count(50)
    assert all(call["body"]["orgId"] == org and not call["body"].get("workspaceId") for call in list_events(calls)), list_events(calls)
    expect(page.get_by_role("list", name="Audit events")).to_contain_text("ledger")

    with page.expect_response(lambda response: response.url.endswith("/ListEvents")) as older:
        page.get_by_role("button", name="Older events", exact=True).click()
    assert older.value.request.post_data_json.get("beforeSeq"), older.value.request.post_data_json
    expect(rows).not_to_have_count(50)
    expect(page).to_have_url(re.compile(r"[?&]before="))
    page.reload()
    expect(rows).not_to_have_count(50)
    page.get_by_role("button", name="Newest events", exact=True).click()
    expect(rows).to_have_count(50)

    separator = page.get_by_role("separator", name="Resize Time", exact=True)
    initial_width = int(separator.get_attribute("aria-valuenow"))
    separator.focus()
    page.keyboard.press("ArrowRight")
    expect(separator).to_have_attribute("aria-valuenow", str(initial_width + 16))
    page.get_by_role("button", name="Live", exact=True).click()
    expect(page).to_have_url(re.compile(r"[?&]live=1"))
    evidence.rpc(api, users["owner"]["token"], "loco.resource.v1.ResourceService/UpdateResource", {"resourceId": state["audit"]["checkout"], "description": "live audit update"})
    with page.expect_response(lambda response: response.url.endswith("/ListEvents"), timeout=15000):
        pass
    expect(rows.first).to_contain_text("Updated a resource")
    page.get_by_role("button", name="Pause", exact=True).click()
    expect(page).not_to_have_url(re.compile(r"[?&]live="))
    page.get_by_role("button", name="Event", exact=True).click()
    expect(page.get_by_text(re.compile(r"^0 of \d+$"))).to_be_visible()
    expect(page.get_by_role("option", name=re.compile(r"^Invited a member"))).to_be_visible()
    page.get_by_role("option", name=re.compile(r"^Created a resource")).click()
    page.keyboard.press("Escape")
    expect(page).to_have_url(re.compile(r"[?&]types=resource\.created"))
    expect(rows).to_have_count(3)
    assert list_events(calls)[-1]["body"]["query"] == "(@type:resource.created)"

    rows.first.click()
    drawer = page.get_by_role("dialog")
    expect(drawer.get_by_role("heading", name="Created a resource", exact=True)).to_be_visible()
    expect(drawer.get_by_role("button", name="Newer event", exact=True)).to_be_disabled()
    expect(drawer.get_by_title("Copy request ID", exact=True)).to_be_visible()
    request_id = drawer.get_by_title("Copy request ID", exact=True).inner_text().strip()
    expect(drawer.get_by_role("button", name=re.compile(r"^More by "))).to_be_visible()
    drawer.get_by_role("button", name="Older event", exact=True).click()
    expect(drawer.get_by_title("Copy request ID", exact=True)).not_to_have_text(request_id)
    request_id = drawer.get_by_title("Copy request ID", exact=True).inner_text().strip()
    expect(drawer.get_by_role("button", name="Newer event", exact=True)).to_be_enabled()
    page.keyboard.press("Escape")
    expect(page).not_to_have_url(re.compile(r"[?&]event="))

    search = page.get_by_role("textbox", name="Search request ID, actor or target", exact=True)
    with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("query") == "(@type:resource.created) ledger"):
        search.fill("ledger")
    expect(rows).to_have_count(1)
    page.get_by_role("button", name="Clear search", exact=True).click()
    expect(rows).to_have_count(3)
    page.get_by_role("button", name="Clear event filter", exact=True).click()
    expect(rows).to_have_count(50)
    # The search reads the whole log: inventory's events are older than the loaded page.
    expect(page.get_by_role("list", name="Audit events")).not_to_contain_text("inventory")
    with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("query") == "INVENTORY"):
        search.fill("INVENTORY")
    expect(page.get_by_role("list", name="Audit events")).to_contain_text("inventory")
    # Typing sends only the query the typing pauses on, so a half-typed field neither reads nor fails.
    typed_from = len(list_events(calls))
    search.press("ControlOrMeta+a")
    with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("query") == "@type:secret.set"):
        search.press_sequentially("@type:secret.set", delay=20)
    typed = [call["body"].get("query") for call in list_events(calls)[typed_from:]]
    assert typed == ["@type:secret.set"], typed
    expect(page.get_by_role("alert")).to_have_count(0)
    with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("query") == "@type:>1") as rejected:
        search.fill("@type:>1")
    assert rejected.value.status == 400, rejected.value.status
    expect(page.get_by_role("alert")).to_contain_text("@type does not compare numbers")
    expect(rows).to_have_count(0)
    expect(search).to_have_attribute("placeholder", "Search, or @type:deployment.* @actor:name")
    expect(search).to_have_attribute("aria-invalid", "true")
    expect(search).to_have_attribute("aria-describedby", "audit-query-error")
    panel = page.locator("#audit-query-error")
    expect(panel.locator("mark")).to_have_text("@type:>1")
    expect(panel.get_by_role("button")).to_have_count(5)
    for field in ("@actor:", "@actor_type:", "@target:", "@type:", "@request:"):
        expect(panel.get_by_role("button", name=re.compile(re.escape(field)))).to_be_visible()
    expect(page.get_by_text("Fix the search to see events.", exact=True)).to_have_count(0)
    with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("query") == "@actor:"):
        panel.get_by_role("button", name=re.compile(r"^@actor:")).click()
    expect(search).to_be_focused()
    expect(search).to_have_value("@actor:")
    assert search.evaluate("node => node.selectionStart") == len("@actor:")
    with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("query") == "@actor:" + users["owner"]["id"]):
        search.press_sequentially(users["owner"]["id"])
    expect(search).to_have_attribute("aria-invalid", "false")
    expect(panel).to_have_count(0)
    expect(rows).to_have_count(50)
    with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("query") == "@wrong:value"):
        search.fill("@wrong:value")
    expect(panel.locator("mark")).to_have_text("@wrong:value")
    page.set_viewport_size(evidence.NARROW)
    expect(panel.get_by_role("button", name=re.compile(r"^@request:"))).to_be_visible()
    page.set_viewport_size(evidence.DESKTOP)

    with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("query") == request_id) as searched:
        search.fill(request_id)
    assert not searched.value.request.post_data_json.get("beforeSeq")
    expect(rows).to_have_count(1)
    expect(page.get_by_role("list", name="Audit events")).to_contain_text("inventory")
    expect(page).to_have_url(re.compile(r"[?&]q=" + request_id))
    page.set_viewport_size({"width": 390, "height": 844})
    expect(search).to_be_visible()
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
    with page.expect_response(lambda response: response.url.endswith("/ListEvents") and "@actor_type:system" in response.request.post_data_json.get("query", "")) as system_events:
        page.get_by_role("option", name="Loco", exact=True).click()
    assert system_events.value.request.post_data_json["query"] == "(@type:resource.created) @actor_type:system"
    expect(page.get_by_text("No events match these filters", exact=True)).to_be_visible()
    search.fill("no-such-event")
    page.get_by_role("button", name="Clear filters", exact=True).click()
    expect(search).to_have_value("")
    expect(page).not_to_have_url(re.compile(r"[?&](actor|types|q)="))
    expect(rows).to_have_count(50)
    page.get_by_role("button", name="Event", exact=True).click()
    page.get_by_role("option", name=re.compile(r"^Created a resource")).click()
    page.keyboard.press("Escape")
    expect(rows).to_have_count(3)
    rows.first.click()
    with page.expect_response(lambda response: response.url.endswith("/ListEvents") and "@actor:" + users["owner"]["id"] in response.request.post_data_json.get("query", "")) as by_actor:
        page.get_by_role("dialog").get_by_role("button", name=re.compile(r"^More by ")).click()
    assert "@actor:" + users["owner"]["id"] in by_actor.value.request.post_data_json["query"]
    expect(page.get_by_role("dialog")).not_to_be_visible()
    expect(page).to_have_url(re.compile(r"[?&]actor=" + users["owner"]["id"]))
    page.reload()
    expect(rows).to_have_count(3)
    assert "@actor:" + users["owner"]["id"] in list_events(calls)[-1]["body"]["query"]

    page.goto(state["ui_url"] + audit_path + f"?range=forever&types=resource.created,unknown.event&ws={shared}&res=00000000-0000-4000-8000-000000000001&env=00000000-0000-4000-8000-000000000002&size=bad&before=bad&q=checkout")
    banner = page.get_by_role("status").filter(has_text="Some filters in this link couldn’t be used")
    expect(banner).to_be_visible()
    for value in ("forever", "unknown.event", "bad", "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"):
        expect(banner).to_contain_text(value)
    expect(rows).to_have_count(1)
    expect(search).to_have_value("checkout")
    expect(page).not_to_have_url(re.compile(r"[?&](range|res|env|size|before)="))
    assert list_events(calls)[-1]["body"]["query"] == "(@type:resource.created) checkout"
    assert list_events(calls)[-1]["body"]["workspaceId"] == shared
    page.get_by_role("button", name="Dismiss", exact=True).click()
    expect(banner).to_have_count(0)
    page.goto(state["ui_url"] + audit_path + "?ws=missing&res=missing&env=missing")
    expect(page.get_by_role("status").filter(has_text="Some filters in this link couldn’t be used")).to_contain_text("Workspace not found")
    expect(rows).to_have_count(50)
    expect(page).not_to_have_url(re.compile(r"[?&](ws|res|env)="))
    page.goto(state["ui_url"] + audit_path + "?env=missing")
    expect(banner).to_contain_text("pick a workspace to filter by environment")
    expect(page.get_by_role("button", name="Environment", exact=True)).to_have_count(0)
    expect(page).not_to_have_url(re.compile(r"[?&]env="))

    field = b"start_time"
    description = b"Time range is outside retention."
    violation = b"\x0a" + bytes([len(field)]) + field + b"\x12" + bytes([len(description)]) + description
    details = base64.b64encode(b"\x0a" + bytes([len(violation)]) + violation).decode()
    request_id = "00000000-0000-4000-8000-000000000003"
    def reject_filters(route):
        route.fulfill(status=400, headers={"x-loco-request-id": request_id, "access-control-expose-headers": "x-loco-request-id"}, json={"code": "invalid_argument", "message": "Invalid filters", "details": [{"type": "google.rpc.BadRequest", "value": details}]})
    page.route("**/loco.event.v1.EventService/ListEvents", reject_filters)
    page.goto(state["ui_url"] + audit_path + "?range=90d")
    alert = page.get_by_role("alert")
    expect(alert.get_by_role("heading")).to_have_text("Couldn’t load events for this time range")
    expect(alert).to_contain_text("start_time")
    expect(alert).to_contain_text("Time range is outside retention.")
    expect(alert.get_by_title("Copy request ID", exact=True)).to_have_text(request_id)
    expect(alert).to_contain_text("EventService.ListEvents")
    expect(alert).to_contain_text("invalid_argument")
    expect(page.locator('[data-slot="table-header"]')).to_have_count(0)
    expect(page.get_by_role("list", name="Audit events")).to_have_count(0)
    expect(page.get_by_role("group", name="Rows per page")).to_have_count(0)
    expect(page.get_by_text("Fix the filters to see events.", exact=True)).to_have_count(0)
    expect(rows).to_have_count(0)
    expect(search).to_be_visible()
    page.context.grant_permissions(["clipboard-read", "clipboard-write"])
    alert.get_by_title("Copy request ID", exact=True).click()
    assert page.evaluate("navigator.clipboard.readText()") == request_id
    expect(alert.get_by_title("Copied", exact=True)).to_be_visible()
    page.set_viewport_size(evidence.NARROW)
    expect(alert.get_by_title("Copied", exact=True)).to_be_visible()
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
    page.set_viewport_size(evidence.DESKTOP)
    page.unroute("**/loco.event.v1.EventService/ListEvents", reject_filters)
    alert.get_by_role("button", name="Reset to Past 7 days", exact=True).click()
    expect(rows).to_have_count(50)
    expect(alert).to_have_count(0)

    assert not errors, errors
    context.close()

    # Workspace admins read only the workspaces they administer, one at a time.
    context, page, calls, errors = open_page(browser, "workspace_admin", f"/org/{org}/wks/{shared}")
    page.get_by_role("link", name="Audit log", exact=True).click()
    rows = page.get_by_role("list", name="Audit events").get_by_role("button")
    expect(rows.first).to_be_visible()
    workspace_filter = page.get_by_role("button", name=re.compile(r"^(shared|sibling)$"))
    expect(workspace_filter).to_have_count(1)
    expect(page.get_by_role("button", name="Clear workspace filter", exact=True)).to_have_count(0)
    expect(page.get_by_role("list", name="Audit events")).not_to_contain_text("ledger")
    workspace_filter.click()
    page.get_by_role("option", name="sibling", exact=True).click()
    expect(page.get_by_role("list", name="Audit events")).to_contain_text("Wren Workspace")
    expect(page.get_by_role("list", name="Audit events")).not_to_contain_text("checkout")
    reads = list_events(calls)
    assert reads and all(call["body"].get("workspaceId") in (shared, sibling) for call in reads), reads
    assert {call["body"]["workspaceId"] for call in reads} == {shared, sibling}, reads
    assert all(call["status"] == 200 for call in reads), reads
    page.get_by_role("button", name="Actor", exact=True).click()
    with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("query") == "@actor:" + users["workspace_admin"]["id"]):
        page.get_by_role("option", name=re.compile("Wren Workspace")).click()
    expect(page.get_by_role("button", name="Clear actor filter", exact=True)).to_be_visible()
    assert list_events(calls)[-1]["body"]["query"] == "@actor:" + users["workspace_admin"]["id"]
    assert list_events(calls)[-1]["body"]["workspaceId"] == sibling

    # Workspace settings link to the log filtered to that workspace, and a resource filter in the URL applies on first load.
    page.goto(state["ui_url"] + f"/org/{org}/wks/{sibling}/settings?scope=workspace")
    page.get_by_role("link", name="Activity in audit log", exact=True).click()
    page.wait_for_url(f"**{audit_path}?range=90d&ws={sibling}")
    expect(page.get_by_role("button", name="Past 90 days", exact=True)).to_be_visible()
    expect(page.get_by_role("button", name="sibling", exact=True)).to_be_visible()
    with page.expect_response(lambda response: response.url.endswith("/ListEvents") and response.request.post_data_json.get("resourceId") == state["audit"]["inventory"] and response.request.post_data_json.get("workspaceId") == sibling) as filtered:
        page.goto(state["ui_url"] + f"{audit_path}?ws={sibling}&res={state['audit']['inventory']}")
    expect(page.get_by_role("button", name="inventory", exact=True)).to_be_visible()
    expect(page.get_by_role("list", name="Audit events")).not_to_contain_text("Wren Workspace")
    first = filtered.value.request.post_data_json
    assert first["workspaceId"] == sibling and first["resourceId"] == state["audit"]["inventory"], first
    assert not errors, errors
    context.close()

    # Members see no entry, and the page itself says only admins can read it.
    context, page, calls, errors = open_page(browser, "member", f"/org/{org}/wks/{shared}")
    page.get_by_role("link", name="Team", exact=True).wait_for()
    expect(page.get_by_role("link", name="Audit log", exact=True)).to_have_count(0)
    page.goto(state["ui_url"] + audit_path)
    expect(page.get_by_text("Only admins can see the audit log", exact=True)).to_be_visible()
    assert not errors, errors
    context.close()
    browser.close()
print("audit log page passed for organization admins, workspace admins and members", flush=True)
