import argparse
import datetime
import json
import re
import sys
import urllib.parse
from pathlib import Path

from activity_fixture import prepare_activity, verify_activity_access
from harness import evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

sys.dont_write_bytecode = True
parser = argparse.ArgumentParser(
    description="Exercise scoped audit Activity with a throwaway API and database"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/activity-ui"))
args = parser.parse_args()
state = load_state(args, "activity_ui", "tests/ui/fixtures/activity/scenario.json")
activity = prepare_activity(evidence, state)
state["activity"] = activity
evidence.write_json(Path(args.state), state)
access = verify_activity_access(evidence, state, activity)
origin = state["ui_url"]
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
(out / "full").mkdir(exist_ok=True)
base = f"/org/{activity['orgId']}/wks/{activity['workspaceId']}"
results = []
VIEWPORT = {"width": 1440, "height": 1000}


def check_scoped_user(browser):
    """A member whose only grant is read on one resource sees it on every page without a refused request."""
    rid = activity["resourceId"]
    owner = state["users"]["owner"]["token"]
    environments = evidence.rpc(
        state["api_url"],
        owner,
        "loco.environment.v1.EnvironmentService/ListEnvironments",
        {"workspaceId": activity["workspaceId"]},
    )["environments"]
    production = next(
        e["id"] for e in environments if e["type"] == "ENVIRONMENT_TYPE_PRODUCTION"
    )
    minute_ago = datetime.datetime.now(datetime.UTC) - datetime.timedelta(minutes=1)
    stamp = minute_ago.strftime("%Y-%m-%dT%H:%M:%S.000Z")
    telemetry = {
        "QueryLogBuckets": {
            "buckets": [{"startTime": stamp, "severity": "INFO", "count": "1"}]
        },
        "QueryLogs": {
            "entries": [
                {
                    "timestamp": stamp,
                    "severity": "INFO",
                    "body": "checkout order placed",
                    "resourceId": rid,
                    "resourceAttributes": {
                        "loco.region": "us-east-1",
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
        "data": {
            "reason": "crash_loop",
            "message": "Back-off restarting failed container",
            "placementId": "checkout-us-east-1",
            "region": "us-east-1",
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
        "data": {"resourceId": rid, "environmentId": production},
    }
    other_rollout = dict(
        rollout,
        id="01a1c8b3-0000-7000-8000-00000000d002",
        type="resource.scaled",
        data={"environmentId": "01a1c8b3-0000-7000-8000-0000000000ff", "replicas": 3},
    )
    down = {
        "environmentId": production,
        "environmentName": "production",
        "status": "RESOURCE_STATUS_UNAVAILABLE",
        "regions": [
            {
                "region": "us-east-1",
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
                page.get_by_label("Last log lines in us-east-1")
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
            browser, "scoped", state=state, viewport=VIEWPORT
        )
        recorded = []

        def route_request(route, *, recorded=recorded, scene=scene):
            url = urllib.parse.urlparse(route.request.url)
            if url.path == "/runtime-config.js":
                route.fulfill(
                    content_type="application/javascript",
                    body="window.locoConfig=" + json.dumps({"apiURL": origin}) + ";",
                )
                return
            if url.path == "/dex/.well-known/openid-configuration":
                route.fulfill(json=evidence.discovery_document(origin + "/dex"))
                return
            if not url.path.startswith("/loco."):
                route.continue_()
                return
            method = url.path.rsplit("/", 1)[-1]
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
            response = route.fetch(url=state["api_url"] + url.path)
            recorded.append({"method": method, "body": body, "status": response.status})
            if method == "GetConfig":
                config = response.json()
                config["auth"]["issuer"] = origin + "/dex"
                route.fulfill(response=response, json=config)
            elif (
                method == "ListEvents"
                and scene in ("resource", "events")
                and body.get("environmentId")
                and response.status == 200
            ):
                # The cluster reports crash loops as runtime events; this resource never ran.
                listed = response.json()
                listed["events"] = [back_off, *listed.get("events", [])]
                route.fulfill(response=response, json=listed)
            elif (
                method == "ListEvents"
                and scene == "events"
                and not body.get("environmentId")
                and response.status == 200
            ):
                # Deploys and scaling carry their environment only in their data.
                listed = response.json()
                listed["events"] = [rollout, other_rollout, *listed.get("events", [])]
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
            with page.expect_response(
                lambda response: response.url.endswith("/QueryMetrics")
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
                and request["body"].get("resourceId") == rid
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
    for user, scene, path, title in [
        (
            "owner",
            "resource-owner",
            base + "/resource/" + activity["resourceId"],
            "Activity",
        ),
        ("member", "org-member", base + "/settings?scope=org", ""),
        (
            "workspace_admin",
            "workspace",
            base + "/settings?scope=workspace",
            "Activity",
        ),
        (
            "resource_reader",
            "resource",
            base + "/resource/" + activity["resourceId"],
            "Activity",
        ),
    ]:
        context, page, errors = open_page(browser, user, state=state, viewport=VIEWPORT)
        recorded = []
        mode = {
            "fail_older": False,
            "hold_initial": scene == "resource",
            "fail_initial": False,
        }
        held = []

        def route_request(route, *, mode=mode, held=held, recorded=recorded):
            url = urllib.parse.urlparse(route.request.url)
            if url.path == "/runtime-config.js":
                route.fulfill(
                    content_type="application/javascript",
                    body="window.locoConfig=" + json.dumps({"apiURL": origin}) + ";",
                )
                return
            if url.path == "/dex/.well-known/openid-configuration":
                route.fulfill(json=evidence.discovery_document(origin + "/dex"))
                return
            if url.path.startswith("/loco."):
                method = url.path.rsplit("/", 1)[-1]
                assert method.startswith(("Get", "List", "WhoAmI", "Query")), method
                body = route.request.post_data_json
                is_activity = method == "ListEvents" and not body.get("query")
                if is_activity and mode["hold_initial"]:
                    held.append(route)
                    return
                if is_activity and (
                    (mode["fail_older"] and body.get("beforeSeq"))
                    or (mode["fail_initial"] and not body.get("beforeSeq"))
                ):
                    recorded.append(
                        {
                            "method": method,
                            "body": body,
                            "status": 503,
                            "controlledFailure": True,
                        }
                    )
                    route.fulfill(
                        status=503,
                        json={
                            "code": "unavailable",
                            "message": "Activity is temporarily unavailable",
                        },
                    )
                    return
                response = route.fetch(url=state["api_url"] + url.path)
                recorded.append(
                    {"method": method, "body": body, "status": response.status}
                )
                if method == "GetConfig":
                    config = response.json()
                    config["auth"]["issuer"] = origin + "/dex"
                    route.fulfill(response=response, json=config)
                else:
                    route.fulfill(response=response)
                return
            route.continue_()

        context.route("**/*", route_request)
        if user == "member":
            with (
                page.expect_response(lambda response: response.url.endswith("/GetOrg")),
                page.expect_response(
                    lambda response: response.url.endswith("/ListOrgMembers")
                ),
            ):
                page.goto(origin + path)
        else:
            page.goto(origin + path)
        marker = "checkout" if scene.startswith("resource") else "Settings"
        page.get_by_text(marker, exact=True).first.wait_for()
        page.locator("[role=status][aria-busy]").wait_for(state="detached")
        if mode["hold_initial"]:
            panel = page.get_by_role("region", name=title, exact=True)
            expect(panel.locator("[data-slot=skeleton]")).to_have_count(3)
            expect(panel.get_by_role("listitem")).to_have_count(0)
            assert len(held) == 1
            mode["hold_initial"] = False
            mode["fail_initial"] = True
            held[0].fulfill(
                status=503,
                json={
                    "code": "unavailable",
                    "message": "Activity is temporarily unavailable",
                },
            )
            panel.get_by_role("alert").wait_for()
            expect(panel.get_by_role("listitem")).to_have_count(0)
            mode["fail_initial"] = False
            panel.get_by_role("button", name="Retry", exact=True).click()
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
        if user in ("member", "workspace_admin"):
            assert not errors, errors
            results.append(
                {"user": user, "scene": scene, "requests": recorded, "errors": errors}
            )
            context.unroute_all(behavior="wait")
            context.close()
            continue
        panel = page.get_by_role("region", name=title, exact=True)
        expect(panel.get_by_role("listitem")).to_have_count(10)
        expect(panel).to_contain_text("Olivia Owner restarted a resource")
        expect(panel).not_to_contain_text("STAGING_ONLY")
        reads = [
            request["body"]
            for request in recorded
            if request["method"] == "ListEvents" and not request["body"].get("query")
        ]
        assert reads and all(request.get("environmentId") for request in reads), reads
        # Only someone who can open the full audit log for the workspace gets the link to it.
        expect(
            panel.get_by_role("link", name="View all in audit log", exact=True)
        ).to_have_count(1 if user == "owner" else 0)
        if user == "resource_reader":
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
            assert (
                "other-company"
                not in page.locator("nav[aria-label=breadcrumb]").inner_text()
            )
            assert any(
                request["method"] == "ListEnvironments" and request["status"] == 200
                for request in recorded
            )
            assert not [
                request
                for request in recorded
                if request["status"] >= 400 and not request.get("controlledFailure")
            ], recorded
        panel.evaluate(
            '(element) => element.scrollIntoView({block: "start", behavior: "instant"})'
        )
        if scene == "resource":
            loaded_rows = panel.get_by_role("listitem").all_text_contents()
            mode["fail_older"] = True
            panel.get_by_role("button", name="Show older", exact=True).click()
            panel.get_by_role("alert").wait_for()
            expect(panel.get_by_role("listitem")).to_have_text(loaded_rows)
            cursor = [
                request["body"]["beforeSeq"]
                for request in recorded
                if request.get("controlledFailure")
            ][-1]
            mode["fail_older"] = False
            panel.get_by_role("button", name="Retry", exact=True).click()
            expect(panel.get_by_role("listitem")).to_have_count(20)
            assert panel.get_by_role("listitem").all_text_contents()[:10] == loaded_rows
            retries = [
                request
                for request in recorded
                if request["method"] == "ListEvents"
                and request["status"] == 200
                and request["body"].get("beforeSeq")
            ]
            assert retries[-1]["body"]["beforeSeq"] == cursor, retries[-1]
        if user == "owner":
            panel.get_by_role("link", name="View all in audit log", exact=True).click()
            page.wait_for_url(
                "**/org/"
                + activity["orgId"]
                + "/audit?range=90d&ws="
                + activity["workspaceId"]
                + "&res="
                + activity["resourceId"]
                + "&env=*"
            )
            page.get_by_role("list", name="Audit events").wait_for()
            expect(
                page.get_by_role("button", name="checkout", exact=True)
            ).to_be_visible()
            reads = [
                request["body"]
                for request in recorded
                if request["method"] == "ListEvents"
                and request["body"].get("startTime")
            ]
            assert (
                reads
                and reads[0]["workspaceId"] == activity["workspaceId"]
                and reads[0]["resourceId"] == activity["resourceId"]
            ), reads
            page.goto(origin + base + "/settings?scope=workspace")
            page.get_by_role("link", name="Activity in audit log", exact=True).click()
            page.wait_for_url(
                "**/org/"
                + activity["orgId"]
                + "/audit?range=90d&ws="
                + activity["workspaceId"]
            )
            page.get_by_role("list", name="Audit events").wait_for()
            expect(
                page.get_by_role("button", name="shared", exact=True)
            ).to_be_visible()
            page.goto(origin + base + "/resource/" + activity["resourceId"])
            page.get_by_role("button", name="Events", exact=True).wait_for()
            environment_switcher = page.get_by_title("Switch environment", exact=True)
            environment_switcher.click()
            page.get_by_role("menuitem", name="staging", exact=False).click()
            page.keyboard.press("Escape")
            expect(environment_switcher).to_contain_text("staging")
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
    json.dumps({"access": access, "browser": results, "scoped": scoped}, indent=2)
)
print(
    f"scoped Activity API access and {len(results)} production-browser cases passed",
    flush=True,
)
