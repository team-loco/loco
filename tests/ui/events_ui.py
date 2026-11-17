import argparse
import re
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import expect
from harness import evidence, load_state, open_page, repo, session, wait_for_query

parser = argparse.ArgumentParser(description="Exercise runtime event browsing, filters and subscriptions")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/events-ui"))
args = parser.parse_args()
state = load_state(args, "events-ui", "tests/ui/fixtures/events/scenario.json")
workspace = state["workspaces"]["acme/shared"]
resource = state["resources"]["acme/shared/web"]
owner = state["users"]["owner"]["id"]
org = evidence.psql(state["container"], f"SELECT org_id FROM workspaces WHERE id='{workspace}'")
environment = evidence.psql(state["container"], f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND name='production'")
base = f"/org/{org}/wks/{workspace}"
evidence.psql(state["container"], f"""INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,actor_type,actor_id,subject_type,request_id,data,created_at)
SELECT kind,'{org}','{workspace}','{resource}',CASE WHEN kind='resource.degraded' THEN '{environment}'::uuid ELSE NULL END,'user','{owner}',split_part(kind,'.',1),'req-events-ui',
jsonb_build_object('region','us-east-1','environmentId','{environment}') || CASE
WHEN kind='resource.degraded' THEN jsonb_build_object('reason',CASE WHEN n=1 THEN 'crash_looping' ELSE 'out_of_memory' END,'message',CASE WHEN n=1 THEN 'Back-off restarting app' ELSE '' END)
WHEN kind LIKE 'deployment.%' THEN jsonb_build_object('deploymentId','7c41e90a-31d2-4c8e-b5f6-0a9e8d7c6b5a')
ELSE jsonb_build_object('buildId','32c7419e-0000-4000-8000-000000000001','message','Build failed: package installation exited with code 1.') END,NOW()-n*INTERVAL '1 minute'
FROM (VALUES(1,'resource.degraded'),(2,'resource.degraded'),(3,'deployment.ready'),(4,'deployment.failed'),(5,'build.failed')) AS records(n,kind);""")
out = Path(args.out)
with session(out) as browser:
    context, page, errors = open_page(browser, state=state, permissions=["clipboard-read", "clipboard-write"])
    try:
        page.goto(state["ui_url"] + base + "/observability?view=events&live=0")
        events = page.get_by_role("region", name="Events", exact=True)
        expect(events.get_by_role("button", name="Degraded in us-east-1", exact=True)).to_have_count(2)
        expect(events.get_by_role("columnheader", name="Time", exact=True)).to_be_visible()
        expect(events.get_by_role("columnheader", name="Count", exact=True)).to_have_count(0)
        expect(events.get_by_role("button", name="Deploy ready in us-east-1", exact=True)).to_be_visible()
        expect(events.get_by_role("button", name="Deploy failed in us-east-1", exact=True)).to_be_visible()
        expect(events.get_by_text("Out of memory", exact=True)).to_be_visible()
        expect(events.get_by_text("deployment 7c41e90a", exact=True)).to_have_count(2)
        events.get_by_role("button", name=re.compile(r"^Warnings")).click()
        wait_for_query(page, lambda query: query.get("q") == ["@severity:warning"])
        expect(events.get_by_role("button", name="Degraded in us-east-1", exact=True)).to_have_count(2)
        expect(events.get_by_role("button", name="Deploy failed in us-east-1", exact=True)).to_have_count(0)
        events.get_by_role("button", name="Degraded in us-east-1", exact=True).first.click()
        drawer = page.get_by_role("dialog", name="Degraded in us-east-1", exact=True)
        expect(drawer.get_by_role("region", name="Attributes")).to_contain_text("Olivia Owner")
        attributes = drawer.get_by_role("region", name="Attributes")
        expect(attributes).to_contain_text("production")
        expect(attributes).to_contain_text("Restarting after crash")
        expect(attributes.get_by_text("environmentId", exact=True)).to_have_count(0)
        expect(attributes.get_by_text("environment_id", exact=True)).to_have_count(0)
        expect(drawer.get_by_role("button", name="Only events with reason crash_looping", exact=True)).to_be_visible()
        assert drawer.locator("code").first.locator(".sh__line").first.evaluate("el => getComputedStyle(el, '::before').content") == "none"
        expect(drawer.get_by_role("region", name="Raw event")).to_contain_text('"actor_type": "user"')
        wait_for_query(page, lambda query: "event" in query)
        first = parse_qs(urlparse(page.url).query)["event"]
        page.keyboard.press("ArrowDown")
        wait_for_query(page, lambda query: query.get("event") != first)
        page.keyboard.press("ArrowUp")
        wait_for_query(page, lambda query: query.get("event") == first)
        expect(drawer.get_by_role("button", name="Newer event", exact=True)).to_be_disabled()
        drawer.get_by_role("button", name="Copy message", exact=True).click()
        expect(drawer.get_by_role("button", name="Copied", exact=True)).to_be_visible()
        assert page.evaluate("navigator.clipboard.readText()") == "Back-off restarting app"
        subscribe = drawer.get_by_role("link", name="Subscribe", exact=True)
        target = urlparse(subscribe.get_attribute("href"))
        assert target.path == base + "/webhooks"
        assert parse_qs(target.query) == {"new": ["1"], "types": ["resource.degraded"]}
        drawer.get_by_role("button", name="Event actions", exact=True).click()
        expect(page.get_by_role("menuitem", name="Logs around this event", exact=True)).to_be_visible()
        expect(page.get_by_role("menuitem", name="Metrics around this event", exact=True)).to_be_visible()
        assert page.get_by_role("menuitem", name="Open resource", exact=True).get_attribute("href") == base + "/resource/" + resource
        audit = page.get_by_role("menuitem", name="Open in audit log", exact=True)
        assert parse_qs(urlparse(audit.get_attribute("href")).query)["q"] == ["@request:req-events-ui"]
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
        events.get_by_role("button", name="Degraded in us-east-1", exact=True).first.click()
        expect(drawer).to_be_visible()
        drawer.get_by_role("button", name="Only events with region us-east-1", exact=True).click()
        expect(drawer).not_to_be_visible()
        wait_for_query(page, lambda query: "@region:us-east-1" in query.get("q", [""])[0])
        assert not errors, errors
    except BaseException:
        out.mkdir(parents=True, exist_ok=True)
        page.screenshot(path=out / "failure.png", animations="disabled")
        (out / "failure-url.txt").write_text(page.url)
        raise
    finally:
        context.close()
    context, page, errors = open_page(browser, user="reader", state=state)
    try:
        page.goto(state["ui_url"] + base + "/observability?view=events&live=0")
        page.get_by_role("region", name="Events", exact=True).get_by_role("button", name="Degraded in us-east-1", exact=True).first.click()
        drawer = page.get_by_role("dialog", name="Degraded in us-east-1", exact=True)
        expect(drawer).to_be_visible()
        expect(drawer.get_by_role("link", name="Subscribe", exact=True)).to_have_count(0)
        assert not errors, errors
    finally:
        context.close()
print("PASS: ungrouped events, severity, drawer navigation, copying and subscription links", flush=True)
