import argparse
import contextlib
import json
import re
import sys
import time
import uuid
from functools import partial
from pathlib import Path

from harness import evidence, load_state, open_page, repo, rpc as api_rpc, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Exercise deployment history against an isolated production API and database"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/deployment-history"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)


SEPARATED_ROWS = """section => [...section.querySelectorAll("table")].filter((table) => table.offsetParent !== null).every((table) => {
    const rows = [...table.querySelectorAll("tbody tr")].slice(0, -1).filter((row) => !row.classList.contains("border-b-0"));
    return rows.length > 0 && rows.every((row) => getComputedStyle(row).borderBottomWidth === "1px");
})"""


def failure_hook(kind, error, traceback):
    (out / "exception.json").write_text(
        json.dumps({"type": kind.__name__, "error": str(error)}, indent=2)
    )
    sys.__excepthook__(kind, error, traceback)


sys.excepthook = failure_hook
started = time.monotonic()
state = load_state(
    args, "deployment-history", "tests/ui/fixtures/deployment-history/scenario.json"
)
boot_seconds = time.monotonic() - started
owner = state["users"]["owner"]["token"]
workspace_id = state["workspaces"]["acme/shared"]


rpc = partial(api_rpc, state)


BUILD_DIGEST = "sha256:" + "7c41e9b2" * 8
PLATFORM_DOMAIN = "apps.e2e.test"
BUILD_LOG = [
    ("INFO", "#1 [internal] load build definition from Dockerfile"),
    ("ERROR", "Pilot build step failed"),
]


def fixture():
    existing = state.get("deployment_history")
    if existing is not None:
        return existing
    environments = rpc(
        "environment", "ListEnvironments", {"workspaceId": workspace_id}
    )["environments"]
    production = next(env["id"] for env in environments if env["name"] == "production")
    staging = rpc(
        "environment",
        "CreateEnvironment",
        {
            "workspaceId": workspace_id,
            "name": "staging",
            "type": "ENVIRONMENT_TYPE_STAGING",
        },
    )["environmentId"]
    resource = rpc(
        "resource",
        "CreateResource",
        {
            "workspaceId": workspace_id,
            "name": "history-pilot",
            "type": "RESOURCE_TYPE_SERVICE",
            "spec": {
                "service": {
                    "routing": {"port": 8080},
                    "regions": {
                        region: {
                            "enabled": True,
                            "cpu": "100m",
                            "memory": "128Mi",
                            "minReplicas": 1,
                            "maxReplicas": 1,
                        }
                        for region in ("us-east-1", "eu-west-1")
                    },
                }
            },
        },
    )["resourceId"]
    edge = rpc(
        "resource",
        "CreateResource",
        {
            "workspaceId": workspace_id,
            "name": "edge-pilot",
            "type": "RESOURCE_TYPE_SERVICE",
            "spec": {
                "service": {
                    "routing": {"port": 8080},
                    "regions": {
                        region: {
                            "enabled": True,
                            "cpu": "100m",
                            "memory": "128Mi",
                            "minReplicas": 1,
                            "maxReplicas": 1,
                        }
                        for region in ("us-east-1", "eu-west-1")
                    },
                }
            },
        },
    )["resourceId"]
    clusters = [str(uuid.uuid4()) for _ in range(3)]
    release_ids = [str(uuid.uuid4()) for _ in range(221)]
    staging_release = str(uuid.uuid4())
    builds = {
        name: str(uuid.uuid4())
        for name in ("awaiting", "running", "deployed", "failed", "canceled", "older")
    }
    sql = []
    for cluster, region, tier in zip(
        clusters,
        ("us-east-1", "eu-west-1", "us-east-1"),
        ("production", "production", "staging"),
        strict=True,
    ):
        domains = "{" + PLATFORM_DOMAIN + "}" if cluster == clusters[0] else "{}"
        sql.append(
            f"INSERT INTO clusters (id,name,region,provider,is_active,is_default,health_status,last_heartbeat,tier,domains) VALUES ('{cluster}','browser-pilot','{region}','fixture',true,false,'healthy',NOW(),'{tier}','{domains}');"
        )
    for index, deployment in enumerate(release_ids, 1):
        region = "eu-west-1" if index == len(release_ids) else "us-east-1"
        cluster = clusters[1] if region == "eu-west-1" else clusters[0]
        active = index in (1, len(release_ids))
        status = "running" if active else "superseded"
        source = {
            "type": "image",
            "image": f"example.invalid/browser-pilot:release-{index:03}",
        }
        if index == 2:
            source = dict(source, type="dockerfile", buildId=builds["deployed"])
        runtime = json.dumps(
            {
                "observability": {"injectOtel": True},
                "build": source,
                "cpu": "100m",
                "memory": "128Mi",
                "minReplicas": 1,
                "maxReplicas": 1,
                "port": 8080,
            }
        )
        sql.append(
            f"INSERT INTO deployments (actor_type, actor_id, method, id,resource_id,cluster_id,region,replicas,status,is_active,environment_id,spec,spec_version,created_at,started_at) VALUES ('user', '{state['users']['owner']['id']}', 'cli', '{deployment}','{resource}','{cluster}','{region}',1,'{status}',{str(active).lower()},'{production}',{evidence.sql_literal(runtime)},1,NOW()-INTERVAL '{index} minutes',NOW()-INTERVAL '{index} minutes');"
        )
    runtime = json.dumps(
        {
            "observability": {"injectOtel": True},
            "build": {
                "type": "image",
                "image": "example.invalid/browser-pilot:staging-current",
            },
            "cpu": "100m",
            "memory": "128Mi",
            "minReplicas": 2,
            "maxReplicas": 2,
            "port": 8080,
        }
    )
    sql.append(
        f"INSERT INTO deployments (actor_type, actor_id, method, id,resource_id,cluster_id,region,replicas,status,is_active,environment_id,spec,spec_version,started_at) VALUES ('user', '{state['users']['owner']['id']}', 'cli', '{staging_release}','{resource}','{clusters[2]}','us-east-1',2,'running',true,'{staging}',{evidence.sql_literal(runtime)},1,NOW());"
    )
    for deployment, cluster, environment, region, replicas in (
        (release_ids[0], clusters[0], production, "us-east-1", 1),
        (release_ids[-1], clusters[1], production, "eu-west-1", 1),
        (staging_release, clusters[2], staging, "us-east-1", 2),
    ):
        sql.append(
            f"INSERT INTO placements (resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,ready_replicas,status_updated_at) VALUES ('{resource}','{cluster}','{region}','{deployment}','{environment}','{{\"app_spec\":{{}}}}',{replicas},NOW());"
        )
    for cluster, region in ((clusters[0], "us-east-1"), (clusters[1], "eu-west-1")):
        deployment = str(uuid.uuid4())
        runtime = json.dumps(
            {
                "observability": {"injectOtel": True},
                "build": {"type": "image", "image": "example.invalid/edge-pilot:1.0.0"},
                "cpu": "100m",
                "memory": "128Mi",
                "minReplicas": 1,
                "maxReplicas": 1,
                "port": 8080,
            }
        )
        sql.append(
            f"INSERT INTO deployments (actor_type, actor_id, method, id,resource_id,cluster_id,region,replicas,status,is_active,environment_id,spec,spec_version,started_at) VALUES ('user', '{state['users']['owner']['id']}', 'cli', '{deployment}','{edge}','{cluster}','{region}',1,'running',true,'{production}',{evidence.sql_literal(runtime)},1,NOW());"
        )
        sql.append(
            f"INSERT INTO placements (resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,ready_replicas,status_updated_at) VALUES ('{edge}','{cluster}','{region}','{deployment}','{production}','{{\"app_spec\":{{}}}}',1,NOW());"
        )
    sql.append(
        f"INSERT INTO resource_domains (resource_id,environment_id,domain,domain_source,subdomain_label,platform_domain,is_primary) VALUES ('{edge}','{production}','edge.{PLATFORM_DOMAIN}','platform_provided','edge','{PLATFORM_DOMAIN}',true);"
    )
    sql.append(
        f"INSERT INTO user_scopes (user_id,scope,entity_type,entity_id) VALUES ('{state['users']['resource_reader']['id']}','read','resource','{resource}');"
    )
    owner_id = state["users"]["owner"]["id"]
    build_rows = (
        ("awaiting", "awaiting_upload", ""),
        ("running", "running", ""),
        ("deployed", "succeeded", ""),
        ("failed", "failed", "RUN go build ./cmd/api exited with code 1"),
        ("canceled", "canceled", "canceled"),
        ("older", "succeeded", ""),
    )
    for minutes, (name, status, message) in enumerate(build_rows, 1):
        digest = f"'{BUILD_DIGEST}'" if status == "succeeded" else "NULL"
        started = (
            "NULL"
            if status == "awaiting_upload"
            else f"NOW()-INTERVAL '{minutes} minutes'"
        )
        finished = (
            f"NOW()-INTERVAL '{minutes} minutes'+INTERVAL '108 seconds'"
            if status in ("succeeded", "failed", "canceled")
            else "NULL"
        )
        sql.append(
            f"INSERT INTO builds (id,resource_id,status,source_type,source_key,source_size,dockerfile_path,context,image_repository,image_digest,message,created_by,created_at,started_at,finished_at) VALUES ('{builds[name]}','{resource}','{status}','upload','pilot/{builds[name]}',1024,'Dockerfile','.','example.invalid/browser-pilot',{digest},'{message}','{owner_id}',NOW()-INTERVAL '{minutes} minutes',{started},{finished});"
        )
    sql.append(
        f"UPDATE deployments SET commit_sha='{'7c41e9a'.ljust(40, '0')}', commit_dirty=true WHERE id='{release_ids[0]}';"
    )
    evidence.psql(state["container"], "BEGIN;" + "\n".join(sql) + "COMMIT;")
    data = {
        "resource": resource,
        "edge": edge,
        "production": production,
        "staging": staging,
        "releases": release_ids,
        "staging_release": staging_release,
        "builds": builds,
    }
    state["deployment_history"] = data
    evidence.write_json(Path(args.state), state)
    return data


data = fixture()
for resource, environment in (
    (data["resource"], data["production"]),
    (data["resource"], data["staging"]),
    (data["edge"], data["production"]),
):
    assert (
        rpc(
            "resource",
            "GetResourceEnv",
            {"resourceId": resource, "environmentId": environment},
        )
        == {}
    )
first_page = rpc(
    "deployment",
    "ListDeployments",
    {
        "resourceId": data["resource"],
        "environmentId": data["production"],
        "pageSize": 200,
    },
)
assert len(first_page["deployments"]) == 200 and first_page["nextPageToken"]
assert data["releases"][-1] not in [row["id"] for row in first_page["deployments"]]
older = rpc(
    "deployment",
    "ListDeployments",
    {
        "resourceId": data["resource"],
        "environmentId": data["production"],
        "pageSize": 200,
        "pageToken": first_page["nextPageToken"],
    },
)
assert len(older["deployments"]) == 21 and not older.get("nextPageToken")
assert rpc("deployment", "GetDeployment", {"deploymentId": data["releases"][-1]})[
    "deployment"
]["spec"]["service"]["build"]["image"].endswith("release-221")
org_id = next(
    org["id"]
    for org in evidence.rpc(
        state["api_url"],
        owner,
        "loco.org.v1.OrgService/ListUserOrgs",
        {"userId": state["users"]["owner"]["id"]},
    )["orgs"]
    if org["name"] == "acme"
)
base = f"/org/{org_id}/wks/{workspace_id}"
results = []
with session(out) as browser:
    try:
        evidence.psql(
            state["container"],
            f"UPDATE clusters SET last_heartbeat=NOW() WHERE id IN (SELECT cluster_id FROM deployments WHERE resource_id='{data['resource']}');",
        )
        evidence.psql(
            state["container"],
            f"UPDATE builds SET status='awaiting_upload', message='', finished_at=NULL WHERE id='{data['builds']['awaiting']}';",
        )
        context, page, errors = open_page(browser, state=state)
        context.set_default_timeout(evidence.STEP_TIMEOUT_MS)
        requests = []

        def record_deployment_request(request):
            method = request.url.rsplit("/", 1)[-1]
            if ".DeploymentService/" in request.url and method in (
                "GetDeployment",
                "ListDeployments",
            ):
                requests.append({"method": method, "body": request.post_data_json})

        page.on("request", record_deployment_request)
        log_queries = []

        def build_logs(route):
            log_queries.append(route.request.post_data_json)
            entries = [
                {
                    "timestamp": f"2026-01-01T00:00:0{index}Z",
                    "severity": severity,
                    "body": text,
                }
                for index, (severity, text) in enumerate(BUILD_LOG)
            ]
            route.fulfill(json={"entries": entries})

        context.route(
            "**/loco.observability.v1.ObservabilityService/QueryLogs", build_logs
        )
        failed = []

        def history(route):
            body = route.request.post_data_json
            if body.get("pageToken") and not failed:
                failed.append(body)
                route.fulfill(
                    status=503,
                    json={"code": "unavailable", "message": "Pilot page failure"},
                )
            else:
                route.continue_()

        context.route(
            "**/loco.deployment.v1.DeploymentService/ListDeployments", history
        )
        active_page, active_context = page, context
        stage, user = "history", "owner"
        try:
            page.goto(
                state["ui_url"]
                + base
                + "/resource/"
                + data["resource"]
                + "?env=production"
            )
            section = page.get_by_role("tab", name=re.compile(r"^Deployments")).locator(
                "xpath=ancestor::section[1]"
            )
            expect(section.get_by_text("1–10 of 200+", exact=False)).to_be_visible()
            expect(
                section.get_by_role(
                    "button",
                    name=f"Show details for {data['releases'][0][:8]}",
                    exact=True,
                )
            ).to_be_visible()
            expect(
                section.get_by_role("columnheader", name="Deployed by", exact=True)
            ).to_be_visible()
            expect(
                section.get_by_role(
                    "tabpanel", name=re.compile(r"^Deployments")
                ).get_by_role("columnheader", name="Time", exact=True)
            ).to_be_visible()
            expect(
                section.get_by_text("loco deploy · CLI", exact=True).first
            ).to_be_visible()
            section.get_by_role(
                "button", name=f"Show details for {data['releases'][0][:8]}", exact=True
            ).click()
            expect(
                section.get_by_role("button", name="Copy SHA", exact=True)
            ).to_have_attribute("title", f"Copy {'7c41e9a'.ljust(40, '0')}")
            section.get_by_role(
                "button", name=f"Hide details for {data['releases'][0][:8]}", exact=True
            ).click()
            expect(
                section.get_by_role("columnheader", name="Region", exact=True)
            ).to_be_visible()
            page.get_by_role("button", name="Redeploy", exact=True).click()
            redeploy_dialog = page.get_by_role("dialog", name="Redeploy history-pilot?")
            expect(
                redeploy_dialog.get_by_text("Now running", exact=True)
            ).to_be_visible()
            redeploy_dialog.get_by_role("button", name="Cancel", exact=True).click()
            page.get_by_role("button", name="More actions", exact=True).click()
            expect(
                page.get_by_role("menuitem", name="Delete resource", exact=True)
            ).to_be_enabled()
            page.keyboard.press("Escape")
            assert any(
                request["method"] == "GetDeployment"
                and request["body"]["deploymentId"] == data["releases"][-1]
                for request in requests
            )
            stage = "older-page-retry"
            section.get_by_role("button", name="50", exact=True).click()
            next_button = section.get_by_role("button", name="Next page", exact=True)
            for _ in range(3):
                next_button.click()
            expect(
                section.get_by_role(
                    "button",
                    name=f"Show details for {data['releases'][150][:8]}",
                    exact=True,
                )
            ).to_be_visible()
            next_button.click()
            section.get_by_text("Pilot page failure", exact=False).wait_for()
            expect(
                section.get_by_role(
                    "button",
                    name=f"Show details for {data['releases'][150][:8]}",
                    exact=True,
                )
            ).to_be_visible()
            assert len(failed) == 1
            expect(section.get_by_text("151–200 of 200+", exact=False)).to_be_visible()
            next_button.focus()
            page.keyboard.press("Enter")
            expect(section.get_by_text("201–221 of 221", exact=False)).to_be_visible()
            expect(
                section.get_by_role(
                    "button",
                    name=f"Show details for {data['releases'][220][:8]}",
                    exact=True,
                )
            ).to_be_visible()
            older_requests = [
                request["body"]
                for request in requests
                if request["method"] == "ListDeployments"
                and request["body"].get("pageToken")
            ]
            assert (
                len(older_requests) == 2
                and older_requests[0] == older_requests[1] == failed[0]
            )
            stage = "environment-switch"
            environment_switcher = page.get_by_title("Switch environment", exact=True)
            environment_switcher.click()
            page.get_by_role("menuitem", name="staging staging", exact=True).click()
            expect(page).to_have_url(re.compile(r"[?&]env=staging"))
            expect(
                section.get_by_role(
                    "button",
                    name=f"Show details for {data['staging_release'][:8]}",
                    exact=True,
                )
            ).to_be_visible()
            # One deployment fits the smallest page size, so the pager is gone.
            expect(section.get_by_text("1–1 of 1", exact=False)).to_have_count(0)
            assert not section.get_by_role(
                "button",
                name=f"Show details for {data['releases'][220][:8]}",
                exact=True,
            ).count()
            assert any(
                request["method"] == "ListDeployments"
                and request["body"].get("environmentId") == data["staging"]
                and not request["body"].get("pageToken")
                for request in requests
            )
            expect(next_button).to_have_count(0)
            assert any(
                request["method"] == "GetDeployment"
                and request["body"]["deploymentId"] == data["staging_release"]
                for request in requests
            )
            environment_switcher.click()
            page.get_by_role(
                "menuitem", name="production production", exact=True
            ).click()
            expect(
                section.get_by_role(
                    "button",
                    name=f"Show details for {data['releases'][0][:8]}",
                    exact=True,
                )
            ).to_be_visible()
            expect(
                section.get_by_role(
                    "button",
                    name=f"Show details for {data['releases'][150][:8]}",
                    exact=True,
                )
            ).to_have_count(0)
            assert section.evaluate(SEPARATED_ROWS), (
                "deployment rows lost their separators"
            )
            stage = "builds"
            builds = data["builds"]
            section.get_by_role(
                "button", name=f"Show details for {data['releases'][1][:8]}", exact=True
            ).click()
            section.get_by_role(
                "button", name=f"Build {builds['deployed'][:8]}", exact=True
            ).click()
            expect(page).to_have_url(re.compile(r"[?&]hist=builds"))
            expect(
                section.get_by_role("tab", name=re.compile(r"^Builds\s*6$"))
            ).to_have_attribute("aria-selected", "true")
            expect(section.get_by_text("1–5 of 6", exact=False)).to_be_visible()
            assert section.evaluate(SEPARATED_ROWS), "build rows lost their separators"
            expect(
                section.get_by_role(
                    "button", name=f"Hide build {builds['deployed'][:8]}", exact=True
                )
            ).to_be_visible()
            expect(
                section.get_by_text("Olivia Owner", exact=True).first
            ).to_be_visible()
            expect(
                section.get_by_text(BUILD_DIGEST[:19], exact=True).first
            ).to_be_visible()
            section.get_by_role(
                "button", name="Deployment release-002", exact=True
            ).click()
            expect(page).not_to_have_url(re.compile(r"[?&]hist=builds"))
            expect(
                section.get_by_role(
                    "button",
                    name=f"Hide details for {data['releases'][1][:8]}",
                    exact=True,
                )
            ).to_be_visible()
            section.get_by_role("tab", name=re.compile(r"^Builds")).click()
            expect(
                section.get_by_role(
                    "tabpanel", name=re.compile(r"^Builds")
                ).get_by_role("columnheader", name="Time", exact=True)
            ).to_be_visible()
            section.get_by_role(
                "button", name=f"Hide build {builds['deployed'][:8]}", exact=True
            ).click()
            section.get_by_role(
                "button", name=f"Show build {builds['failed'][:8]}", exact=True
            ).click()
            expect(
                section.get_by_text(
                    "RUN go build ./cmd/api exited with code 1", exact=True
                )
            ).to_be_visible()
            expect(section.get_by_text(BUILD_LOG[1][1], exact=True)).to_be_visible()
            expect(
                section.get_by_role("link", name="Open in Logs", exact=True)
            ).to_have_attribute("href", re.compile(f"q=@build:{builds['failed']}"))
            log_requests = [
                body
                for body in log_queries
                if body.get("query") == f"@build:{builds['failed']}"
            ]
            assert (
                log_requests
                and not log_requests[0].get("environmentId")
                and log_requests[0].get("order") == "LOG_ORDER_OLDEST_FIRST"
            ), log_queries
            expect(
                section.get_by_role("button", name="Cancel build", exact=True)
            ).to_have_count(0)
            section.get_by_role(
                "button", name=f"Show build {builds['awaiting'][:8]}", exact=True
            ).click()
            expect(
                section.get_by_text("Waiting for the CLI to upload source.", exact=True)
            ).to_be_visible()
            section.get_by_role("button", name="Cancel build", exact=True).click()
            page.get_by_role("alertdialog").get_by_role(
                "button", name="Cancel build", exact=True
            ).click()
            expect(
                page.get_by_text(f"Canceled build {builds['awaiting'][:8]}", exact=True)
            ).to_be_visible(timeout=evidence.STEP_TIMEOUT_MS)
            expect(
                section.get_by_role("button", name="Cancel build", exact=True)
            ).to_have_count(0)
            expect(section.get_by_text("canceled", exact=True).first).to_be_visible()
            stage = "resources"
            page.goto(
                state["ui_url"]
                + base
                + "/resources?env=staging&cols=env-production,env-staging,size,replicas"
            )
            pilot_row = page.get_by_role("row").filter(
                has=page.get_by_role("link", name="history-pilot", exact=True)
            )
            expect(pilot_row).to_be_visible()
            headers = page.get_by_role("columnheader").all_inner_texts()
            expect(
                pilot_row.get_by_role("cell").nth(headers.index("Replicas"))
            ).to_have_text("2")
            page.goto(state["ui_url"] + base + "/resources?env=production&view=map")
            page.get_by_role("button", name=re.compile(r"^history-pilot")).click()
            map_panel = page.get_by_role("dialog", name="history-pilot")
            expect(
                map_panel.get_by_role("link", name="Open history-pilot")
            ).to_be_visible()
            page.keyboard.press("Escape")
            page.get_by_role("button", name="Table", exact=True).click()
            expect(page).not_to_have_url(re.compile(r"view=map"))
            page.get_by_role("button", name="Region: All regions", exact=True).click()
            region = page.get_by_role("menuitemradio", name=re.compile(r"^eu-west-1"))
            region.click()
            expect(region).to_have_attribute("aria-checked", "true")
            check_box = region.locator("svg").bounding_box()
            count_box = region.get_by_text(re.compile(r"^\d+$")).bounding_box()
            assert check_box["x"] >= count_box["x"] + count_box["width"], (
                check_box,
                count_box,
            )
            page.keyboard.press("Escape")
            page.get_by_role(
                "checkbox", name="Select history-pilot", exact=True
            ).click()
            bulk_delete = page.get_by_role("button", name="Delete", exact=True)
            bulk_delete.click()
            page.get_by_role("alertdialog").get_by_role(
                "button", name="Cancel", exact=True
            ).click()
            expect(bulk_delete).to_be_focused()
            page.keyboard.press("Enter")
            page.get_by_role("alertdialog").wait_for()
            page.keyboard.press("Escape")
            expect(bulk_delete).to_be_focused()
            page.get_by_role(
                "button", name="Actions for history-pilot", exact=True
            ).click()
            page.get_by_role("menuitem", name="Delete", exact=True).click()
            row_confirm = page.get_by_role("alertdialog", name="Delete history-pilot?")
            expect(
                row_confirm.get_by_role("button", name="Delete", exact=True)
            ).to_be_disabled()
            row_confirm.get_by_role("textbox").fill("history-pilot")
            expect(
                row_confirm.get_by_role("button", name="Delete", exact=True)
            ).to_be_enabled()
            row_confirm.get_by_role("button", name="Cancel", exact=True).click()
            page.goto(state["ui_url"] + base + "?env=production")
            expect(page.get_by_label("Spend: not available yet")).to_be_visible()
            expect(
                page.get_by_role("group", name=re.compile(r"^Allocated: "))
            ).to_be_visible()
            page.get_by_role(
                "button", name="Actions for history-pilot", exact=True
            ).click()
            expect(
                page.get_by_role("menuitem", name=re.compile(r"^Roll back\s*to "))
            ).to_be_enabled()
            expect(
                page.get_by_role("menuitem", name="Restart", exact=True)
            ).to_be_enabled()
            page.get_by_role("menuitem", name=re.compile(r"^Roll back")).click()
            expect(
                page.get_by_role(
                    "dialog", name=re.compile(r"^Roll back history-pilot in ")
                )
            ).to_be_visible()
            page.keyboard.press("Escape")
            stage = "domains"
            page.goto(
                state["ui_url"]
                + base
                + "/resource/"
                + data["edge"]
                + "?env=production&tab=settings"
            )
            networking = page.get_by_role("region", name="Networking")
            networking.get_by_role("textbox", name="Subdomain", exact=True).fill(
                "edge-v2"
            )
            expect(
                networking.get_by_text(
                    "No platform domain is served in every region of edge-pilot",
                    exact=True,
                )
            ).to_be_visible()
            expect(
                networking.get_by_role("button", name="Add", exact=True)
            ).to_be_disabled()
            stage = "owner-settings"
            page.goto(state["ui_url"] + base + "/settings?scope=workspace")
            owner_name = page.get_by_placeholder("storefront", exact=True)
            expect(owner_name).to_be_enabled()
            owner_name.fill("shared-edited")
            expect(page.get_by_role("button", name="Save", exact=True)).to_be_visible()
            page.get_by_role("button", name="Discard", exact=True).click()
            member_context, member_page, member_errors = open_page(
                browser, "member", state=state
            )
            active_page, active_context = member_page, member_context
            stage, user = "reader-settings", "member"
            member_page.goto(state["ui_url"] + base + "/settings?scope=workspace")
            expect(
                member_page.get_by_placeholder("storefront", exact=True)
            ).to_be_disabled()
            expect(
                member_page.get_by_role("button", name="Save", exact=True)
            ).to_have_count(0)
            member_context.tracing.stop()
            member_context.close()
            errors.extend(member_errors)
            reader_context, reader_page, reader_errors = open_page(
                browser, "resource_reader", state=state
            )
            active_page, active_context = reader_page, reader_context
            stage, user = "resource-reader", "resource_reader"
            denied = []
            reader_page.on(
                "response",
                lambda response: (
                    denied.append(response.url) if response.status == 403 else None
                ),
            )
            reader_page.goto(
                state["ui_url"]
                + base
                + "/resource/"
                + data["resource"]
                + "?env=production"
            )
            reader_section = reader_page.get_by_role(
                "tab", name=re.compile(r"^Deployments")
            ).locator("xpath=ancestor::section[1]")
            expect(
                reader_section.get_by_role(
                    "button",
                    name=f"Show details for {data['releases'][0][:8]}",
                    exact=True,
                )
            ).to_be_visible()
            expect(
                reader_page.get_by_text("Not deployed yet", exact=True)
            ).to_have_count(0)
            redeploy = reader_page.get_by_role("button", name="Redeploy", exact=True)
            expect(redeploy).to_be_disabled()
            expect(redeploy).to_have_attribute(
                "title", "Needs write access to this resource"
            )
            reader_page.get_by_role("button", name="More actions", exact=True).click()
            expect(
                reader_page.get_by_role("menuitem", name=re.compile(r"^Roll back"))
            ).to_be_disabled()
            reader_page.keyboard.press("Escape")
            reader_page.goto(
                state["ui_url"]
                + base
                + "/resource/"
                + data["resource"]
                + "?env=production&tab=settings"
            )
            expect(reader_page.get_by_role("region", name="Variables")).to_be_visible()
            assert not denied, denied
            reader_context.close()
            errors.extend(reader_errors)
            writer_context, writer_page, writer_errors = open_page(
                browser, "writer", state=state
            )
            active_page, active_context = writer_page, writer_context
            stage, user = "writer-delete", "writer"
            needs_admin = "Needs admin access to this resource"
            writer_page.goto(state["ui_url"] + base + "/resources?env=production")
            writer_page.get_by_role(
                "button", name="Actions for history-pilot", exact=True
            ).click()
            row_delete = writer_page.get_by_role("menuitem", name="Delete", exact=True)
            expect(row_delete).to_be_disabled()
            expect(row_delete).to_have_attribute("title", needs_admin)
            writer_page.keyboard.press("Escape")
            writer_page.goto(
                state["ui_url"]
                + base
                + "/resource/"
                + data["resource"]
                + "?env=production"
            )
            expect(
                writer_page.get_by_role("button", name="Redeploy", exact=True)
            ).to_be_enabled()
            writer_page.get_by_role("button", name="More actions", exact=True).click()
            expect(
                writer_page.get_by_role("menuitem", name="Delete resource", exact=True)
            ).to_be_disabled()
            writer_page.keyboard.press("Escape")
            writer_page.goto(
                state["ui_url"]
                + base
                + "/resource/"
                + data["resource"]
                + "?env=production&tab=settings"
            )
            settings_delete = writer_page.get_by_role(
                "button", name="Delete resource", exact=True
            )
            expect(settings_delete).to_be_disabled()
            writer_context.close()
            errors.extend(writer_errors)
            active_page, active_context = page, context
            assert not errors, errors
            results.append(
                {
                    "errors": errors,
                    "requests": requests,
                    "injectedFailures": len(failed),
                }
            )
        except BaseException as error:
            with contextlib.suppress(Exception):
                active_page.screenshot(path=out / "failure.png", animations="disabled")
            (out / "failure.json").write_text(
                json.dumps(
                    {
                        "stage": stage,
                        "user": user,
                        "error": repr(error),
                        "requests": requests,
                        "errors": errors,
                        "completed": results,
                    },
                    indent=2,
                )
            )
            with contextlib.suppress(Exception):
                active_context.tracing.stop(path=out / "failure.zip")
            raise
        context.tracing.stop()
        context.close()
    finally:
        browser.close()
(out / "results.json").write_text(
    json.dumps(
        {
            "bootSeconds": round(boot_seconds, 2),
            "totalSeconds": round(time.monotonic() - started, 2),
            "cases": results,
        },
        indent=2,
    )
)
print(f"PASS: {len(results)} deployment-history cases", flush=True)
