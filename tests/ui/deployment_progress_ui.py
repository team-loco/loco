import argparse
import json
import re
import time
import uuid
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from harness import (
    call,
    copied_text,
    evidence,
    line_starts,
    load_state,
    open_page,
    repo,
    session,
    sql,
)
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check deployment progress, replica watches and recovery"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(
    args, "deployment-progress", "tests/ui/fixtures/deployment-history/scenario.json"
)
owner = state["users"]["owner"]
workspace = state["workspaces"]["acme/shared"]


if "progress" not in state:
    org = state["orgs"]["acme"]["id"]
    production = next(
        env["id"]
        for env in call(
            state,
            "loco.environment.v1.EnvironmentService/ListEnvironments",
            {"workspaceId": workspace},
        )["environments"]
        if env["slug"] == "production"
    )
    previous, current, build = (str(uuid.uuid4()) for _ in range(3))
    old_image = "registry.example.test/progress:v1@sha256:" + "1" * 64
    image = "registry.example.test/progress:v2@sha256:" + "2" * 64
    old_spec = {
        "platform": "linux/amd64",
        "observability": {"injectOtel": True},
        "build": {"type": "image", "image": old_image},
        "port": 8080,
        "regions": {
            "us-ca-1": {
                "cpu": "100m",
                "memory": "128Mi",
                "minReplicas": 2,
                "maxReplicas": 2,
            }
        },
    }
    spec = {
        **old_spec,
        "build": {"type": "dockerfile", "image": image, "buildId": build},
        "healthCheck": {
            "path": "/health",
            "intervalSeconds": 10,
            "timeoutSeconds": 2,
            "failureThreshold": 3,
        },
    }
    cluster = evidence.seed_cluster(state, "us-ca-1", name="progress-ui")
    target = {
        "environment": "production",
        "placements": {"us-ca-1": {"cluster": cluster}},
    }
    resource = evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": "progress-ui",
                "deployments": [
                    {**target, "id": previous, "spec": old_spec, "age": "10 minutes"},
                    {
                        **target,
                        "id": current,
                        "spec": spec,
                        "status": "building",
                        "live": False,
                        "age": "45 seconds",
                    },
                ],
            }
        ],
    )["acme/shared/progress-ui"]
    sql(
        state,
        f"""BEGIN;
    INSERT INTO builds(method, origin_environment_id,platform, id,resource_id,status,source_type,source_key,source_size,dockerfile_path,image_repository,image_digest,created_by,started_at,finished_at,git_commit,git_branch,git_commit_message)
    VALUES('cli', '{production}','linux/amd64', '{build}','{resource}','succeeded','upload','progress-{build}',100,'Dockerfile','registry.example.test/progress','sha256:{"2" * 64}','{owner["id"]}',NOW()-INTERVAL '2 minutes',NOW()-INTERVAL '52 seconds','{"a" * 40}','main','Show saved cards');
    UPDATE deployments SET completed_at=NOW()-INTERVAL '9 minutes' WHERE id='{previous}';
    UPDATE deployments SET spec_version=2, commit_sha='{"a" * 40}' WHERE id='{current}';
    COMMIT;""",
    )
    state["progress"] = {
        "org": org,
        "environment": production,
        "resource": resource,
        "cluster": cluster,
        "previous": previous,
        "current": current,
        "old_image": old_image,
    }
    evidence.write_json(Path(args.state), state)

p = state["progress"]
path = f"/org/{p['org']}/wks/{workspace}/resource/{p['resource']}?env=production"


def phase(status, problem_reason=None, *, new_ready=False, survivors=True):
    building = status in ("building", "canceled")
    now = int(time.time())
    replica = {
        "uid": "new-replica",
        "name": "progress-new-1",
        "node": "worker-east-3",
        "placement_revision": 2,
        "deployment_id": p["current"],
        "ready": status == "running" or new_ready,
        "state": "Failing"
        if status == "failed"
        else "Serving"
        if status == "running" or new_ready
        else "Starting",
        "detail": "Process exited with code 1"
        if status == "failed"
        else "Accepting traffic"
        if status == "running" or new_ready
        else "Waiting for readiness",
    }
    previous = [
        {
            "uid": name,
            "name": name,
            "node": f"worker-east-{index}",
            "placement_revision": 1,
            "deployment_id": p["previous"],
            "ready": building
            or status == "failed"
            and survivors
            or status == "deploying"
            and index == 1,
            "state": "Stopped"
            if status == "running" or status == "failed" and not survivors
            else "Serving"
            if status == "failed" or building
            else "Stopping"
            if index == 2
            else "Serving",
            "detail": "Shutdown complete"
            if status == "running" or status == "failed" and not survivors
            else "Draining requests"
            if index == 2
            else "Serving the previous version",
        }
        for index, name in enumerate(("progress-old-1", "progress-old-2"), 1)
    ]
    previous[1].update(
        state_since={"seconds": now - 10}, state_deadline={"seconds": now + 50}
    )
    replicas = [] if building else [replica, *previous]
    if status == "running":
        replicas.append(
            {
                **replica,
                "uid": "new-replica-2",
                "name": "progress-new-2",
                "node": "worker-east-4",
            }
        )
    ready_count = 2 if building else sum(row["ready"] for row in replicas)
    snapshot = {
        "placementId": sql(
            state, f"SELECT id FROM placements WHERE resource_id='{p['resource']}';"
        ),
        "replicas": replicas,
    }
    old_snapshot = {"placementId": snapshot["placementId"], "replicas": previous}
    reason = problem_reason or ("restarting" if status == "failed" else None)
    if reason is not None:
        snapshot["problem"] = {
            "reason": reason,
            "detail": "Memory limit exceeded"
            if reason == "out_of_memory"
            else "Process exited with code 1",
            "replica_name": replica["name"],
        }
    last_seq = int(
        sql(
            state,
            f"SELECT COALESCE(MAX(seq),0) FROM deployment_activity WHERE deployment_id='{p['current']}';",
        )
    )
    entries = [
        ("info", "", "Deployment requested"),
        ("info", "", "Preparing the new version"),
        ("info", replica["name"], "Waiting for a node"),
        ("ok", replica["name"], "Scheduled on worker-east-3"),
        ("info", replica["name"], "Pulling image"),
        ("ok", replica["name"], "Image pulled"),
        ("info", replica["name"], "Starting application"),
        ("warn", previous[1]["name"], "Draining previous replica"),
        ("error" if reason else "info", replica["name"], f"Deployment {status}"),
    ]
    activity = ",".join(
        f"('{p['current']}',{last_seq + index},2,NOW()-INTERVAL '{len(entries) - index} seconds',{evidence.sql_literal(severity)},{evidence.sql_literal(name)},{evidence.sql_literal(message)},"
        + (f"'{p['cluster']}','us-ca-1')" if name else "NULL,'')")
        for index, (severity, name, message) in enumerate(entries, 1)
    )
    completed = "NULL" if status in ("building", "deploying", "running") else "NOW()"
    sql(
        state,
        f"""BEGIN;
    UPDATE clusters SET last_heartbeat=NOW() WHERE id='{p["cluster"]}';
    UPDATE deployments SET status='{status}',updated_at=NOW(),completed_at={completed} WHERE id='{p["current"]}';
    UPDATE deployment_placements SET status='{"pending" if status == "building" else status}',image={"''" if status == "building" else "spec->'build'->>'image'"},started_at={"NOW()" if status == "running" else "NULL"},completed_at={completed},updated_at=NOW()
    FROM deployments WHERE deployments.id=deployment_placements.deployment_id AND deployment_id='{p["current"]}';
    UPDATE placements SET deployment_id='{p["previous"] if building else p["current"]}',desired_revision={1 if building else 2},observed_revision={1 if building else 2},ready_replicas={ready_count},status_updated_at=NOW() WHERE resource_id='{p["resource"]}';
    INSERT INTO deployment_rollouts(deployment_id,cluster_id,revision,snapshot)
    VALUES('{p["previous"]}','{p["cluster"]}',1,{evidence.sql_literal(json.dumps(old_snapshot))})
    ON CONFLICT(deployment_id,cluster_id,revision) DO NOTHING;
    INSERT INTO deployment_rollouts(deployment_id,cluster_id,revision,snapshot)
    VALUES('{p["current"]}','{p["cluster"]}',2,{evidence.sql_literal(json.dumps(snapshot))})
    ON CONFLICT(deployment_id,cluster_id,revision) DO UPDATE SET snapshot=EXCLUDED.snapshot;
    INSERT INTO deployment_activity(deployment_id,seq,revision,at,severity,replica_name,message,cluster_id,region) VALUES {activity};
    COMMIT;""",
    )


with session(repo / "tests/ui/out/deployment-progress") as browser:
    sql(state, f"DELETE FROM deployment_activity WHERE deployment_id='{p['current']}';")
    phase("building")
    context, page, errors = open_page(browser, state=state)
    page.goto(state["ui_url"] + path)
    panel = page.get_by_role("region", name="Building", exact=True)
    expect(panel).to_be_visible()
    expect(panel.get_by_text("Deploy aaaaaaa", exact=True)).to_be_visible()
    versions = panel.locator('dl[aria-label="Deployment versions"]')
    expect(
        versions.get_by_text("Desired deployment", exact=True).locator("+ dd")
    ).to_contain_text(p["current"][-8:])
    expect(
        versions.get_by_text("Serving deployments", exact=True).locator("+ dd")
    ).to_contain_text(p["previous"][-8:])
    expect(
        panel.get_by_role("region", name="New version", exact=True).get_by_text(
            "No replicas", exact=True
        )
    ).to_be_visible()
    activity = panel.get_by_role("region", name="Activity feed", exact=True)
    expect(activity.get_by_role("listitem")).to_have_count(9)
    expect(activity.get_by_role("listitem").last).to_contain_text("Deployment building")
    expect(activity.get_by_role("listitem").first).to_contain_text(
        re.compile(r"^\+\d+s after the rollout started")
    )
    panel.get_by_role("button", name="Copy deployment ID", exact=True).click()
    assert copied_text(page) == p["current"]
    panel.get_by_role("button", name="What’s being deployed", exact=True).click()
    expect(panel.get_by_text("0.1 vCPU, 128 Mi", exact=True)).to_be_visible()
    expect(panel.get_by_text("/health every 10s", exact=True)).to_be_visible()
    phase("deploying")
    page.reload()
    panel = page.get_by_role("region", name="Deploying", exact=True)
    new_version = panel.get_by_role("region", name="New version", exact=True)
    old_version = panel.get_by_role("region", name="Previous version", exact=True)
    expect(new_version.get_by_text("progress-new-1", exact=True)).to_be_visible()
    expect(new_version.get_by_text("worker-east-3", exact=True)).to_be_visible()
    expect(new_version.get_by_text("Starting", exact=True)).to_be_visible()
    expect(new_version.get_by_text("Waiting for readiness", exact=True)).to_be_visible()
    expect(old_version.get_by_role("listitem")).to_have_count(2)
    expect(old_version.get_by_text("Serving", exact=True)).to_be_visible()
    expect(old_version.get_by_text("Stopping", exact=True)).to_be_visible()
    countdown = old_version.get_by_role(
        "progressbar", name="progress-old-2 stopping time remaining", exact=True
    )
    expect(countdown).to_be_visible()
    remaining = countdown.get_attribute("aria-valuenow")
    expect(countdown).not_to_have_attribute("aria-valuenow", remaining, timeout=3000)
    versions = panel.locator('dl[aria-label="Deployment versions"]')
    serving = versions.get_by_text("Serving deployments", exact=True).locator("+ dd")
    expect(serving).to_contain_text(p["previous"][-8:])
    expect(serving).not_to_contain_text(p["current"][-8:])
    serving.get_by_role("button").focus()
    page.keyboard.press("Enter")
    assert copied_text(page) == p["previous"]
    phase("deploying", new_ready=True)
    expect(serving).to_contain_text(p["current"][-8:], timeout=25000)
    expect(serving).to_contain_text(p["previous"][-8:])
    page.reload()
    panel = page.get_by_role("region", name="Deploying", exact=True)
    expect(panel.get_by_role("list", name="Rollout steps", exact=True)).to_contain_text(
        "1 of 2 ready"
    )
    replicas = page.get_by_role("region", name="Replicas", exact=True)
    expect(replicas).to_contain_text("1 of 2 ready, 1 previous")
    phase("running")
    # The open watch updates both the replicas and feed without reloading.
    panel = page.get_by_role("region", name="Running", exact=True)
    expect(panel).to_be_visible(timeout=25000)
    expect(
        panel.get_by_role("region", name="New version", exact=True).get_by_text(
            "Serving", exact=True
        )
    ).to_have_count(2)
    stopped = panel.get_by_role(
        "region", name="Previous version", exact=True
    ).get_by_role("listitem")
    expect(stopped).to_have_count(2)
    for row in stopped.all():
        expect(row.get_by_text("Stopped", exact=True)).to_be_visible()
    expect(
        panel.get_by_role("region", name="Activity feed", exact=True)
        .get_by_role("listitem")
        .last
    ).to_contain_text("Deployment running")
    phase("failed")
    page.reload()
    panel = page.get_by_role("region", name="Failed", exact=True)
    versions = panel.locator('dl[aria-label="Deployment versions"]')
    expect(
        versions.get_by_text("Desired deployment", exact=True).locator("+ dd")
    ).to_contain_text(p["current"][-8:])
    serving = versions.get_by_text("Serving deployments", exact=True).locator("+ dd")
    expect(serving).to_contain_text(p["previous"][-8:])
    expect(serving).to_contain_text("(2 ready)")
    expect(serving).not_to_contain_text(p["current"][-8:])
    incident = page.locator('section[aria-labelledby="incident-title"]')
    expect(incident.get_by_text("Running", exact=True).locator("+ dd")).to_have_text(
        re.compile(
            r"^v1, the deployment serving this region, deployed (just now|\d+[mhd] ago)( by .+)?$"
        )
    )
    phase("failed", survivors=False)
    expect(serving).to_have_text("No ready replicas reported", timeout=25000)
    phase("failed")
    expect(serving).to_contain_text(p["previous"][-8:], timeout=25000)
    expect(
        panel.get_by_text(
            "A replica keeps restarting. Check its logs.",
            exact=True,
        ).first
    ).to_be_visible()
    expect(
        panel.get_by_text("v1 is still serving traffic.", exact=True)
    ).to_be_visible()
    expect(panel.get_by_role("link", name="Build logs", exact=True)).to_be_visible()
    logs = panel.get_by_role("link", name="View logs", exact=True)
    query = parse_qs(urlparse(logs.get_attribute("href")).query)
    assert query.get("r") == ["progress-ui"] and query.get("q") == [
        "@replica:progress-new-1"
    ], query
    expect(
        panel.get_by_role("region", name="New version", exact=True).get_by_text(
            "Failing", exact=True
        )
    ).to_be_visible()
    expect(logs).to_have_count(1)
    panel.get_by_role("button", name="Roll back to v1", exact=True).click()
    dialog = page.get_by_role(
        "dialog", name="Roll back progress-ui to v1 in us-ca-1?", exact=True
    )
    expect(dialog).to_be_visible()
    mutations = []

    def rollback(route):
        mutations.append(route.request.post_data_json)
        route.fulfill(json={"deploymentId": str(uuid.uuid4())})

    context.route("**/loco.deployment.v1.DeploymentService/CreateDeployment", rollback)
    dialog.get_by_role("button", name="Roll back", exact=True).click()
    expect(dialog).to_be_hidden()
    assert (
        len(mutations) == 1
        and mutations[0]["spec"]["service"]["build"]["image"] == p["old_image"]
    ), mutations
    assert (
        mutations[0]["environmentId"] == p["environment"]
        and mutations[0]["regions"] == ["us-ca-1"]
        and mutations[0]["trigger"] == "DEPLOYMENT_TRIGGER_ROLLBACK"
        and mutations[0]["sourceDeploymentId"] == p["previous"]
    ), mutations
    page.goto(state["ui_url"] + path + "&tab=settings")
    banner = page.get_by_role("status").filter(
        has_text=f"These settings are from deployment {p['current'][-8:]}, which did not finish in us-ca-1."
    )
    expect(banner).to_contain_text(f"v1 ({p['previous'][-8:]}) still serves.")
    expect(banner).to_contain_text("image:")
    expect(banner).to_contain_text("health:")
    mutations.clear()
    banner.get_by_role("button", name="Roll back to v1", exact=True).click()
    dialog.get_by_role("button", name="Roll back", exact=True).click()
    expect(dialog).to_be_hidden()
    assert (
        len(mutations) == 1 and mutations[0]["sourceDeploymentId"] == p["previous"]
    ), mutations
    phase("canceled")
    sql(
        state,
        f"UPDATE deployments SET canceled_by_type='user',canceled_by_id='{owner['id']}' WHERE id='{p['current']}';",
    )
    page.reload()
    canceled = page.get_by_role("region", name="Canceled", exact=True)
    expect(canceled).to_be_visible()
    expect(canceled.get_by_text("Canceled by Olivia Owner", exact=True)).to_be_visible()
    sql(
        state,
        f"UPDATE deployments SET canceled_by_type=NULL,canceled_by_id=NULL WHERE id='{p['current']}';",
    )
    phase("running")
    page.reload()
    expect(page.get_by_role("heading", name="progress-ui", exact=True)).to_be_visible()
    panel = page.get_by_role("region", name="Running", exact=True)
    expect(panel).to_be_visible()
    phase("running", "out_of_memory")
    expect(panel.get_by_text("Memory limit exceeded", exact=True)).to_be_visible(
        timeout=25000
    )
    expect(panel.get_by_role("heading", name="Running", exact=True)).to_be_visible()
    expect(panel.get_by_role("link", name="View logs", exact=True)).to_have_count(1)
    assert not errors, errors
    context.close()

    phase("failed")
    context, page, errors = open_page(browser, "member", narrow=True, state=state)
    page.goto(state["ui_url"] + path)
    panel = page.get_by_role("region", name="Failed", exact=True)
    expect(
        panel.get_by_role("button", name="Roll back to v1", exact=True)
    ).to_be_disabled()
    expect(panel.get_by_role("link", name="View logs", exact=True)).to_be_visible()
    assert panel.bounding_box()["width"] <= page.viewport_size["width"]
    meta = panel.locator("[data-meta-line]")
    assert len(line_starts(meta)) > 1, "the deployment meta line fits on one line"
    assert "·" not in line_starts(meta), line_starts(meta)
    assert not errors, errors
    context.close()

    phase("deploying")
    context, page, errors = open_page(browser, state=state, reduced_motion="reduce")
    page.goto(state["ui_url"] + path)
    panel = page.get_by_role("region", name="Deploying", exact=True)
    expect(
        panel.get_by_role("region", name="New version", exact=True).get_by_text(
            "Starting", exact=True
        )
    ).to_be_visible()
    assert (
        panel.evaluate(
            "node => node.getAnimations({subtree:true}).filter(animation => animation.effect.getTiming().iterations === Infinity).length"
        )
        == 0
    )
    sql(
        state,
        f"UPDATE deployment_placements SET started_at=NOW() WHERE deployment_id='{p['current']}';",
    )
    page.reload()
    restarting = page.get_by_role("region", name="Deploying", exact=True)
    expect(restarting).to_be_visible()
    expect(restarting.get_by_role("button", name="Cancel", exact=True)).to_have_count(0)
    sql(
        state,
        f"UPDATE deployment_placements SET started_at=NULL WHERE deployment_id='{p['current']}';",
    )
    page.reload()
    panel = page.get_by_role("region", name="Deploying", exact=True)
    cancels = []

    def cancel(route):
        cancels.append(route.request.post_data_json)
        route.fulfill(json={})

    context.route("**/loco.deployment.v1.DeploymentService/CancelDeployment", cancel)
    panel.get_by_role("button", name="Cancel", exact=True).click()
    dialog = page.get_by_role("alertdialog")
    expect(dialog).to_contain_text("go back to the version that served them before")
    dialog.get_by_role("button", name="Keep deployment", exact=True).click()
    expect(dialog).to_be_hidden()
    assert cancels == [], cancels
    panel.get_by_role("button", name="Cancel", exact=True).click()
    page.get_by_role("alertdialog").get_by_role(
        "button", name="Cancel deployment", exact=True
    ).click()
    expect(page.get_by_role("alertdialog")).to_be_hidden()
    assert cancels == [{"deploymentId": p["current"]}], cancels
    assert not errors, errors
    context.close()

    phase("running")
    sql(
        state,
        f"UPDATE deployments SET started_at=NULL WHERE id='{p['current']}';"
        f"UPDATE deployment_placements SET started_at=NULL WHERE deployment_id='{p['current']}';",
    )
    context, page, errors = open_page(browser, state=state)
    page.goto(state["ui_url"] + path)
    panel = page.get_by_role("region", name="Running", exact=True)
    expect(panel.get_by_role("button", name="Cancel", exact=True)).to_be_visible()
    expect(panel.get_by_role("button", name="Dismiss", exact=True)).to_have_count(0)
    sql(
        state,
        f"UPDATE deployments SET started_at=NOW() WHERE id='{p['current']}';"
        f"UPDATE deployment_placements SET started_at=NOW() WHERE deployment_id='{p['current']}';",
    )
    page.reload()
    panel = page.get_by_role("region", name="Running", exact=True)
    expect(panel.get_by_role("button", name="Dismiss", exact=True)).to_be_visible()
    expect(panel.get_by_role("button", name="Cancel", exact=True)).to_have_count(0)
    assert not errors, errors
    context.close()

    phase("running")
    placement = sql(
        state, f"SELECT id FROM placements WHERE resource_id='{p['resource']}';"
    )

    def pod(name, revision, state_name, ready=False, deployment=p["current"]):
        return {
            "uid": name,
            "name": name,
            "node": "worker-east-5",
            "placement_revision": revision,
            "deployment_id": deployment,
            "ready": ready,
            "state": state_name,
        }

    restart = {
        "placementId": placement,
        "replicas": [
            pod("progress-restart-1", 3, "Starting"),
            pod("progress-new-1", 2, "Serving", True),
            pod("progress-new-2", 2, "Serving", True),
            pod("progress-old-1", 1, "Stopped", deployment=p["previous"]),
        ],
    }
    last_seq = int(
        sql(
            state,
            f"SELECT COALESCE(MAX(seq),0) FROM deployment_activity WHERE deployment_id='{p['current']}';",
        )
    )
    sql(
        state,
        f"""BEGIN;
    UPDATE placements SET desired_revision=3,observed_revision=3,status_updated_at=NOW() WHERE id='{placement}';
    UPDATE deployment_placements SET status='deploying',completed_at=NULL,updated_at=NOW() WHERE deployment_id='{p["current"]}';
    UPDATE deployments SET status='deploying',completed_at=NULL,updated_at=NOW() WHERE id='{p["current"]}';
    INSERT INTO deployment_rollouts(deployment_id,cluster_id,revision,snapshot)
    VALUES('{p["current"]}','{p["cluster"]}',3,{evidence.sql_literal(json.dumps(restart))});
    INSERT INTO deployment_activity(deployment_id,seq,revision,at,severity,replica_name,message,cluster_id,region) VALUES
    ('{p["current"]}',{last_seq + 1},3,NOW()-INTERVAL '3 seconds','info','progress-restart-1','Starting','{p["cluster"]}','us-ca-1'),
    ('{p["current"]}',{last_seq + 2},3,NOW()-INTERVAL '1 second','info','progress-new-1','Stopping','{p["cluster"]}','us-ca-1');
    COMMIT;""",
    )
    context, page, errors = open_page(browser, state=state)
    page.goto(state["ui_url"] + path)
    panel = page.get_by_role("region", name="Deploying", exact=True)
    new_version = panel.get_by_role("region", name="New version", exact=True)
    expect(new_version.get_by_role("listitem")).to_have_count(1)
    expect(new_version).to_contain_text("progress-restart-1")
    previous_version = panel.get_by_role("region", name="Previous version", exact=True)
    expect(previous_version.get_by_role("listitem")).to_have_count(2)
    expect(previous_version).not_to_contain_text("progress-old-1")
    activity = panel.get_by_role("region", name="Activity feed", exact=True)
    expect(activity.get_by_role("listitem")).to_have_count(2)
    expect(activity.get_by_role("listitem").first).to_contain_text(
        "+0s after the rollout started"
    )
    expect(activity.get_by_role("listitem").last).to_contain_text(
        "+2s after the rollout started"
    )
    expect(activity).not_to_contain_text("Deployment requested")
    expect(panel.get_by_title("Time since the restart started")).to_be_visible()
    expect(panel.get_by_role("list", name="Rollout steps", exact=True)).to_contain_text(
        "0 of 2 ready"
    )
    replicas = page.get_by_role("region", name="Replicas", exact=True)
    expect(replicas).to_contain_text("0 of 2 ready, 2 previous")
    assert not errors, errors
    context.close()

    # Canceling a rollout that already finished in one region offers to roll that region back.
    east = evidence.seed_cluster(state, "us-ca-1", name="cancel-east")
    west = evidence.seed_cluster(state, "us-va-1", name="cancel-west")
    sizing = {"cpu": "100m", "memory": "128Mi", "minReplicas": 1, "maxReplicas": 1}
    cancel_old = "registry.example.test/cancel:v1@sha256:" + "3" * 64
    cancel_new = "registry.example.test/cancel:v2@sha256:" + "4" * 64

    def cancel_spec(image):
        return {
            "platform": "linux/amd64",
            "observability": {"injectOtel": True},
            "build": {"type": "image", "image": image},
            "regions": {"us-ca-1": sizing, "us-va-1": sizing},
        }

    before, rollout = str(uuid.uuid4()), str(uuid.uuid4())
    both = {"us-ca-1": {"cluster": east}, "us-va-1": {"cluster": west}}
    cancel_resource = evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": "cancel-ui",
                "deployments": [
                    {
                        "environment": "production",
                        "placements": both,
                        "id": before,
                        "spec": cancel_spec(cancel_old),
                        "age": "1 hour",
                    },
                    {
                        "environment": "production",
                        "placements": both,
                        "id": rollout,
                        "spec": cancel_spec(cancel_new),
                        "status": "deploying",
                        "live": False,
                        "age": "1 minute",
                    },
                ],
            }
        ],
    )["acme/shared/cancel-ui"]
    sql(
        state,
        f"""BEGIN;
    UPDATE deployment_placements SET status='running',started_at=NOW()-INTERVAL '30 seconds' WHERE deployment_id='{rollout}' AND region='us-ca-1';
    UPDATE deployment_placements SET status='deploying',started_at=NULL WHERE deployment_id='{rollout}' AND region='us-va-1';
    UPDATE deployment_placements SET status='superseded',superseded_by='{rollout}',completed_at=NOW() WHERE deployment_id='{before}' AND region='us-ca-1';
    UPDATE placements SET deployment_id='{rollout}',desired_revision=2,observed_revision=2 WHERE resource_id='{cancel_resource}';
    COMMIT;""",
    )
    context, page, errors = open_page(browser, state=state)
    page.goto(
        state["ui_url"]
        + f"/org/{p['org']}/wks/{workspace}/resource/{cancel_resource}?env=production"
    )
    page.get_by_role("button", name="Cancel", exact=True).first.click()
    dialog = page.get_by_role("alertdialog")
    finished = dialog.get_by_role(
        "list", name="Regions that already run it", exact=True
    )
    expect(finished.get_by_role("listitem")).to_have_count(1)
    expect(finished).to_contain_text("us-ca-1")
    expect(finished).to_contain_text("previous v1")
    rollback_option = dialog.get_by_role(
        "checkbox", name="Also roll back us-ca-1 to v1", exact=True
    )
    expect(rollback_option).not_to_be_checked()
    rollback_option.click()
    dialog.get_by_role("button", name="Cancel deployment", exact=True).click()
    expect(dialog).to_be_hidden()
    queued = call(
        state,
        "loco.deployment.v1.DeploymentService/ListDeployments",
        {"resourceId": cancel_resource, "environmentId": p["environment"]},
    )["deployments"]
    rollbacks = [d for d in queued if d.get("trigger") == "DEPLOYMENT_TRIGGER_ROLLBACK"]
    assert len(rollbacks) == 1, queued
    assert rollbacks[0]["sourceDeploymentId"] == before, rollbacks
    assert [pl["region"] for pl in rollbacks[0]["placements"]] == ["us-ca-1"], rollbacks
    assert not errors, errors
    context.close()
    browser.close()
print(
    "Deployment progress phases, replica watches, logs, rollback, reduced motion, restarts and cancel rollback passed"
)
