import argparse
import re
from pathlib import Path

from playwright.sync_api import expect
from harness import evidence, load_state, open_page, repo, session

parser = argparse.ArgumentParser(description="Exercise webhook management and server delivery paging in Chromium")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/webhooks-ui"))
args = parser.parse_args()
state = load_state(args, 'webhooks-ui', 'tests/ui/fixtures/webhooks/scenario.json')
workspace = state["workspaces"]["acme/shared"]
org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';")
token = state["users"]["owner"]["token"]
service = "loco.webhook.v1.WebhookService/"

held = []


def slow(route):
    held.append(route)

def unavailable(route):
    route.fulfill(status=503, json={"code": "unavailable"})

def call(method, **body):
    return evidence.rpc(state["api_url"], token, service + method, {"workspaceId": workspace, **body})

out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with session(out) as browser:
    hook = call("CreateWebhook", url="https://hooks.invalid/loco", eventTypes=["resource.created"])["webhook"]
    hook_id = hook["id"]
    # Seed runtime records through the real event-to-delivery trigger. Keep pending
    # attempts in the future so the sender does not change fixture statuses.
    evidence.psql(state["container"], f"""INSERT INTO events(type,workspace_id,actor_type,created_at)
SELECT 'resource.created','{workspace}','system',NOW() - n * INTERVAL '1 minute' FROM generate_series(1,31) n;
UPDATE webhook_deliveries SET status=CASE WHEN mod(event_seq,3)=0 THEN 'failed' WHEN mod(event_seq,3)=1 THEN 'succeeded' ELSE 'pending' END,
next_attempt_at=NOW()+INTERVAL '1 day',attempts=1,last_error='fixture receiver unavailable' WHERE webhook_id='{hook_id}';""")
    context, page, errors = open_page(browser, state=state)
    requests = []
    page.on("request", lambda req: requests.append((req.url.rsplit("/", 1)[-1], req.post_data_json)) if service in req.url else None)
    try:
        page.goto(state["ui_url"] + f"/org/{org}/wks/{workspace}/settings?scope=workspace")
        hook_button = page.get_by_role("button", name="Deliveries for https://hooks.invalid/loco", exact=True)
        cells = hook_button.locator("xpath=ancestor::tr").get_by_role("cell")
        deliveries_cell = cells.nth(2)
        strip = deliveries_cell.get_by_role("img")
        expect(strip).to_be_visible()
        assert strip.bounding_box()["x"] + strip.bounding_box()["width"] <= deliveries_cell.bounding_box()["x"] + deliveries_cell.bounding_box()["width"]
        page.get_by_role("button", name="Deliveries for https://hooks.invalid/loco", exact=True).click()
        drawer = page.get_by_role("dialog", name="https://hooks.invalid/loco", exact=True)
        deliveries = drawer.get_by_role("list", name="Deliveries", exact=True)
        expect(deliveries.locator("li")).to_have_count(25)
        all_filter = drawer.get_by_role("button", name=re.compile(r"^All\b"))
        expect(all_filter).to_have_attribute("aria-pressed", "true")
        total_count = int(evidence.psql(state["container"], f"SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id='{hook_id}';"))
        failing_count = int(evidence.psql(state["container"], f"SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id='{hook_id}' AND (status='failed' OR (status='pending' AND COALESCE(last_error,'')<>''));"))
        expect(all_filter).to_have_text(f"All{total_count}")
        failing_filter = drawer.get_by_role("button", name=re.compile(r"^Failing\b"))
        expect(failing_filter).to_have_text(f"Failing{failing_count}")
        failing_filter.click()
        expect(deliveries.locator("li")).to_have_count(failing_count)
        assert all("Failed" in text or "Retrying" in text for text in deliveries.locator("li").all_text_contents())
        assert any(method == "ListWebhookDeliveries" and body.get("failing") for method, body in requests)
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
        assert any(method == "ListWebhookDeliveries" and body.get("pageToken") for method, body in requests)
        drawer.get_by_role("button", name="50", exact=True).click()
        expect(drawer.get_by_text("1–31 of 31", exact=True)).to_be_visible()
        assert any(method == "ListWebhookDeliveries" and body.get("pageSize") == 50 for method, body in requests)
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
        held.pop().continue_()
        expect(page.get_by_text("Test event queued", exact=True)).to_be_visible()
        expect(send_test).to_be_focused()
        assert any(method == "SendTestEvent" for method, _ in requests)
        expect(drawer.get_by_role("alert")).to_have_text("Could not refresh deliveries.")
        expect(deliveries.locator("li")).to_have_count(25)
        retry = drawer.get_by_role("button", name="Retry deliveries", exact=True)
        retry.focus()
        with page.expect_response("**/" + service + "ListWebhookDeliveries"):
            page.keyboard.press("Enter")
        expect(retry).to_be_focused()
        page.unroute("**/" + service + "ListWebhookDeliveries")
        retry.click()
        expect(drawer.get_by_role("alert")).to_have_count(0)
        drawer.get_by_role("button", name="Failed", exact=True).click()
        failed_count = int(evidence.psql(state["container"], f"SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id='{hook_id}' AND status='failed';"))
        expect(deliveries.locator("li")).to_have_count(failed_count)
        assert all("Failed" in text for text in deliveries.locator("li").all_text_contents())
        assert any(method == "ListWebhookDeliveries" and body.get("status") == "failed" and not body.get("pageToken") for method, body in requests)
        page.route("**/" + service + "RedeliverWebhookDelivery", slow)
        redeliver = deliveries.get_by_role("button", name="Redeliver", exact=True).first
        redeliver.focus()
        with page.expect_request("**/" + service + "RedeliverWebhookDelivery"):
            page.keyboard.press("Enter")
        expect(redeliver).to_be_focused()
        held.pop().continue_()
        expect(page.get_by_text("Redelivery queued", exact=True)).to_be_visible()
        assert any(method == "RedeliverWebhookDelivery" for method, _ in requests)
        drawer.get_by_role("button", name="Pending", exact=True).click()
        expect(deliveries.get_by_text(re.compile("^(Pending|Retrying)$")).first).to_be_visible()
        expect(deliveries.get_by_role("button", name="Redeliver", exact=True)).to_have_count(0)
        all_filter.focus()
        page.keyboard.press("Enter")
        expect(all_filter).to_have_attribute("aria-pressed", "true")
        expect(drawer.get_by_role("button", name="Pending", exact=True)).to_have_attribute("aria-pressed", "false")
        expect(deliveries.locator("li")).to_have_count(25)
        expect(deliveries).to_contain_text("Delivered")
        expect(deliveries).to_contain_text("Failed")
        drawer.get_by_role("button", name="Edit webhook", exact=True).click()
        edit = page.get_by_role("dialog", name="Edit webhook", exact=True)
        url = edit.get_by_role("textbox", name="Endpoint URL", exact=True)
        expect(url).to_have_value("https://hooks.invalid/loco")
        url.fill("http://hooks.invalid/loco")
        expect(edit.get_by_role("button", name="Save changes", exact=True)).to_be_disabled()
        expect(url).to_have_attribute("aria-invalid", "true")
        expect(edit.get_by_text("Use an https:// URL.", exact=True)).to_be_visible()
        url.fill("https://hooks.invalid/updated")
        edit.get_by_role("button", name="All events", exact=True).click()
        page.route("**/" + service + "UpdateWebhook", unavailable)
        edit.get_by_role("button", name="Save changes", exact=True).click()
        expect(edit.get_by_text("Could not save the webhook.", exact=True)).to_be_visible()
        expect(edit.get_by_role("button", name="Save changes", exact=True)).to_be_focused()
        page.unroute("**/" + service + "UpdateWebhook")
        edit.get_by_role("button", name="Save changes", exact=True).click()
        expect(edit).not_to_be_visible()
        updated = call("ListWebhooks")["webhooks"]
        assert next(h for h in updated if h["id"] == hook_id)["url"] == "https://hooks.invalid/updated"
        drawer = page.get_by_role("dialog", name="https://hooks.invalid/updated", exact=True)
        drawer.get_by_role("button", name="Rotate secret", exact=True).click()
        rotation = page.get_by_role("dialog", name="Rotate signing secret", exact=True)
        hours = rotation.get_by_role("textbox", name="Grace period (hours)", exact=True)
        hours.fill("1.0001")
        expect(rotation.get_by_role("button", name="Rotate secret", exact=True)).to_be_disabled()
        hours.fill("169")
        page.route("**/" + service + "RotateWebhookSecret", slow)
        rotate = rotation.get_by_role("button", name="Rotate secret", exact=True)
        with page.expect_request("**/" + service + "RotateWebhookSecret"):
            rotate.click()
        expect(rotate).to_be_focused()
        held.pop().continue_()
        expect(rotation.get_by_role("alert")).to_contain_text("grace_period_seconds")
        expect(rotate).to_be_focused()
        assert any(method == "RotateWebhookSecret" and body["gracePeriodSeconds"] == 169 * 3600 for method, body in requests)
        hours.fill("1")
        with page.expect_request("**/" + service + "RotateWebhookSecret"):
            rotation.get_by_role("button", name="Rotate secret", exact=True).click()
        held.pop().continue_()
        secret_dialog = page.get_by_role("dialog", name="Signing secret", exact=True)
        expect(secret_dialog.get_by_text(re.compile(r"^whsec_"))).to_be_visible()
        secret_dialog.get_by_role("button", name="Done", exact=True).click()
        assert any(method == "RotateWebhookSecret" and body["gracePeriodSeconds"] == 3600 for method, body in requests)
        expect(drawer.get_by_text("Previous secret expires", exact=False)).to_be_visible()
        page.set_viewport_size({"width": 390, "height": 844})
        group = drawer.get_by_role("group", name="Show deliveries", exact=True)
        bounds = drawer.bounding_box()
        for button in group.get_by_role("button").all():
            box = button.bounding_box()
            assert bounds["x"] <= box["x"] and box["x"] + box["width"] <= bounds["x"] + bounds["width"], box

        assert not errors, errors
    except BaseException:
        page.screenshot(path=out / "failure.png", animations="disabled")
        raise
    finally:
        context.close()
        call("DeleteWebhook", webhookId=hook_id)
    browser.close()
print("PASS: webhook edit, rotation, test, redelivery, server status filters and paging", flush=True)
