import argparse
import datetime
import json
import re
import urllib.parse
from pathlib import Path

from activity_fixture import prepare_activity, verify_activity_access
from harness import call, evidence, expect_rpc, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Exercise scoped audit Activity with a throwaway API and database"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/activity-ui"))
args = parser.parse_args()
state = load_state(args, "activity_ui", "tests/ui/fixtures/activity/scenario.json")
activity = prepare_activity(state)
state["activity"] = activity
evidence.write_json(Path(args.state), state)
verify_activity_access(state, activity)
origin = state["ui_url"]
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
base = f"/org/{activity['orgId']}/wks/{activity['workspaceId']}"
results = []
VIEWPORT = {"width": 1440, "height": 1000}


def check_scoped_user(browser):
    """A member whose only grant is read on one resource sees it on every page without a refused request."""
    rid = activity["resourceId"]
    environments = call(
        state,
        "loco.environment.v1.EnvironmentService/ListEnvironments",
        {"workspaceId": activity["workspaceId"]},
    )["environments"]
    production = next(e["id"] for e in environments if e["slug"] == "production")
    now = datetime.datetime.now(datetime.UTC)
    stamp = (now - datetime.timedelta(minutes=1)).strftime("%Y-%m-%dT%H:%M:%S.000Z")
    telemetry = {
        "QueryLogBuckets": {
            "buckets": [{"startTime": stamp, "level": "LOG_LEVEL_INFO", "count": "1"}]
        },
        "QueryLogs": {
            "entries": [
                {
                    "timestamp": stamp,
                    "level": "LOG_LEVEL_INFO",
                    "body": "checkout order placed",
                    "resourceId": rid,
                    "resourceAttributes": {
                        "loco.region": "us-ca-1",
                        "k8s.pod.name": "checkout-7d9f-x2k4p",
                    },
                }
            ]
        },
        "ListTraces": {
            "totalCount": "1",
            "summaries": [
                {
                    "traceId": "0af7651916cd43dd8448eb211c80319c",
                    "rootSpanName": "POST /api/checkout",
                    "serviceName": "checkout",
                    "startTime": stamp,
                    "durationNanos": "12000000",
                    "status": "TRACE_STATUS_OK",
                    "spanCount": "1",
                    "resourceId": rid,
                }
            ],
        },
        "QueryMetrics": {
            "series": [
                {"resourceId": rid, "points": [{"timestamp": stamp, "value": 0.25}]}
            ]
        },
        "QueryTraceBuckets": {
            "buckets": [],
            "total": {
                "traceCount": "1",
                "p50DurationNanos": "12000000",
                "p95DurationNanos": "12000000",
            },
        },
    }
    back_off = {
        "seq": "1",
        "id": "01a1c8b3-0000-7000-8000-00000000b0ff",
        "type": "resource.degraded",
        "orgId": activity["orgId"],
        "workspaceId": activity["workspaceId"],
        "resourceId": rid,
        "environmentId": production,
        "createdAt": stamp,
        "title": "Degraded in us-ca-1",
        "severity": "warning",
        "region": "us-ca-1",
        "data": {
            "reason": "restarting",
            "message": "Back-off restarting failed container",
            "placementId": "checkout-us-ca-1",
            "region": "us-ca-1",
        },
    }
    rollout = {
        "seq": "2",
        "id": "01a1c8b3-0000-7000-8000-00000000d001",
        "type": "deployment.created",
        "orgId": activity["orgId"],
        "workspaceId": activity["workspaceId"],
        "resourceId": rid,
        "createdAt": stamp,
        "title": "Deploy created",
        "severity": "normal",
        "data": {"resourceId": rid, "environmentId": production},
    }
    other_rollout = dict(
        rollout,
        id="01a1c8b3-0000-7000-8000-00000000d002",
        type="resource.scaled",
        title="Scaled",
        data={"environmentId": "01a1c8b3-0000-7000-8000-0000000000ff", "replicas": 3},
    )
    down = {
        "environmentId": production,
        "environmentName": "production",
        "status": "RESOURCE_STATUS_UNAVAILABLE",
        "regions": [
            {
                "region": "us-ca-1",
                "status": "RESOURCE_STATUS_UNAVAILABLE",
                "desiredReplicas": 1,
                "message": "Back-off restarting failed container",
            }
        ],
    }
    pages = [
        (
            "dashboard",
            base,
            lambda page: expect(
                page.get_by_role("list", name="Your resources").get_by_role(
                    "link", name="checkout", exact=True
                )
            ).to_be_visible(),
        ),
        (
            "resources",
            base + "/resources",
            lambda page: expect(
                page.get_by_role("link", name="checkout", exact=True).first
            ).to_be_visible(),
        ),
        (
            "resource",
            base + "/resource/" + rid,
            lambda page: expect(
                page.get_by_label("Last log lines in us-ca-1")
            ).to_contain_text("checkout order placed"),
        ),
        (
            "logs",
            base + "/observability?view=logs",
            lambda page: expect(
                page.get_by_text("checkout order placed").first
            ).to_be_visible(),
        ),
        (
            "metrics",
            base + "/observability?view=metrics",
            lambda page: expect(
                page.get_by_role("heading", name="Metrics")
            ).to_be_visible(),
        ),
        (
            "traces",
            base + "/observability?view=traces",
            lambda page: expect(
                page.get_by_text("POST /api/checkout").first
            ).to_be_visible(),
        ),
        (
            "events",
            base + "/observability?view=events",
            lambda page: (
                expect(
                    page.get_by_text("Back-off restarting failed container").first
                ).to_be_visible(),
                expect(page.get_by_text("Deploy created", exact=True)).to_be_visible(),
                expect(page.get_by_text("Scaled", exact=True)).to_have_count(0),
            ),
        ),
        (
            "settings",
            base + "/settings?scope=workspace",
            lambda page: expect(
                page.get_by_text(
                    "You have access to 1 resource in this workspace", exact=True
                )
            ).to_be_visible(),
        ),
        (
            "team",
            f"/org/{activity['orgId']}/team",
            lambda page: expect(
                page.get_by_role("heading", name="Team")
            ).to_be_visible(),
        ),
        (
            "tokens",
            "/tokens",
            lambda page: expect(
                page.get_by_role("heading", name="Tokens")
            ).to_be_visible(),
        ),
        (
            "sibling",
            f"/org/{activity['orgId']}/wks/{activity['siblingId']}",
            lambda page: expect(
                page.get_by_text(
                    "You have access to 0 resources in this workspace", exact=True
                )
            ).to_be_visible(),
        ),
    ]
    checked = []
    for scene, path, visible in pages:
        context, page, errors = open_page(
            browser, "scoped", state=state, now=now, viewport=VIEWPORT
        )
        recorded = []

        def route_request(route, *, recorded=recorded, scene=scene):
            url = urllib.parse.urlparse(route.request.url)
            url = url._replace(path=url.path.removeprefix("/api"))
            if not url.path.startswith("/loco."):
                route.continue_()
                return
            method = url.path.rsplit("/", 1)[-1]
            # Streams stay open, so fetching one would never return.
            if method in ("TailLogs", "StreamEvents"):
                route.continue_()
                return
            body = route.request.post_data_json
            if method in telemetry:
                # The proxy serves a caller without workspace read only for resources it names and reads.
                named = body.get("scope", body).get("resourceIds") or []
                status = (
                    200
                    if named
                    and set(named) == {rid}
                    and (body.get("scope", body)).get("environmentId") == production
                    else 403
                )
                recorded.append({"method": method, "body": body, "status": status})
                if status == 200:
                    route.fulfill(json=telemetry[method])
                else:
                    route.fulfill(
                        status=403,
                        json={
                            "code": "permission_denied",
                            "message": "permission denied",
                        },
                    )
                return
            response = route.fetch()
            recorded.append({"method": method, "body": body, "status": response.status})
            if (
                method == "ListEvents"
                and scene in ("resource", "events")
                and body.get("environmentId")
                and response.status == 200
            ):
                # The cluster reports restarts as runtime events; this resource never ran.
                # Deploys and scaling carry their environment only in their data.
                listed = response.json()
                extra = [rollout, other_rollout] if scene == "events" else []
                listed["events"] = [back_off, *extra, *listed.get("events", [])]
                route.fulfill(response=response, json=listed)
            elif (
                method == "GetResource"
                and scene == "resource"
                and response.status == 200
            ):
                resource = response.json()
                resource["resource"]["environmentStatuses"] = [down]
                route.fulfill(response=response, json=resource)
            else:
                route.fulfill(response=response)

        context.route("**/*", route_request)
        page.on(
            "console",
            lambda message, *, errors=errors: (
                message.type == "error" and errors.append(message.text)
            ),
        )
        if scene == "metrics":
            with expect_rpc(
                page,
                "QueryMetrics",
                lambda body: body.get("environmentId") == production,
            ):
                page.goto(origin + path)
        else:
            page.goto(origin + path)
        visible(page)
        refused = [request for request in recorded if request["status"] >= 400]
        assert not refused, (scene, refused)
        assert not errors, (scene, errors)
        if scene in ("logs", "metrics", "traces", "resource"):
            queries = [
                request for request in recorded if request["method"] in telemetry
            ]
            assert queries and all(request["status"] == 200 for request in queries), (
                scene,
                queries,
            )
        if scene == "events":
            assert any(
                request["method"] == "ListEvents"
                and request["body"].get("readableResources")
                and not request["body"].get("resourceId")
                and request["status"] == 200
                for request in recorded
            ), recorded
        if scene == "dashboard":
            expect(page.locator("nav[aria-label=breadcrumb]")).to_contain_text("shared")
            # Environment-wide traffic needs workspace read; the scoped view shows none.
            assert not [
                request for request in recorded if request["method"] == "QueryMetrics"
            ], recorded
        checked.append({"scene": scene, "requests": recorded})
        context.unroute_all(behavior="wait")
        context.close()
    return checked


with session(out) as browser:
    for user, scene, path in [
        ("owner", "resource-owner", base + "/resource/" + activity["resourceId"]),
        ("member", "org-member", base + "/settings?scope=org"),
        ("workspace_admin", "workspace", base + "/settings?scope=workspace"),
    ]:
        context, page, errors = open_page(browser, user, state=state, viewport=VIEWPORT)
        recorded = []

        def route_request(route, *, recorded=recorded):
            url = urllib.parse.urlparse(route.request.url)
            url = url._replace(path=url.path.removeprefix("/api"))
            if url.path.startswith("/loco."):
                method = url.path.rsplit("/", 1)[-1]
                # Streams stay open, so fetching one would never return.
                if method == "StreamEvents":
                    route.continue_()
                    return
                assert method.startswith(("Get", "List", "WhoAmI", "Query")), method
                body = route.request.post_data_json
                response = route.fetch()
                recorded.append(
                    {"method": method, "body": body, "status": response.status}
                )
                route.fulfill(response=response)
                return
            route.continue_()

        context.route("**/*", route_request)
        if user == "member":
            with (
                expect_rpc(
                    page, "GetOrg", lambda body: body.get("orgId") == activity["orgId"]
                ),
                expect_rpc(
                    page,
                    "ListOrgMembers",
                    lambda body: body.get("orgId") == activity["orgId"],
                ),
            ):
                page.goto(origin + path)
        else:
            page.goto(origin + path)
        ready = (
            page.get_by_role("heading", name="checkout", exact=True)
            if scene.startswith("resource")
            else page.get_by_role("textbox", name="Name", exact=True)
        )
        expect(ready).to_be_visible()
        if user == "member":
            assert any(
                request["method"] == "GetOrg" and request["status"] == 200
                for request in recorded
            )
            assert any(
                request["method"] == "ListOrgMembers" and request["status"] == 200
                for request in recorded
            )
            assert not any(request["method"] == "ListEvents" for request in recorded)
        if user == "workspace_admin":
            expect(page.locator("nav[aria-label=breadcrumb]")).to_contain_text("shared")
            breadcrumb = page.locator("nav[aria-label=breadcrumb]").inner_text()
            assert "shared" in breadcrumb and "other-company" not in breadcrumb
            scopes = page.get_by_role("group", name="Settings for", exact=True)
            expect(
                scopes.get_by_role("button", name="Workspace", exact=True)
            ).to_have_attribute("aria-pressed", "true")
            expect(
                scopes.get_by_role("button", name="Organization", exact=True)
            ).to_have_count(0)
            expect(
                page.get_by_role("region", name="Activity", exact=True)
            ).to_have_count(0)
            forbidden = [
                request
                for request in recorded
                if request["method"] in ("GetOrg", "ListOrgMembers")
                or (
                    request["method"] == "ListOrgWorkspaces"
                    and request["body"].get("orgId") == activity["orgId"]
                )
            ]
            assert not forbidden, forbidden
        if user == "owner":
            page.goto(origin + base + "/settings?scope=workspace")
            page.get_by_role("link", name="Activity in audit log", exact=True).click()
            page.wait_for_url(
                "**/org/"
                + activity["orgId"]
                + "/audit?range=90d&ws="
                + activity["workspaceId"]
            )
            page.get_by_role("table", name="Audit events").locator(
                "tbody tr[data-interactive]"
            ).first.wait_for()
            expect(
                page.get_by_role("button", name="shared", exact=True).last
            ).to_be_visible()
            page.goto(origin + base + "/resource/" + activity["resourceId"])
            page.get_by_role("button", name="Events", exact=True).wait_for()
            environment_switcher = page.get_by_title("Switch environment", exact=True)
            environment_switcher.click()
            page.get_by_role("menuitem", name="staging", exact=False).click()
            page.keyboard.press("Escape")
            # The app blanks a focused trigger's title while it shows the title as a tooltip.
            expect(
                page.get_by_role("navigation", name="breadcrumb").get_by_role(
                    "button", name="staging", exact=True
                )
            ).to_be_visible()
            expect(page).to_have_url(re.compile(r"[?&]env=staging"))
            page.get_by_title("Switch workspace", exact=True).click()
            page.get_by_role("menuitem", name="sibling", exact=False).click()
            page.keyboard.press("Escape")
            page.wait_for_url("**/wks/" + activity["siblingId"] + "**")
            assert not [request for request in recorded if request["status"] >= 400], (
                recorded
            )
        assert not errors, errors
        results.append(
            {"user": user, "scene": scene, "requests": recorded, "errors": errors}
        )
        context.unroute_all(behavior="wait")
        context.close()
    scoped = check_scoped_user(browser)
    browser.close()
(out / "results.json").write_text(
    json.dumps({"browser": results, "scoped": scoped}, indent=2)
)
print(
    f"scoped Activity API access and {len(results)} production-browser cases passed",
    flush=True,
)
