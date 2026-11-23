import argparse
import datetime
import json
import re
import time
import uuid
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from harness import evidence, load_state, open_page, repo, session, sql
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check the replicas of each region and a rollout across regions"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(
    args, "resource-replicas", "tests/ui/fixtures/deployment-history/scenario.json"
)
workspace = state["workspaces"]["acme/shared"]
EAST, WEST = "us-ca-1", "us-va-1"
SCALED_REPLICAS = 13
SCALED_PODS = [f"scaled-ui-7f9c-p{i:02d}" for i in range(SCALED_REPLICAS)]

if "replicas" not in state:
    spec = {
        "platform": "linux/amd64",
        "observability": {"injectOtel": True},
        "build": {"type": "image", "image": "registry.example.test/replicas:v2"},
        "port": 8080,
        "regions": {
            region: {
                "cpu": "500m",
                "memory": "256Mi",
                "minReplicas": 2,
                "maxReplicas": 2,
            }
            for region in (EAST, WEST)
        },
    }
    clusters = {
        region: evidence.seed_cluster(state, region, name=f"replicas-{region}")
        for region in (EAST, WEST)
    }
    deployment = str(uuid.uuid4())
    resource = evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": "replicas-ui",
                "deployments": [
                    {
                        "environment": "production",
                        "id": deployment,
                        "spec": spec,
                        "status": "deploying",
                        "age": "5 minutes",
                        "placements": {
                            region: {
                                "cluster": clusters[region],
                                "ready_replicas": 1 if region == EAST else 2,
                            }
                            for region in (EAST, WEST)
                        },
                    }
                ],
            }
        ],
    )["acme/shared/replicas-ui"]
    state["replicas"] = {
        "org": state["orgs"]["acme"]["id"],
        "resource": resource,
        "clusters": clusters,
        "deployment": deployment,
    }
    evidence.write_json(Path(args.state), state)

if "scaling" not in state:
    clusters = state["replicas"]["clusters"]

    def scaled_spec(sizes):
        return {
            "platform": "linux/amd64",
            "observability": {"injectOtel": True},
            "build": {"type": "image", "image": "registry.example.test/scaled:v1"},
            "port": 8080,
            "regions": {
                region: {"cpu": "1", "memory": "2Gi", **size}
                for region, size in sizes.items()
            },
        }

    def deployment(sizes, ready):
        return {
            "environment": "production",
            "id": str(uuid.uuid4()),
            "spec": scaled_spec(sizes),
            "status": "running",
            "age": "1 hour",
            "placements": {
                region: {"cluster": clusters[region], "ready_replicas": ready}
                for region in sizes
            },
        }

    autoscaled = {
        "minReplicas": 2,
        "maxReplicas": 20,
        "scalers": {"enabled": True, "cpuTarget": 70},
    }
    scaled = deployment({EAST: autoscaled}, SCALED_REPLICAS)
    seeded = evidence.seed_resources(
        state,
        [
            {"workspace": "acme/shared", "name": "scaled-ui", "deployments": [scaled]},
        ],
    )
    state["scaling"] = {
        "scaled": seeded["acme/shared/scaled-ui"],
        "scaled_deployment": scaled["id"],
    }
    evidence.write_json(Path(args.state), state)

r = state["replicas"]
scaling = state["scaling"]
path = f"/org/{r['org']}/wks/{workspace}/resource/{r['resource']}?env=production"


def report_replicas():
    """Writes each region's rollout report with times relative to now."""
    now = int(time.time())
    deployment = r["deployment"]

    def replica(name, deployment, *, ready=True, **fields):
        return {
            "uid": name,
            "name": name,
            "node": f"node-{name[-2:]}",
            "placement_revision": 1,
            "deployment_id": deployment,
            "ready": ready,
            "state": "Serving" if ready else "Failing",
            "started_at": {"seconds": now - 3 * 3600},
            **fields,
        }

    snapshots = {
        EAST: [
            replica("replicas-ui-east-a1", deployment),
            replica(
                "replicas-ui-east-b2",
                deployment,
                ready=False,
                restarts=4,
                started_at={"seconds": now - 20 * 60},
                last_exit={
                    "reason": "OOMKilled",
                    "exit_code": 137,
                    "finished_at": {"seconds": now - 5 * 60},
                },
                last_restart={
                    "at": {"seconds": now - 5 * 60},
                    "cause": "out_of_memory",
                    "exit_code": 137,
                },
            ),
        ],
        WEST: [
            replica(
                "replicas-ui-west-c3",
                deployment,
                last_exit={"reason": "Completed", "exit_code": 0},
            ),
            replica("replicas-ui-west-d4", deployment),
        ],
    }
    statements = []
    for region in (EAST, WEST):
        cluster = r["clusters"][region]
        placement = sql(
            state,
            f"SELECT id FROM placements WHERE deployment_id='{deployment}' AND cluster_id='{cluster}';",
        )
        snapshot = {"placementId": placement, "replicas": snapshots[region]}
        statements.append(
            "INSERT INTO deployment_rollouts(deployment_id,cluster_id,revision,snapshot) "
            f"VALUES('{deployment}','{cluster}',1,{evidence.sql_literal(json.dumps(snapshot))}) "
            "ON CONFLICT(deployment_id,cluster_id,revision) DO UPDATE SET snapshot=EXCLUDED.snapshot;"
        )
    sql(state, "BEGIN;\n" + "\n".join(statements) + "\nCOMMIT;")


def report_scaled():
    """Writes the autoscaled resource's report: more replicas than the charts draw lines for."""
    now = int(time.time())
    deployment = scaling["scaled_deployment"]
    cluster = r["clusters"][EAST]
    placement = sql(
        state,
        f"SELECT id FROM placements WHERE deployment_id='{deployment}' AND cluster_id='{cluster}';",
    )
    replicas = [
        {
            "uid": name,
            "name": name,
            "node": "node-1",
            "placement_revision": 1,
            "deployment_id": deployment,
            "ready": True,
            "state": "Serving",
            "started_at": {"seconds": now - 3600},
        }
        for name in SCALED_PODS
    ]
    snapshot = {"placementId": placement, "replicas": replicas}
    sql(
        state,
        "INSERT INTO deployment_rollouts(deployment_id,cluster_id,revision,snapshot) "
        f"VALUES('{deployment}','{cluster}',1,{evidence.sql_literal(json.dumps(snapshot))}) "
        "ON CONFLICT(deployment_id,cluster_id,revision) DO UPDATE SET snapshot=EXCLUDED.snapshot;",
    )


usage = {
    "k8s.pod.cpu_limit_utilization": {
        "replicas-ui-east-a1": 0.45,
        "replicas-ui-east-b2": 0.9,
        "replicas-ui-west-c3": 0.1,
    },
    "k8s.pod.memory.working_set_limit_utilization": {
        "replicas-ui-east-a1": 0.6,
        "replicas-ui-east-b2": 0.99,
        "replicas-ui-west-c3": 0.25,
    },
}


def heartbeat(region, age):
    sql(
        state,
        f"UPDATE clusters SET last_heartbeat=NOW()-INTERVAL '{age}' WHERE id='{r['clusters'][region]}';",
    )


with session(repo / "tests/ui/out/resource-replicas") as browser:
    heartbeat(EAST, "0 seconds")
    heartbeat(WEST, "0 seconds")
    context, page, errors = open_page(browser, state=state)
    pod_requests = []

    def metrics(route):
        body = route.request.post_data_json
        if not body.get("byPod"):
            stamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() - 60))
            band = [
                {
                    "resourceId": scaling["scaled"],
                    "labels": {},
                    "points": [{"timestamp": stamp, "value": 0.42}],
                }
            ]
            scaled = body["resourceIds"][0] == scaling["scaled"]
            route.fulfill(json={"series": band if scaled else []})
            return
        pod_requests.append(body)
        stamps = [
            time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() - ago))
            for ago in (90, 30)
        ]
        if body["resourceIds"][0] == scaling["scaled"]:
            route.fulfill(
                json={
                    "series": [
                        {
                            "resourceId": scaling["scaled"],
                            "labels": {"k8s.pod.name": pod},
                            "points": [
                                {"timestamp": stamp, "value": 0.42} for stamp in stamps
                            ],
                        }
                        for pod in SCALED_PODS
                    ]
                }
            )
            return
        route.fulfill(
            json={
                "series": [
                    {
                        "resourceId": r["resource"],
                        "labels": {"k8s.pod.name": pod},
                        "points": [
                            {"timestamp": stamp, "value": value} for stamp in stamps
                        ],
                    }
                    for pod, value in usage.get(body["metricName"], {}).items()
                    if pod.split("-")[2]
                    == ("east" if body.get("region") == EAST else "west")
                ]
            }
        )

    context.route("**/loco.observability.v1.ObservabilityService/QueryMetrics", metrics)
    report_replicas()
    page.goto(state["ui_url"] + path)

    panel = page.get_by_role("region", name="Metrics", exact=True)
    east = panel.get_by_role("group", name=f"Replicas in {EAST}", exact=True)
    west = panel.get_by_role("group", name=f"Replicas in {WEST}", exact=True)
    expect(east.get_by_role("button")).to_have_count(2)
    expect(west.get_by_role("button")).to_have_count(2)
    expect(panel.get_by_text("Per-replica status", exact=False)).to_have_count(0)

    expect(east).to_contain_text("1/2 ready")
    expect(west).to_contain_text("2/2 ready")
    expect(panel.get_by_text("average per region", exact=True)).to_have_count(2)

    def cell(region, short):
        return region.get_by_role("button", name=re.compile(rf"^{short}, "))

    def replica_popover(region, short):
        cell(region, short).click()
        popover = page.locator('[data-slot="popover-content"][data-open]')
        expect(popover).to_be_visible()
        return popover

    popover = replica_popover(east, "a1")
    expect(popover).to_contain_text("a1")
    expect(popover).to_contain_text("Serving")
    expect(popover).to_contain_text("CPU 0.23/0.5 vCPU")
    expect(popover).to_contain_text("Mem 154/256 Mi")
    logs_href = popover.get_by_role(
        "link", name="Replica logs", exact=True
    ).get_attribute("href")
    assert parse_qs(urlparse(logs_href).query)["q"] == [
        "@replica:replicas-ui-east-a1"
    ], logs_href
    popover.get_by_role("button", name="Close", exact=True).click()
    expect(page.locator('[data-slot="popover-content"][data-open]')).to_have_count(0)
    popover = replica_popover(east, "b2")
    expect(popover).to_contain_text("Failing, 4 restarts")
    expect(popover).to_contain_text("CPU 0.45/0.5 vCPU")
    page.keyboard.press("Escape")
    popover = replica_popover(west, "d4")
    expect(popover).to_contain_text("CPU —")
    expect(popover).to_contain_text("Mem —")
    page.keyboard.press("Escape")
    assert {
        (q.get("region"), q["resourceIds"][0], q["metricName"]) for q in pod_requests
    } >= {
        (region, r["resource"], metric) for region in (EAST, WEST) for metric in usage
    }, pod_requests
    expect(west).not_to_contain_text("Last reported")

    card = page.get_by_role("region", name="Deploying", exact=True)
    expect(card).to_have_count(1)
    lanes = card.get_by_role("list", name="Regions", exact=True).get_by_role("listitem")
    expect(lanes).to_have_count(2)
    for name in (EAST, WEST):
        expect(lanes.filter(has_text=name)).to_contain_text(re.compile(r"\d/2 ready"))
    lanes.filter(has_text=WEST).get_by_role("button").click()
    expect(card.get_by_role("region", name="New version", exact=True)).to_contain_text(
        "replicas-ui-west-c3"
    )
    lanes.filter(has_text=EAST).get_by_role("button").click()
    expect(card.get_by_role("region", name="New version", exact=True)).to_contain_text(
        "replicas-ui-east-b2"
    )

    page.get_by_role("button", name="Workspace activity", exact=False).click()
    page.mouse.move(0, 0)
    drawer = page.get_by_role("dialog")
    cards = (
        drawer.get_by_role("listitem")
        .filter(has_text="replicas-ui")
        .filter(has_text="Deploying")
    )
    expect(cards).to_have_count(1)
    for name in (EAST, WEST):
        expect(cards.first.get_by_title(re.compile(f"^{name}: "))).to_be_visible()
    page.keyboard.press("Escape")

    history = page.get_by_role("tabpanel", name="Deployments")
    history.get_by_role("button", name="Add a column", exact=True).click()
    page.get_by_role("menuitemcheckbox", name="Region(s)", exact=True).click()
    page.keyboard.press("Escape")
    rollout_rows = history.get_by_role("row").filter(has_text=re.compile(EAST))
    expect(rollout_rows).to_have_count(1)
    expect(rollout_rows.first).to_contain_text(EAST)
    expect(rollout_rows.first).to_contain_text(WEST)

    def resource_path(resource):
        return f"/org/{r['org']}/wks/{workspace}/resource/{resource}?env=production"

    report_scaled()
    page.goto(state["ui_url"] + resource_path(scaling["scaled"]))
    panel = page.get_by_role("region", name="Metrics", exact=True)
    scaled_east = panel.get_by_role("group", name=f"Replicas in {EAST}", exact=True)
    expect(scaled_east.get_by_role("button")).to_have_count(SCALED_REPLICAS)
    expect(scaled_east).not_to_contain_text("avg")
    expect(panel.locator('[aria-label="CPU: average"]')).to_be_visible()
    expect(panel.locator('[aria-label="Memory: average"]')).to_be_visible()
    expect(panel.get_by_text("average", exact=True)).to_have_count(2)

    page.goto(state["ui_url"] + path)
    # The evidence API counts a cluster silent after three missed two-hour heartbeats.
    heartbeat(WEST, "7 hours")
    report_replicas()
    page.reload()
    west = page.get_by_role("group", name=f"Replicas in {WEST}", exact=True)
    expect(west.get_by_role("button")).to_have_count(2)
    expect(west).to_contain_text("Last reported")

    # A secret the resource reads that changed after its oldest replica started asks for a restart.
    changed_at = [time.time()]

    def secrets(route):
        stamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(changed_at[0]))
        route.fulfill(
            json={
                "secrets": [
                    {
                        "name": "STRIPE_SECRET_KEY",
                        "version": 2,
                        "updatedAt": stamp,
                        "usedBy": [{"resourceId": r["resource"]}],
                    },
                    {
                        "name": "OTHER_KEY",
                        "version": 1,
                        "updatedAt": stamp,
                        "usedBy": [{"resourceId": str(uuid.uuid4())}],
                    },
                ]
            }
        )

    context.route("**/loco.secret.v1.SecretService/ListSecrets", secrets)
    report_replicas()
    page.reload()
    notice = page.locator("#secrets-notice-ov")
    expect(notice).to_have_text(
        re.compile(
            r"^STRIPE_SECRET_KEY changed after this deployment\. Restart to use the new value\.\s*Restart$"
        )
    )
    notice.get_by_role("button", name="Restart", exact=True).click()
    restart = page.get_by_role("dialog", name="Restart replicas-ui", exact=True)
    expect(restart).to_be_visible()
    restart.get_by_role("button", name="Cancel", exact=True).click()
    page.goto(state["ui_url"] + path + "&tab=settings")
    expect(page.locator("#secrets-notice-set")).to_contain_text("STRIPE_SECRET_KEY")
    # A change made before every running replica started shows no notice.
    changed_at[0] = time.time() - 4 * 3600
    page.goto(state["ui_url"] + path)
    expect(page.get_by_role("region", name="Metrics", exact=True)).to_be_visible()
    expect(page.locator("#secrets-notice-ov")).to_have_count(0)
    context.unroute("**/loco.secret.v1.SecretService/ListSecrets")

    # A resource that first started under two minutes ago shows a line instead of its charts.
    fresh_name = "fresh-" + uuid.uuid4().hex[:8]
    fresh = evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": fresh_name,
                "deployments": [
                    {
                        "environment": "production",
                        "spec": {
                            "platform": "linux/amd64",
                            "observability": {"injectOtel": True},
                            "build": {
                                "type": "image",
                                "image": "registry.example.test/fresh:v1",
                            },
                            "port": 8080,
                            "regions": {
                                EAST: {
                                    "cpu": "500m",
                                    "memory": "256Mi",
                                    "minReplicas": 1,
                                    "maxReplicas": 1,
                                }
                            },
                        },
                        "age": "30 seconds",
                        "placements": {
                            EAST: {
                                "cluster": r["clusters"][EAST],
                                "replicas": [
                                    {
                                        "name": f"{fresh_name}-a1",
                                        "ready": True,
                                        "state": "Serving",
                                        "started_at": {"seconds": "{now-20s:s}"},
                                    }
                                ],
                            }
                        },
                    }
                ],
            }
        ],
    )[f"acme/shared/{fresh_name}"]
    clocked, clocked_page, clocked_errors = open_page(
        browser, state=state, now=datetime.datetime.now(datetime.UTC)
    )
    clocked.route(
        "**/loco.observability.v1.ObservabilityService/QueryMetrics",
        lambda route: route.fulfill(json={"series": []}),
    )
    clocked_page.goto(state["ui_url"] + resource_path(fresh))
    fresh_panel = clocked_page.get_by_role("region", name="Metrics", exact=True)
    expect(fresh_panel).to_contain_text(
        f"CPU and memory show up a couple of minutes after {fresh_name} first starts."
    )
    expect(fresh_panel.get_by_text("No data in this range", exact=True)).to_have_count(
        0
    )
    fresh_replica = fresh_panel.get_by_role(
        "group", name=f"Replicas in {EAST}", exact=True
    ).get_by_role("button")
    expect(fresh_replica).to_have_count(1)
    fresh_replica.click()
    fresh_popover = clocked_page.locator('[data-slot="popover-content"][data-open]')
    expect(fresh_popover).not_to_contain_text("CPU")
    clocked_page.keyboard.press("Escape")
    expect(
        clocked_page.get_by_role("group", name="Time range", exact=True)
    ).to_have_count(0)
    clocked_page.clock.run_for(2 * 60_000)
    expect(fresh_panel.get_by_text("No data in this range", exact=True)).to_have_count(
        2
    )
    expect(fresh_panel).not_to_contain_text("a couple of minutes")
    fresh_replica.click()
    expect(fresh_popover).to_contain_text("CPU —")
    expect(
        clocked_page.get_by_role("group", name="Time range", exact=True)
    ).to_be_visible()
    clocked.close()

    narrow, narrow_page, narrow_errors = open_page(browser, state=state, narrow=True)
    narrow.route("**/loco.observability.v1.ObservabilityService/QueryMetrics", metrics)
    report_replicas()
    narrow_page.goto(state["ui_url"] + path)
    narrow_east = narrow_page.get_by_role(
        "group", name=f"Replicas in {EAST}", exact=True
    )
    expect(narrow_east.get_by_role("button")).to_have_count(2)
    width = narrow_page.evaluate("document.documentElement.scrollWidth")
    viewport = narrow_page.evaluate("window.innerWidth")
    assert width <= viewport, (width, viewport)
    assert errors == clocked_errors == narrow_errors == [], (
        errors,
        clocked_errors,
        narrow_errors,
    )
