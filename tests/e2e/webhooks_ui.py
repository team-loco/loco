import argparse
import atexit
import contextlib
import importlib.util
import io
import json
import os
import re
import time
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description="Exercise webhook management and server delivery paging in Chromium")
parser.add_argument("--state")
parser.add_argument("--before", action="store_true")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/e2e/out/webhooks-ui"))
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="webhooks-ui", scenario=str(repo / "tests/e2e/fixtures/webhooks/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    print("Preview state:", args.state, flush=True)
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
workspace = state["workspaces"]["acme/shared"]
org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}';")
token = state["users"]["owner"]["token"]
service = "loco.webhook.v1.WebhookService/"

def slow(route):
    time.sleep(0.5)
    route.continue_()

def unavailable(route):
    time.sleep(0.5)
    route.fulfill(status=503, json={"code": "unavailable"})

def call(method, **body):
    return evidence.rpc(state["api_url"], token, service + method, {"workspaceId": workspace, **body})

out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with sync_playwright() as p:
    browser = p.chromium.launch()
    for theme in ("light", "dark"):
        for narrow in (False, True):
            hook = call("CreateWebhook", url="https://hooks.invalid/loco", eventTypes=["resource.created"])["webhook"]
            hook_id = hook["id"]
            # Seed runtime records through the real event-to-delivery trigger. Keep pending
            # attempts in the future so the sender does not change fixture statuses.
            evidence.psql(state["container"], f"""INSERT INTO events(type,workspace_id,actor_type,created_at)
SELECT 'resource.created','{workspace}','system',NOW() - n * INTERVAL '1 minute' FROM generate_series(1,31) n;
UPDATE webhook_deliveries SET status=CASE WHEN mod(event_seq,3)=0 THEN 'failed' WHEN mod(event_seq,3)=1 THEN 'succeeded' ELSE 'pending' END,
next_attempt_at=NOW()+INTERVAL '1 day',attempts=1,last_error='fixture receiver unavailable' WHERE webhook_id='{hook_id}';""")
            context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, color_scheme=theme)
            auth = {"idToken": token, "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS}
            context.add_init_script(f"localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.MODE_KEY)},{json.dumps(theme)});")
            page = context.new_page()
            errors = []
            requests = []
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.on("request", lambda req: requests.append((req.url.rsplit("/", 1)[-1], req.post_data_json)) if service in req.url else None)
            try:
                page.goto(state["ui_url"] + f"/org/{org}/wks/{workspace}/settings?tab=ws")
                page.get_by_role("button", name="Deliveries for https://hooks.invalid/loco", exact=True).click()
                drawer = page.get_by_role("dialog", name="https://hooks.invalid/loco", exact=True)
                deliveries = drawer.get_by_role("list", name="Deliveries", exact=True)
                expect(deliveries.locator("li")).to_have_count(10)
                evidence.settle(page)
                suffix = f"{'before' if args.before else 'after'}-{theme}{'-narrow' if narrow else ''}"
                page.screenshot(path=out / f"webhooks-{suffix}.png", animations="disabled")
                if args.before:
                    continue
                # Reach records beyond the API's old 25-record cutoff.
                # Slow pages keep the loading state visible long enough to catch a disabled, unfocused button.
                page.route("**/" + service + "ListWebhookDeliveries", slow)
                next_page = drawer.get_by_role("button", name="Next page", exact=True)
                next_page.focus()
                for label in ("11–20 of 20+", "21–30 of 30+", "31–31 of 31"):
                    page.keyboard.press("Enter")
                    expect(drawer.get_by_text(label, exact=True)).to_be_visible()
                    if label.endswith("+"):
                        expect(next_page).to_be_focused()
                expect(deliveries.locator("li")).to_have_count(1)
                page.unroute("**/" + service + "ListWebhookDeliveries")
                assert any(method == "ListWebhookDeliveries" and body.get("pageToken") for method, body in requests)
                # A failed background refresh keeps the loaded rows and offers a retry.
                page.route("**/" + service + "ListWebhookDeliveries", unavailable)
                page.route("**/" + service + "SendTestEvent", slow)
                send_test = drawer.get_by_role("button", name="Send test event", exact=True)
                send_test.focus()
                page.keyboard.press("Enter")
                expect(page.get_by_text("Test event queued", exact=True)).to_be_visible()
                expect(send_test).to_be_focused()
                assert any(method == "SendTestEvent" for method, _ in requests)
                expect(drawer.get_by_role("alert")).to_have_text("Could not refresh deliveries.")
                expect(deliveries.locator("li")).to_have_count(1)
                retry = drawer.get_by_role("button", name="Retry deliveries", exact=True)
                retry.focus()
                with page.expect_response("**/" + service + "ListWebhookDeliveries"):
                    page.keyboard.press("Enter")
                expect(retry).to_be_focused()
                page.unroute("**/" + service + "ListWebhookDeliveries")
                retry.click()
                expect(drawer.get_by_role("alert")).to_have_count(0)
                drawer.get_by_role("button", name="Failed", exact=True).click()
                expect(deliveries.locator("li")).to_have_count(10)
                assert all("Failed" in text for text in deliveries.locator("li").all_text_contents())
                assert any(method == "ListWebhookDeliveries" and body.get("status") == "failed" and not body.get("pageToken") for method, body in requests)
                page.route("**/" + service + "RedeliverWebhookDelivery", slow)
                redeliver = deliveries.get_by_role("button", name="Redeliver", exact=True).first
                redeliver.focus()
                page.keyboard.press("Enter")
                # The redelivered row leaves the Failed list once it is pending again.
                expect(redeliver).to_be_focused(timeout=400)
                expect(page.get_by_text("Redelivery queued", exact=True)).to_be_visible()
                assert any(method == "RedeliverWebhookDelivery" for method, _ in requests)
                drawer.get_by_role("button", name="Pending", exact=True).click()
                expect(deliveries.get_by_text(re.compile("^(Pending|Retrying)$")).first).to_be_visible()
                expect(deliveries.get_by_role("button", name="Redeliver", exact=True)).to_have_count(0)
                drawer.get_by_role("button", name="Edit webhook", exact=True).click()
                edit = page.get_by_role("dialog", name="Edit webhook", exact=True)
                url = edit.get_by_role("textbox", name="Endpoint URL", exact=True)
                expect(url).to_have_value("https://hooks.invalid/loco")
                url.fill("invalid")
                edit.get_by_role("button", name="Save changes", exact=True).click()
                expect(url).to_have_attribute("aria-invalid", "true")
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
                rotate.click(timeout=5000)
                expect(rotation.get_by_role("alert")).to_contain_text("grace_period_seconds")
                expect(rotate).to_be_focused()
                assert any(method == "RotateWebhookSecret" and body["gracePeriodSeconds"] == 169 * 3600 for method, body in requests)
                hours.fill("1")
                rotation.get_by_role("button", name="Rotate secret", exact=True).click()
                secret_dialog = page.get_by_role("dialog", name="Signing secret", exact=True)
                secret_input = secret_dialog.get_by_label("Signing secret", exact=True)
                expect(secret_input).to_have_attribute("type", "password")
                assert secret_input.input_value().startswith("whsec_")
                reveal = secret_dialog.get_by_role("button", name="Show", exact=True)
                reveal.click()
                expect(reveal).to_have_attribute("aria-pressed", "true")
                expect(secret_input).to_have_attribute("type", "text")
                secret_dialog.get_by_role("button", name="Done", exact=True).click()
                assert any(method == "RotateWebhookSecret" and body["gracePeriodSeconds"] == 3600 for method, body in requests)
                expect(drawer.get_by_text("Previous secret expires", exact=False)).to_be_visible()
                assert not errors, errors
            except BaseException:
                page.screenshot(path=out / "failure.png", animations="disabled")
                raise
            finally:
                context.close()
                call("DeleteWebhook", webhookId=hook_id)
    browser.close()
print("PASS: webhook edit, rotation, test, redelivery, server status filters and paging in both themes and widths", flush=True)
