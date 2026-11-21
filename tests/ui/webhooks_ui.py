import argparse
import re
from pathlib import Path

from harness import call, expect_rpc, load_state, open_page, repo, session, sql
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Exercise webhook management and server delivery paging in Chromium"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/webhooks-ui"))
args = parser.parse_args()
state = load_state(args, "webhooks-ui", "tests/ui/fixtures/webhooks/scenario.json")
workspace = state["workspaces"]["acme/shared"]
org = state["orgs"]["acme"]["id"]
service = "loco.webhook.v1.WebhookService/"

held = []


def slow(route):
    held.append(route)


def unavailable(route):
    route.fulfill(status=503, json={"code": "unavailable"})


out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with session(out) as browser:
    context, page, errors = open_page(
        browser, state=state, permissions=["clipboard-read", "clipboard-write"]
    )
    requests = []
    page.on(
        "request",
        lambda req: (
            requests.append((req.url.rsplit("/", 1)[-1], req.post_data_json))
            if service in req.url
            else None
        ),
    )
    page.goto(state["ui_url"] + f"/org/{org}/wks/{workspace}/webhooks")
    expect(page.get_by_role("link", name="Webhooks", exact=True)).to_be_visible()
    # A workspace without webhooks offers the first one and the webhook docs.
    hero = page.locator("section").filter(
        has=page.get_by_role("heading", name="No webhooks yet", exact=True)
    )
    expect(hero.get_by_role("link", name="Docs", exact=True)).to_have_attribute(
        "href", re.compile(r"/operations/webhooks/$")
    )
    hero.get_by_role("button", name="New webhook", exact=True).click()
    first = page.get_by_role("dialog", name="New webhook in shared", exact=True)
    # The thumb slides across inside its group even though the dialog grows and moves when the picker opens.
    frames = first.get_by_role("group", name="Events", exact=True).evaluate(
        """group => new Promise(resolve => {
            const frames = [];
            group.querySelector('[aria-pressed="false"]').click();
            const start = performance.now();
            const tick = () => {
                const thumb = group.querySelector('[data-slot="segmented-indicator"]');
                if (thumb) {
                    const box = thumb.getBoundingClientRect(), root = group.getBoundingClientRect();
                    frames.push([box.top - root.top, box.left - root.left]);
                }
                if (performance.now() - start < 400) requestAnimationFrame(tick);
                else resolve(frames);
            };
            requestAnimationFrame(tick);
        })"""
    )
    tops = [top for top, _ in frames]
    lefts = {round(left) for _, left in frames}
    assert tops and max(tops) - min(tops) < 1, frames
    assert len(lefts) > 2, frames
    first.get_by_role("button", name="Cancel", exact=True).click()
    expect(first).to_have_count(0)
    hook = call(
        state,
        service + "CreateWebhook",
        {
            "workspaceId": workspace,
            "url": "https://hooks.invalid/loco",
            "eventTypes": ["resource.created"],
        },
    )["webhook"]
    hook_id = hook["id"]
    # Seed runtime records through the real event-to-delivery trigger. Keep pending
    # attempts in the future so the sender does not change fixture statuses.
    sql(
        state,
        f"""INSERT INTO events(type,workspace_id,actor_type,created_at)
SELECT 'resource.created','{workspace}','system',NOW() - n * INTERVAL '1 minute' FROM generate_series(1,31) n;
UPDATE webhook_deliveries SET status=CASE WHEN mod(event_seq,3)=0 THEN 'failed' WHEN mod(event_seq,3)=1 THEN 'succeeded' ELSE 'pending' END,
next_attempt_at=NOW()+INTERVAL '1 day',attempts=1,last_error='fixture receiver unavailable' WHERE webhook_id='{hook_id}';""",
    )
    page.reload()
    search = page.get_by_role("textbox", name="Search webhooks", exact=True)
    search.fill("no-match")
    expect(page.get_by_text("No webhooks match.", exact=True)).to_be_visible()
    search.fill("resource.created")
    hook_button = page.get_by_role(
        "button", name="Deliveries for https://hooks.invalid/loco", exact=True
    )
    cells = hook_button.locator("xpath=ancestor::tr").get_by_role("cell")
    deliveries_cell = cells.nth(2)
    strip = deliveries_cell.get_by_role("img")
    expect(strip).to_be_visible()
    assert (
        strip.bounding_box()["x"] + strip.bounding_box()["width"]
        <= deliveries_cell.bounding_box()["x"] + deliveries_cell.bounding_box()["width"]
    )
    page.get_by_role(
        "button", name="Deliveries for https://hooks.invalid/loco", exact=True
    ).click()
    drawer = page.get_by_role("dialog", name="hooks.invalid", exact=True)
    fields = drawer.locator("dl").first
    values = fields.locator("dd")
    url_copy = fields.get_by_role(
        "button", name="https://hooks.invalid/loco", exact=True
    )
    id_copy = fields.get_by_role("button", name=hook_id, exact=True)
    for control, expected in (
        (url_copy, "https://hooks.invalid/loco"),
        (id_copy, hook_id),
    ):
        control.click()
        assert page.evaluate("navigator.clipboard.readText()") == expected
    left = values.nth(2).bounding_box()["x"]
    assert abs(url_copy.locator("span").first.bounding_box()["x"] - left) < 1
    assert abs(id_copy.locator("span").first.bounding_box()["x"] - left) < 1
    deliveries = drawer.get_by_role("list", name="Deliveries", exact=True)
    expect(deliveries.locator("li")).to_have_count(25)
    all_filter = drawer.get_by_role("button", name=re.compile(r"^All\b"))
    expect(all_filter).to_have_attribute("aria-pressed", "true")
    total_count = int(
        sql(
            state,
            f"SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id='{hook_id}';",
        )
    )
    failing_count = int(
        sql(
            state,
            f"SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id='{hook_id}' AND (status='failed' OR (status='pending' AND COALESCE(last_error,'')<>''));",
        )
    )
    expect(all_filter).to_have_text(f"All{total_count}")
    failing_filter = drawer.get_by_role("button", name=re.compile(r"^Failing\b"))
    expect(failing_filter).to_have_text(f"Failing{failing_count}")
    failing_filter.click()
    expect(deliveries.locator("li")).to_have_count(failing_count)
    assert all(
        "Failed" in text or "Retrying" in text
        for text in deliveries.locator("li").all_text_contents()
    )
    assert any(
        method == "ListWebhookDeliveries" and body.get("failing")
        for method, body in requests
    )
    all_filter.click()
    expect(deliveries.locator("li")).to_have_count(25)

    # Hold requests while checking keyboard focus during loading.
    page.route("**/" + service + "ListWebhookDeliveries", slow)
    next_page = drawer.get_by_role("button", name="Next page", exact=True)
    next_page.focus()
    with page.expect_request("**/" + service + "ListWebhookDeliveries"):
        page.keyboard.press("Enter")
    expect(next_page).to_be_focused()
    held.pop().continue_()
    expect(drawer.get_by_text("26–31 of 31", exact=True)).to_be_visible()
    expect(deliveries.locator("li")).to_have_count(6)
    page.unroute("**/" + service + "ListWebhookDeliveries")
    assert any(
        method == "ListWebhookDeliveries" and body.get("pageToken")
        for method, body in requests
    )
    drawer.get_by_role("button", name="50", exact=True).click()
    expect(drawer.get_by_text("1–31 of 31", exact=True)).to_be_visible()
    assert any(
        method == "ListWebhookDeliveries" and body.get("pageSize") == 50
        for method, body in requests
    )
    drawer.get_by_role("button", name="25", exact=True).click()
    expect(deliveries.locator("li")).to_have_count(25)
    # A failed background refresh keeps the loaded rows and offers a retry.
    page.route("**/" + service + "ListWebhookDeliveries", unavailable)
    page.route("**/" + service + "SendTestEvent", slow)
    send_test = drawer.get_by_role("button", name="Send test event", exact=True)
    send_test.focus()
    with page.expect_request("**/" + service + "SendTestEvent"):
        page.keyboard.press("Enter")
    expect(send_test).to_be_focused()
    # A test event that reaches the server queues a delivery the sender fails against
    # hooks.invalid, which changes the Failing count checked below.
    held.pop().fulfill(json={})
    expect(page.get_by_text("Sent a test event", exact=True)).to_be_visible()
    expect(send_test).to_be_focused()
    assert any(method == "SendTestEvent" for method, _ in requests)
    expect(drawer.get_by_role("alert")).to_have_text("Could not refresh deliveries.")
    expect(deliveries.locator("li")).to_have_count(25)
    retry = drawer.get_by_role("button", name="Retry deliveries", exact=True)
    retry.focus()
    with expect_rpc(
        page, "ListWebhookDeliveries", lambda body: body.get("webhookId") == hook_id
    ):
        page.keyboard.press("Enter")
    expect(retry).to_be_focused()
    page.unroute("**/" + service + "ListWebhookDeliveries")
    retry.click()
    expect(drawer.get_by_role("alert")).to_have_count(0)
    failing_filter.click()
    expect(deliveries.locator("li")).to_have_count(failing_count)
    page.route("**/" + service + "RedeliverWebhookDelivery", slow)
    redeliver = deliveries.get_by_role(
        "button", name="Redeliver resource.created", exact=True
    ).first
    redeliver.focus()
    with page.expect_request("**/" + service + "RedeliverWebhookDelivery"):
        page.keyboard.press("Enter")
    expect(redeliver).to_be_focused()
    held.pop().continue_()
    expect(
        page.get_by_text("Redelivering resource.created", exact=True)
    ).to_be_visible()
    assert any(method == "RedeliverWebhookDelivery" for method, _ in requests)
    all_filter.click()
    expect(deliveries.locator("li")).to_have_count(25)
    drawer.get_by_role("button", name="Actions for https://hooks.invalid/loco").click()
    page.get_by_role("menuitem", name="Edit", exact=True).click()
    edit = page.get_by_role("dialog", name="Edit webhook", exact=True)
    url = edit.get_by_role("textbox", name="Endpoint URL", exact=True)
    expect(url).to_have_value("https://hooks.invalid/loco")
    url.fill("http://hooks.invalid/loco")
    expect(edit.get_by_role("button", name="Save", exact=True)).to_be_disabled()
    expect(url).to_have_attribute("aria-invalid", "true")
    expect(edit.get_by_text("Use an https:// URL.", exact=True)).to_be_visible()
    url.fill("https://hooks.invalid/updated")
    edit.get_by_role("button", name="All events", exact=True).click()
    page.route("**/" + service + "UpdateWebhook", unavailable)
    edit.get_by_role("button", name="Save", exact=True).click()
    expect(edit.get_by_text("Could not save the webhook.", exact=True)).to_be_visible()
    expect(edit.get_by_role("button", name="Save", exact=True)).to_be_focused()
    page.unroute("**/" + service + "UpdateWebhook")
    edit.get_by_role("button", name="Save", exact=True).click()
    expect(edit).not_to_be_visible()
    updated = call(state, service + "ListWebhooks", {"workspaceId": workspace})[
        "webhooks"
    ]
    assert (
        next(h for h in updated if h["id"] == hook_id)["url"]
        == "https://hooks.invalid/updated"
    )
    drawer = page.get_by_role("dialog", name="hooks.invalid", exact=True)
    drawer.get_by_role(
        "button", name="Actions for https://hooks.invalid/updated"
    ).click()
    page.get_by_role("menuitem", name="Rotate signing secret", exact=True).click()
    rotation = page.get_by_role("dialog", name="Rotate signing secret", exact=True)
    grace = rotation.get_by_role("group", name="Grace period", exact=True)
    assert grace.bounding_box()["width"] < rotation.bounding_box()["width"] * 0.75
    rotation.get_by_role("button", name="1 hour", exact=True).click()
    page.route("**/" + service + "RotateWebhookSecret", slow)
    with page.expect_request("**/" + service + "RotateWebhookSecret"):
        rotation.get_by_role("button", name="Rotate", exact=True).click()
    held.pop().continue_()
    secret_dialog = page.get_by_role("dialog", name="New signing secret", exact=True)
    expect(secret_dialog.get_by_text(re.compile(r"^whsec_"))).to_be_visible()
    secret_dialog.get_by_role("button", name="Done", exact=True).click()
    assert any(
        method == "RotateWebhookSecret" and body["gracePeriodSeconds"] == 3600
        for method, body in requests
    )
    expect(
        drawer.get_by_text("Also signed with the previous secret until", exact=False)
    ).to_be_visible()
    page.set_viewport_size({"width": 390, "height": 844})
    group = drawer.get_by_role("group", name="Show deliveries", exact=True)
    bounds = drawer.bounding_box()
    for button in group.get_by_role("button").all():
        box = button.bounding_box()
        assert (
            bounds["x"] <= box["x"]
            and box["x"] + box["width"] <= bounds["x"] + bounds["width"]
        ), box

    drawer.get_by_role("button", name="Close", exact=True).click()
    page.goto(
        state["ui_url"]
        + f"/org/{org}/wks/{workspace}/webhooks?new=1&types=build.failed"
    )
    creation = page.get_by_role("dialog", name="New webhook in shared", exact=True)
    expect(
        creation.get_by_role("button", name="Choose events", exact=True)
    ).to_have_attribute("aria-pressed", "true")
    builds = creation.get_by_role("checkbox", name=re.compile(r"^Builds "))
    expect(builds).to_have_attribute("aria-checked", "mixed")
    expect(builds).to_have_accessible_name(re.compile(r"^Builds 1 of \d+$"))
    expect(creation.get_by_text(re.compile(r"^1 of \d+ events$"))).to_be_visible()
    name = creation.get_by_role("textbox", name="Name (optional)", exact=True)
    expect(name).to_be_focused()
    expect(name).to_have_accessible_description("Shown in the webhook list.")
    expect(creation.get_by_role("textbox").first).to_have_accessible_name(
        "Name (optional)"
    )
    endpoint = creation.get_by_role("textbox", name="Endpoint URL", exact=True)
    endpoint.fill("https://127.0.0.1/hook")
    expect(
        creation.get_by_text("Private and reserved addresses are blocked.", exact=True)
    ).to_be_visible()
    expect(
        creation.get_by_role("button", name="Create webhook", exact=True)
    ).to_be_disabled()
    endpoint.fill("https://hooks.invalid/named")
    name.fill("Billing alerts")
    creation.get_by_role("button", name="Create webhook", exact=True).click()
    page.get_by_role("dialog", name="Signing secret", exact=True).get_by_role(
        "button", name="Done", exact=True
    ).click()
    named = page.get_by_role(
        "button", name="Deliveries for https://hooks.invalid/named", exact=True
    )
    expect(named).to_contain_text("Billing alerts")
    expect(named).to_contain_text("hooks.invalid/named")
    expect(
        page.get_by_role("button", name="Deliveries for https://hooks.invalid/updated")
    ).to_contain_text("hooks.invalid")
    page.get_by_role("textbox", name="Search webhooks", exact=True).fill("billing")
    expect(named).to_be_visible()
    expect(page.get_by_role("row")).to_have_count(2)
    named.click()
    expect(
        page.get_by_role("dialog", name="Billing alerts", exact=True)
    ).to_be_visible()
    page.keyboard.press("Escape")
    # At the workspace's webhook limit the header says so and blocks a new one.
    sql(
        state,
        f"INSERT INTO org_limit_overrides(org_id, webhooks_per_workspace, reason) VALUES('{org}', 2, 'fixture');",
    )
    try:
        page.reload()
        expect(
            page.get_by_role("link", name="Workspace limit reached", exact=True)
        ).to_be_visible()
        expect(
            page.get_by_role("button", name="New webhook", exact=True)
        ).to_be_disabled()
    finally:
        sql(state, f"DELETE FROM org_limit_overrides WHERE org_id='{org}';")
    assert not errors, errors
    context.close()
    browser.close()
print(
    "PASS: webhook empty state, limit, names, event picker, edit, rotation, test, redelivery, server status filters and paging",
    flush=True,
)
