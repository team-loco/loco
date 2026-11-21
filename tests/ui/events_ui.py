import argparse
import re
import time
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from harness import load_state, open_page, repo, session, sql, wait_for_query
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Exercise runtime event browsing, filters and subscriptions"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/events-ui"))
args = parser.parse_args()
state = load_state(args, "events-ui", "tests/ui/fixtures/events/scenario.json")
workspace = state["workspaces"]["acme/shared"]
resource = state["resources"]["acme/shared/web"]
owner = state["users"]["owner"]["id"]
org = state["orgs"]["acme"]["id"]
environment = sql(
    state,
    f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND slug='production'",
)
base = f"/org/{org}/wks/{workspace}"
sql(
    state,
    f"""INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,deployment_id,actor_type,actor_id,subject_type,request_id,data,created_at)
SELECT kind,'{org}','{workspace}','{resource}',CASE WHEN kind='resource.degraded' OR kind LIKE 'deployment.%' THEN '{environment}'::uuid ELSE NULL END,CASE WHEN kind='deployment.ready' OR (kind='resource.degraded' AND n=1) THEN '7c41e90a-31d2-4c8e-b5f6-0a9e8d7c6b5a'::uuid END,'user','{owner}',split_part(kind,'.',1),'req-events-ui',
jsonb_build_object('region','us-ca-1','environmentId','{environment}') || CASE
WHEN kind='resource.degraded' THEN jsonb_build_object('reason',CASE WHEN n=1 THEN 'restarting' ELSE 'out_of_memory' END,'message',CASE WHEN n=1 THEN 'Back-off restarting app ' || repeat('layer-', 40) || repeat('0123456789abcdef', 12) ELSE 'The service ran out of memory. Increase its memory limit or reduce memory usage.' END)
WHEN kind LIKE 'deployment.%' THEN jsonb_build_object('deploymentId','7c41e90a-31d2-4c8e-b5f6-0a9e8d7c6b5a')
ELSE jsonb_build_object('buildId','32c7419e-0000-4000-8000-000000000001','message','Build failed: package installation exited with code 1.') END,NOW()-n*INTERVAL '1 minute'
FROM (VALUES(1,'resource.degraded'),(2,'resource.degraded'),(3,'deployment.ready'),(4,'deployment.failed'),(5,'build.failed')) AS records(n,kind);""",
)
out = Path(args.out)
with session(out) as browser:
    context, page, errors = open_page(
        browser, state=state, permissions=["clipboard-read", "clipboard-write"]
    )
    now_ms = int(time.time() * 1000)
    page.goto(
        state["ui_url"]
        + base
        + f"/observability?view=events&live=0&from={now_ms - 3 * 3600_000}&to={now_ms - 2 * 3600_000}"
    )
    expect(page.get_by_text("No events in this range", exact=True)).to_be_visible()
    expect(
        page.get_by_role("button", name=re.compile(r"^Show the past"))
    ).to_be_visible()
    expect(page.get_by_role("link", name="New resource", exact=True)).to_have_count(0)
    page.goto(state["ui_url"] + base + "/observability?view=events&live=0")
    events = page.get_by_role("region", name="Events", exact=True)
    expect(
        events.get_by_role("row", name="Degraded in us-ca-1", exact=True)
    ).to_have_count(2)
    expect(events.get_by_role("columnheader", name="Time", exact=True)).to_be_visible()
    expect(events.get_by_role("columnheader", name="Count", exact=True)).to_have_count(
        0
    )
    expect(
        events.get_by_role("row", name="Deploy ready in us-ca-1", exact=True)
    ).to_be_visible()
    expect(
        events.get_by_role("row", name="Deploy failed in us-ca-1", exact=True)
    ).to_be_visible()
    expect(
        events.get_by_text(
            "The service ran out of memory. Increase its memory limit or reduce memory usage.",
            exact=True,
        )
    ).to_be_visible()
    expect(events.get_by_text("deployment 8d7c6b5a", exact=True)).to_have_count(2)
    events.get_by_role("row", name="Deploy ready in us-ca-1", exact=True).click()
    ready = page.get_by_role("dialog", name="Deploy ready in us-ca-1", exact=True)
    link = ready.get_by_role("region", name="Attributes").get_by_role(
        "link", name="7c41e90a-31d2-4c8e-b5f6-0a9e8d7c6b5a", exact=True
    )
    expect(link).to_have_attribute(
        "href", re.compile(rf"{base}/resource/{resource}\?env=production$")
    )
    page.keyboard.press("Escape")
    expect(ready).to_have_count(0)
    events.get_by_role("button", name=re.compile(r"^Warnings")).click()
    wait_for_query(page, lambda query: query.get("q") == ["@severity:warning"])
    expect(
        events.get_by_role("row", name="Degraded in us-ca-1", exact=True)
    ).to_have_count(2)
    expect(
        events.get_by_role("row", name="Deploy failed in us-ca-1", exact=True)
    ).to_have_count(0)
    events.get_by_role("row", name="Degraded in us-ca-1", exact=True).first.click()
    drawer = page.get_by_role("dialog", name="Degraded in us-ca-1", exact=True)
    expect(drawer.get_by_role("region", name="Attributes")).to_contain_text(
        "Olivia Owner"
    )
    attributes = drawer.get_by_role("region", name="Attributes")
    expect(attributes).to_contain_text("production")
    expect(attributes).to_contain_text("restarting")
    expect(attributes.get_by_role("link")).to_have_count(1)
    expect(attributes.get_by_text("environmentId", exact=True)).to_have_count(0)
    expect(attributes.get_by_text("environment_id", exact=True)).to_have_count(0)
    expect(
        drawer.get_by_role(
            "button", name="Only events with reason restarting", exact=True
        )
    ).to_be_visible()
    assert (
        drawer.locator("code")
        .first.locator(".sh__line")
        .first.evaluate("el => getComputedStyle(el, '::before').content")
        == "none"
    )
    raw_event = drawer.get_by_role("region", name="Raw event")
    expect(raw_event).to_contain_text('"actor_type": "user"')
    raw_code = raw_event.locator("pre")
    wrap = raw_event.get_by_role("button", name="Wrap lines", exact=True)
    expect(wrap).to_have_attribute("aria-pressed", "false")
    assert raw_code.evaluate("el => el.scrollWidth > el.clientWidth")
    wrap.click()
    expect(wrap).to_have_attribute("aria-pressed", "true")
    assert raw_code.evaluate("el => el.scrollWidth == el.clientWidth")
    wait_for_query(page, lambda query: "event" in query)
    first = parse_qs(urlparse(page.url).query)["event"]
    page.keyboard.press("ArrowDown")
    wait_for_query(page, lambda query: query.get("event") != first)
    page.keyboard.press("ArrowUp")
    wait_for_query(page, lambda query: query.get("event") == first)
    expect(
        drawer.get_by_role("button", name="Newer event", exact=True)
    ).to_be_disabled()
    drawer.get_by_role("button", name="Copy message", exact=True).click()
    expect(drawer.get_by_role("button", name="Copied", exact=True)).to_be_visible()
    assert page.evaluate("navigator.clipboard.readText()").startswith(
        "Back-off restarting app layer-"
    )
    message = drawer.locator("code").first
    assert message.evaluate(
        "el => el.parentElement.scrollWidth <= el.parentElement.clientWidth"
    )
    subscribe = drawer.get_by_role("link", name="Subscribe", exact=True)
    target = urlparse(subscribe.get_attribute("href"))
    assert target.path == base + "/webhooks"
    assert parse_qs(target.query) == {"new": ["1"], "types": ["resource.degraded"]}
    events_url = page.url
    subscribe.click()
    webhook_dialog = page.get_by_role("dialog", name=re.compile(r"^New webhook"))
    expect(webhook_dialog).to_be_visible()
    webhook_dialog.get_by_role("button", name="Cancel", exact=True).click()
    expect(webhook_dialog).to_have_count(0)
    page.wait_for_url(re.compile(r"/webhooks\?env=production$"))
    page.go_back()
    page.wait_for_url(events_url)
    expect(page.get_by_role("dialog", name=re.compile(r"^New webhook"))).to_have_count(
        0
    )
    drawer.get_by_role("button", name="Event actions", exact=True).click()
    expect(
        page.get_by_role("menuitem", name="Logs around this event", exact=True)
    ).to_be_visible()
    expect(
        page.get_by_role("menuitem", name="Metrics around this event", exact=True)
    ).to_be_visible()
    assert (
        page.get_by_role("menuitem", name="Open resource", exact=True).get_attribute(
            "href"
        )
        == base + "/resource/" + resource
    )
    audit = page.get_by_role("menuitem", name="Open in audit log", exact=True)
    assert parse_qs(urlparse(audit.get_attribute("href")).query)["q"] == [
        "@request:req-events-ui"
    ]
    page.get_by_role("menuitem", name="Copy as JSON", exact=True).click()
    expect(page.get_by_role("menu")).to_have_count(0)
    raw = page.evaluate("navigator.clipboard.readText()")
    assert '"request_id": "req-events-ui"' in raw
    drawer.get_by_role("button", name="Event actions", exact=True).click()
    expect(page.get_by_role("menu")).to_be_visible()
    page.keyboard.press("Escape")
    expect(page.get_by_role("menu")).to_have_count(0)
    expect(drawer).to_be_visible()
    page.keyboard.press("Escape")
    expect(drawer).not_to_be_visible()
    events.get_by_role("row", name="Degraded in us-ca-1", exact=True).first.click()
    expect(drawer).to_be_visible()
    drawer.get_by_role(
        "button", name="Only events with region us-ca-1", exact=True
    ).click()
    expect(drawer).not_to_be_visible()
    wait_for_query(page, lambda query: "@region:us-ca-1" in query.get("q", [""])[0])
    assert not errors, errors
    context.close()
    context, page, errors = open_page(browser, user="reader", state=state)
    page.goto(state["ui_url"] + base + "/observability?view=events&live=0")
    page.get_by_role("region", name="Events", exact=True).get_by_role(
        "row", name="Degraded in us-ca-1", exact=True
    ).first.click()
    drawer = page.get_by_role("dialog", name="Degraded in us-ca-1", exact=True)
    expect(drawer).to_be_visible()
    expect(drawer.get_by_role("link", name="Subscribe", exact=True)).to_have_count(0)
    assert not errors, errors
    context.close()
print(
    "PASS: ungrouped events, severity, drawer navigation, copying and subscription links",
    flush=True,
)
