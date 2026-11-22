import argparse
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
                        "age": "40 seconds",
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
    mixed = deployment(
        {
            EAST: {"minReplicas": 1, "maxReplicas": 1},
            WEST: {
                "minReplicas": 1,
                "maxReplicas": 3,
                "scalers": {"enabled": True, "cpuTarget": 70},
            },
        },
        1,
    )
    seeded = evidence.seed_resources(
        state,
        [
            {"workspace": "acme/shared", "name": "scaled-ui", "deployments": [scaled]},
            {"workspace": "acme/shared", "name": "mixed-ui", "deployments": [mixed]},
        ],
    )
    state["scaling"] = {
        "scaled": seeded["acme/shared/scaled-ui"],
        "scaled_deployment": scaled["id"],
        "mixed": seeded["acme/shared/mixed-ui"],
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
            route.fulfill(json={"series": []})
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

    panel = page.get_by_role("region", name="Replicas", exact=True)
    east = panel.get_by_role("region", name=f"Replicas in {EAST}", exact=True)
    west = panel.get_by_role("region", name=f"Replicas in {WEST}", exact=True)
    expect(east.get_by_role("listitem")).to_have_count(2)
    expect(west.get_by_role("listitem")).to_have_count(2)
    expect(panel.get_by_text("Per-replica status", exact=False)).to_have_count(0)

    scale = page.locator('[data-slot="scale-chip"]')
    expect(scale).to_have_text("Fixed at 2")
    scale.hover()
    expect(
        page.get_by_text("Runs 2 replicas per region, autoscaling off", exact=True)
    ).to_be_visible()
    expect(page.locator('[data-slot="region-chip"]')).to_have_text([EAST, WEST])
    expect(east).to_contain_text("1 of 2 ready")
    expect(west).to_contain_text("2 of 2 ready")

    def chip(region, short):
        return region.get_by_role("listitem").filter(
            has=page.get_by_text(short, exact=True)
        )

    serving = chip(east, "a1")
    expect(serving.get_by_role("button")).to_have_accessible_name(
        re.compile(r"^a1 Serving ")
    )
    expect(serving.get_by_text("Serving", exact=True)).to_have_class(
        re.compile(r"\bsr-only\b")
    )
    expect(serving).to_contain_text("CPU 0.23/0.5")
    expect(serving).to_contain_text("Mem 154/256 Mi")
    crashed = chip(east, "b2")
    expect(crashed).to_contain_text("Failing, 4 restarts")
    expect(crashed).to_contain_text("CPU 0.45/0.5")
    unmeasured = chip(west, "d4")
    expect(unmeasured).to_contain_text("CPU —")
    expect(unmeasured).to_contain_text("Mem —")

    def replica_detail(item, name):
        item.get_by_role("button").click()
        detail = panel.get_by_role("region", name=f"Replica {name}", exact=True)
        expect(detail).to_be_visible()
        return detail

    detail = replica_detail(serving, "replicas-ui-east-a1")
    expect(detail).to_contain_text("0 restarts")
    expect(detail).to_contain_text("Up 3h")
    logs_href = detail.get_by_role("link", name="Logs", exact=True).get_attribute(
        "href"
    )
    assert parse_qs(urlparse(logs_href).query)["q"] == [
        "@replica:replicas-ui-east-a1"
    ], logs_href
    detail = replica_detail(crashed, "replicas-ui-east-b2")
    expect(detail).to_contain_text("4 restarts, last 5m ago, out of memory")
    expect(detail).not_to_contain_text("OOMKilled")
    expect(detail).to_contain_text("Up 20m")
    exited = chip(west, "c3")
    detail = replica_detail(exited, "replicas-ui-west-c3")
    expect(detail).to_contain_text("0 restarts")
    expect(detail).to_contain_text("Completed, exit 0")
    exited.get_by_role("button").click()
    expect(
        panel.get_by_role("region", name="Replica replicas-ui-west-c3")
    ).to_have_count(0)
    panel.get_by_role("group", name="Region", exact=True).get_by_role(
        "button", name=EAST, exact=True
    ).click()
    expect(
        panel.locator('[aria-label^="Memory: one line per replica"]')
    ).to_be_visible()
    expect(panel.get_by_text("Highest, b2", exact=True)).to_be_visible()
    expect(east.locator('[data-slot="replica-key"]')).to_have_count(2)
    lines = panel.locator(
        '[aria-label^="Memory: one line per replica"] .recharts-line-curve'
    )
    expect(lines).to_have_count(2)
    chip(east, "b2").hover()
    expect(lines.nth(0)).to_have_attribute("stroke-opacity", "0.25")
    expect(lines.nth(1)).to_have_attribute("stroke-opacity", "1")
    page.mouse.move(0, 0)
    show_all = panel.get_by_role("button", name="Show all", exact=True)
    show_all.click()
    expect(show_all).to_have_count(0)
    expect(
        panel.get_by_role("group", name="Region", exact=True).get_by_role(
            "button", name="All regions", exact=True
        )
    ).to_have_attribute("aria-pressed", "true")
    assert {
        (q.get("region"), q["resourceIds"][0], q["metricName"]) for q in pod_requests
    } >= {
        (region, r["resource"], metric) for region in (EAST, WEST) for metric in usage
    }, pod_requests
    expect(west).not_to_contain_text("Last reported")

    card = page.get_by_role("region", name="Deploying", exact=True)
    expect(card).to_have_count(1)
    expect(card).to_contain_text("Rolling out to 2 regions")
    expect(card).to_contain_text("3 of 4 ready in 2 regions")
    chips = card.get_by_role("group", name="Rollout regions", exact=True)
    expect(chips.get_by_role("button")).to_have_count(2)
    for name in (EAST, WEST):
        expect(chips.get_by_role("button", name=name)).to_contain_text(
            re.compile(r"\d/2$")
        )
    chips.get_by_role("button", name=WEST).click()
    expect(card.get_by_role("region", name="New version", exact=True)).to_contain_text(
        "replicas-ui-west-c3"
    )
    chips.get_by_role("button", name=EAST).click()
    expect(card.get_by_role("region", name="New version", exact=True)).to_contain_text(
        "replicas-ui-east-b2"
    )

    page.get_by_role("button", name="Workspace activity", exact=False).click()
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
    rollout_rows = history.get_by_role("row").filter(has_text="v2")
    expect(rollout_rows).to_have_count(1)
    expect(rollout_rows.first).to_contain_text(EAST)
    expect(rollout_rows.first).to_contain_text(WEST)
    rollout_rows.first.get_by_role("button", name="Show details", exact=False).click()
    expect(
        history.get_by_text(f"First deployment in {EAST}", exact=True)
    ).to_be_visible()
    expect(
        history.get_by_text(f"First deployment in {WEST}", exact=True)
    ).to_be_visible()

    def resource_path(resource):
        return f"/org/{r['org']}/wks/{workspace}/resource/{resource}?env=production"

    report_scaled()
    page.goto(state["ui_url"] + resource_path(scaling["scaled"]))
    expect(scale).to_have_text("CPU autoscaling 2–20")
    scale.hover()
    expect(
        page.get_by_text(
            "Scales between 2 and 20 replicas per region, targeting 70% CPU",
            exact=True,
        )
    ).to_be_visible()
    panel = page.get_by_role("region", name="Replicas", exact=True)
    scaled_east = panel.get_by_role("region", name=f"Replicas in {EAST}", exact=True)
    expect(scaled_east.get_by_role("listitem")).to_have_count(SCALED_REPLICAS)
    expect(scaled_east).to_contain_text("avg 0.42 vCPU")
    expect(
        panel.get_by_text(
            f"band shows lowest to highest of {SCALED_REPLICAS} replicas", exact=True
        )
    ).to_have_count(2)
    expect(scaled_east.locator('[data-slot="replica-key"]')).to_have_count(0)

    page.goto(state["ui_url"] + resource_path(scaling["mixed"]))
    expect(scale).to_have_text("Scaling differs by region")

    page.goto(state["ui_url"] + path)
    # The evidence API counts a cluster silent after three missed two-hour heartbeats.
    heartbeat(WEST, "7 hours")
    report_replicas()
    page.reload()
    west = page.get_by_role("region", name=f"Replicas in {WEST}", exact=True)
    expect(west.get_by_role("listitem")).to_have_count(2)
    expect(west).to_contain_text("Last reported")

    narrow, narrow_page, narrow_errors = open_page(browser, state=state, narrow=True)
    narrow.route("**/loco.observability.v1.ObservabilityService/QueryMetrics", metrics)
    report_replicas()
    narrow_page.goto(state["ui_url"] + path)
    narrow_east = narrow_page.get_by_role(
        "region", name=f"Replicas in {EAST}", exact=True
    )
    expect(narrow_east.get_by_role("listitem")).to_have_count(2)
    width = narrow_page.evaluate("document.documentElement.scrollWidth")
    viewport = narrow_page.evaluate("window.innerWidth")
    assert width <= viewport, (width, viewport)
    assert errors == [] and narrow_errors == [], (errors, narrow_errors)
