import argparse
import re
from pathlib import Path

from harness import expect_rpc, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Test the dashboard summary cards: time ranges, keyboard focus, the rest of the breakdown and a failed metrics load"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/dashboard-totals"))
args = parser.parse_args()
state = load_state(
    args, "dashboard_totals_ui", "tests/ui/fixtures/activity-drawer/scenario.json"
)
org = state["orgs"]["acme"]["id"]
workspace = state["workspaces"]["acme/shared"]
base = f"{state['ui_url']}/org/{org}/wks/{workspace}"
REQUEST_ID = "00000000-0000-4000-8000-000000000009"
METRICS = "**/loco.observability.v1.ObservabilityService/QueryMetrics"
ALLOCATION = "**/loco.resource.v1.ResourceService/GetEnvironmentAllocation"
failing = False


def metrics(route):
    if not failing:
        route.fulfill(json={"series": []})
        return

    route.fulfill(
        status=500,
        headers={
            "x-loco-request-id": REQUEST_ID,
            "access-control-expose-headers": "x-loco-request-id",
        },
        json={"code": "internal", "message": "metrics store down"},
    )


def allocation(route):
    if failing:
        route.fulfill(status=500, json={"code": "internal", "message": "boom"})
    else:
        route.fallback()


out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    page.route(METRICS, metrics)
    page.route(ALLOCATION, allocation)
    listed = []
    page.on(
        "request",
        lambda request: (
            listed.append(request.post_data_json or {})
            if request.url.endswith("/ListDeployments")
            else None
        ),
    )
    with (
        expect_rpc(page, "GetEnvironmentAllocation", lambda body: True),
        expect_rpc(
            page,
            "QueryDeploymentBuckets",
            lambda body: (
                body.get("bucketCount") == 12 and body.get("maxResourceNames") == 3
            ),
        ),
    ):
        page.goto(f"{base}?env=production")
    ranges = page.get_by_role("group", name="Time range", exact=True)
    expect(ranges.get_by_role("button")).to_have_text(["24h", "7d", "30d"])
    with expect_rpc(
        page, "QueryDeploymentBuckets", lambda body: body.get("bucketCount") == 10
    ):
        ranges.get_by_role("button", name="30d", exact=True).click()
    expect(page).to_have_url(re.compile(r"[?&]range=30d"))

    allocated = page.get_by_role("group", name=re.compile(r"^Allocated: "))
    expect(allocated).to_contain_text("GiB memory")
    allocated.focus()
    # The breakdown clips its links until it has opened.
    allocated.evaluate(
        "node => Promise.all(node.getAnimations({subtree: true}).map(animation => animation.finished))"
    )
    more = allocated.get_by_role("link", name=re.compile(r"^3 more"))
    expect(more).to_be_visible()
    expect(more).to_have_attribute("href", re.compile(r"/resources\?.*sort=size\.desc"))
    # The dashboard lists only the deployments card's page of the last day.
    assert listed and all(
        body.get("pageSize") == 10 and "startTime" in body for body in listed
    ), listed
    more.click()
    expect(page).to_have_url(re.compile(r"/resources\?.*sort=size\.desc"))

    failing = True
    page.goto(f"{base}?env=production")
    alert = page.get_by_role("alert").filter(has_text="Couldn’t load metrics")
    expect(alert).to_contain_text("Requests, errors and bandwidth are missing")
    expect(alert.get_by_title("Copy request ID", exact=True)).to_have_text(REQUEST_ID)
    unallocated = page.get_by_role("alert").filter(has_text="Couldn’t load allocation")
    expect(unallocated).to_be_visible()
    expect(page.get_by_role("group", name="Allocated: unavailable")).to_be_visible()
    failing = False
    alert.get_by_role("button", name="Retry", exact=True).click()
    expect(alert).to_have_count(0)
    unallocated.get_by_role("button", name="Retry", exact=True).click()
    expect(unallocated).to_have_count(0)
    expect(allocated).to_contain_text("GiB memory")
    assert not errors, errors
    context.close()
print("PASS: dashboard totals")
