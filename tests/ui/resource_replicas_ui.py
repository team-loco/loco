import argparse
import json
import time
import uuid
from pathlib import Path

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
EAST, WEST = "us-east-1", "eu-west-1"

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

r = state["replicas"]
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
            ),
        ],
        WEST: [
            replica("replicas-ui-west-c3", deployment),
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
        stamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() - 30))
        route.fulfill(
            json={
                "series": [
                    {
                        "resourceId": r["resource"],
                        "labels": {"k8s.pod.name": pod},
                        "points": [{"timestamp": stamp, "value": value}],
                    }
                    for pod, value in usage[body["metricName"]].items()
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

    serving = east.get_by_role("listitem").filter(has_text="replicas-ui-east-a1")
    expect(serving).to_contain_text("Serving")
    expect(serving).to_contain_text("0 restarts")
    expect(serving).to_contain_text("Up 3h")
    expect(serving).to_contain_text("CPU 45% of 0.5 vCPU")
    expect(serving).to_contain_text("Memory 60% of 256 Mi")

    crashed = east.get_by_role("listitem").filter(has_text="replicas-ui-east-b2")
    expect(crashed).to_contain_text("Failing")
    expect(crashed).to_contain_text("4 restarts")
    expect(crashed).to_contain_text("OOMKilled · exit 137 · 5m ago")
    expect(crashed).to_contain_text("Up 20m")
    expect(crashed).to_contain_text("CPU 90% of 0.5 vCPU")

    unmeasured = west.get_by_role("listitem").filter(has_text="replicas-ui-west-d4")
    expect(unmeasured).to_contain_text("CPU —")
    expect(unmeasured).to_contain_text("Memory —")
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
    chips.get_by_role("button", name=WEST).click()
    expect(card.get_by_role("region", name="New version", exact=True)).to_contain_text(
        "replicas-ui-west-c3"
    )
    chips.get_by_role("button", name=EAST).click()
    expect(card.get_by_role("region", name="New version", exact=True)).to_contain_text(
        "replicas-ui-east-b2"
    )

    page.get_by_role("button", name="Activity", exact=False).click()
    drawer = page.get_by_role("dialog")
    cards = (
        drawer.get_by_role("listitem")
        .filter(has_text="replicas-ui")
        .filter(has_text="Deploying")
    )
    expect(cards).to_have_count(1)
    expect(cards.first).to_contain_text(EAST)
    expect(cards.first).to_contain_text(WEST)
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
