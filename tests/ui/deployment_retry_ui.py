import argparse
import hashlib
import json
import re
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from harness import (
    call,
    evidence,
    expect_rpc,
    load_state,
    open_page,
    repo,
    session,
    sql,
)
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Retry a deployment after its real API reply is dropped"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/deployment-retry"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "deployment-retry", "tests/ui/fixtures/deployment-history/scenario.json"
)
workspace = state["workspaces"]["acme/shared"]
org = state["orgs"]["acme"]["id"]
evidence.seed_cluster(state, "us-ca-1", domains=["apps.e2e.test"], default=True)
staging = call(
    state,
    "loco.environment.v1.EnvironmentService/CreateEnvironment",
    {"workspaceId": workspace, "name": "retry-staging"},
)["environmentId"]

# A synthetic image registry serves an empty amd64 image. Image resolution and deployment RPCs
# both reach the actual API; only the first successful deployment reply is deliberately dropped.
config = json.dumps(
    {
        "architecture": "amd64",
        "os": "linux",
        "rootfs": {"type": "layers", "diff_ids": []},
    }
).encode()
manifest = json.dumps(
    {
        "schemaVersion": 2,
        "mediaType": "application/vnd.oci.image.manifest.v1+json",
        "config": {
            "mediaType": "application/vnd.oci.image.config.v1+json",
            "size": len(config),
            "digest": "sha256:" + hashlib.sha256(config).hexdigest(),
        },
        "layers": [],
    }
).encode()


class Registry(BaseHTTPRequestHandler):
    def do_GET(self):
        if "/manifests/" in self.path:
            body, mime = manifest, "application/vnd.oci.image.manifest.v1+json"
        elif "/blobs/" in self.path:
            body, mime = config, "application/vnd.oci.image.config.v1+json"
        else:
            body, mime = b"{}", "application/json"
        self.send_response(200)
        self.send_header("Content-Type", mime)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        pass


registry = ThreadingHTTPServer(("127.0.0.1", 0), Registry)
thread = threading.Thread(target=registry.serve_forever, daemon=True)
thread.start()
try:
    with session(out) as browser:
        for theme, changed in (
            ("light", ""),
            ("dark", ""),
            ("light", "port"),
            ("light", "targets"),
            ("light", "public"),
            ("light", "domain"),
        ):
            env_slug = "retry-staging" if changed == "public" else "production"
            context, page, errors = open_page(
                browser,
                state=state,
                color_scheme=theme,
                storage={evidence.MODE_KEY: theme},
            )
            name = "retry-" + theme + ("-" + changed if changed else "")
            label = theme + ("-" + changed if changed else "")
            page.goto(
                state["ui_url"]
                + f"/org/{org}/wks/{workspace}/resources?env={env_slug}&new=1"
            )
            ask = page.get_by_role("dialog", name="New resource", exact=True)
            ask.get_by_role("textbox", name="Container image", exact=True).fill(
                f"127.0.0.1:{registry.server_port}/retry:latest"
            )
            ask.get_by_role("textbox", name="Name", exact=True).fill(name)
            ask.get_by_role("button", name="Continue", exact=True).click()
            drawer = page.get_by_role("dialog", name=name, exact=True)
            drawer.get_by_role("textbox", name="Port", exact=True).fill("8080")
            if changed == "public":
                drawer.get_by_role("button", name="Internal", exact=True).click()
            if changed != "public":
                expect(
                    drawer.get_by_text(f"{name}.apps.e2e.test is available", exact=True)
                ).to_be_visible()
            expect(drawer.get_by_text("Checking the image…", exact=True)).to_have_count(
                0
            )
            requests, replies = [], []

            def lost_reply(route, _request, requests=requests, replies=replies):
                requests.append(route.request.post_data_json)
                response = route.fetch()
                assert response.status == 200, response.text()
                replies.append(response.json())
                if len(requests) == 1:
                    route.abort("connectionfailed")
                else:
                    route.fulfill(response=response)

            page.route(
                "**/loco.deployment.v1.DeploymentService/CreateDeployment", lost_reply
            )
            deploy = drawer.get_by_role("button", name="Deploy " + name, exact=True)
            with expect_rpc(
                page,
                "ListWorkspaceResources",
                lambda body: body.get("workspaceId") == workspace,
            ) as refreshed:
                deploy.click()
            assert any(
                r["id"] == requests[0]["resourceId"]
                for r in refreshed.value.json()["resources"]
            )
            expect(drawer.get_by_role("alert")).to_be_visible()
            expect(deploy).to_be_enabled()
            assert (
                sql(
                    state,
                    f"SELECT count(*) FROM resources WHERE workspace_id='{workspace}' AND slug='{name}';",
                )
                == "1"
            )
            assert (
                sql(
                    state,
                    f"SELECT count(*) FROM deployments d JOIN resources r ON r.id=d.resource_id WHERE r.slug='{name}';",
                )
                == "1"
            )
            # Allow the normal resource/domain invalidations to finish. This draft owns its name
            # and address; they must remain usable for the recovery attempt.
            if changed != "public":
                expect(
                    drawer.get_by_text(f"{name}.apps.e2e.test is available", exact=True)
                ).to_be_visible()
            page.screenshot(path=out / f"{label}-reply-lost.png")
            original_draft = page.url
            drawer.get_by_role("button", name="Close", exact=True).click()
            page.goto(
                state["ui_url"]
                + f"/org/{org}/wks/{workspace}/resources?env={env_slug}&new=1"
            )
            other = page.get_by_role("dialog", name="New resource", exact=True)
            other.get_by_role("textbox", name="Container image", exact=True).fill(
                f"127.0.0.1:{registry.server_port}/retry:latest"
            )
            other.get_by_role("textbox", name="Name", exact=True).fill(name + "-other")
            other.get_by_role("button", name="Continue", exact=True).click()
            expect(
                page.get_by_role("dialog", name=name + "-other", exact=True)
            ).to_be_visible()
            page.goto(original_draft)
            page.reload()
            drawer = page.get_by_role("dialog", name=name, exact=True)
            deploy = drawer.get_by_role("button", name="Deploy " + name, exact=True)
            if changed != "public":
                expect(
                    drawer.get_by_text(f"{name}.apps.e2e.test is available", exact=True)
                ).to_be_visible()
            expect(deploy).to_be_enabled()
            if changed == "port":
                drawer.get_by_role("textbox", name="Port", exact=True).fill("9090")
            elif changed == "targets":
                drawer.get_by_role(
                    "button", name=re.compile(r"^Size per replica\b")
                ).click()
                drawer.get_by_role("slider", name="CPU", exact=True).focus()
                page.keyboard.press("Home")
                page.keyboard.press("ArrowRight")
                drawer.get_by_role("button", name=re.compile(r"^Scaling\b")).click()
                drawer.get_by_role(
                    "button", name="Increase replicas", exact=True
                ).click()
                drawer.get_by_role("switch", name="Autoscaling", exact=True).click()
            elif changed == "public":
                drawer.get_by_role("button", name="Public", exact=True).click()
                expect(
                    drawer.get_by_text(f"{name}.apps.e2e.test is available", exact=True)
                ).to_be_visible()
            elif changed == "domain":
                drawer.get_by_role("textbox", name="Subdomain", exact=True).fill(
                    name + "-new"
                )
                expect(
                    drawer.get_by_text(
                        f"{name}-new.apps.e2e.test is available", exact=True
                    )
                ).to_be_visible()
            with expect_rpc(
                page,
                "CreateDeployment",
                lambda body, resource_id=requests[0]["resourceId"]: (
                    body.get("resourceId") == resource_id
                ),
            ):
                deploy.click()
            expect(page).to_have_url(
                re.compile(r"/resource/[0-9a-f-]+\?env=" + env_slug + "$")
            )
            assert len(requests) == 2, requests
            expected_rows = 2 if changed else 1
            if changed:
                assert requests[0]["resourceId"] == requests[1]["resourceId"], requests
                assert requests[0]["requestId"] != requests[1]["requestId"], requests
                if changed == "port":
                    assert requests[1]["spec"]["service"]["port"] == 9090, requests
                if changed == "targets":
                    persisted = json.loads(
                        sql(
                            state,
                            f"SELECT spec->'regions'->'us-ca-1' FROM resources WHERE id='{requests[0]['resourceId']}';",
                        )
                    )
                    assert persisted["cpu"] == "250m", persisted
                    assert persisted["minReplicas"] == 2, persisted
                    assert persisted["maxReplicas"] == 3, persisted
                    assert persisted["scalers"] == {"enabled": True, "cpuTarget": 70}, (
                        persisted
                    )
                    assert requests[1]["updateResourceTargets"] is True
                    assert (
                        requests[0]["resourceRevision"]
                        != requests[1]["resourceRevision"]
                    )
                if changed == "public":
                    assert requests[1]["environmentId"] == staging
                    assert (
                        sql(
                            state,
                            f"SELECT environment_id FROM resource_domains WHERE resource_id='{requests[0]['resourceId']}';",
                        )
                        == staging
                    )
                if changed == "domain":
                    assert (
                        sql(
                            state,
                            f"SELECT domain FROM resource_domains WHERE resource_id='{requests[0]['resourceId']}';",
                        )
                        == name + "-new.apps.e2e.test"
                    )
                assert replies[0]["deploymentId"] != replies[1]["deploymentId"], replies
            else:
                assert requests[0] == requests[1], requests
                assert requests[0]["requestId"], requests
                assert replies[0] == replies[1], replies
            assert (
                sql(
                    state,
                    f"SELECT count(*) FROM resources WHERE workspace_id='{workspace}' AND slug='{name}';",
                )
                == "1"
            )
            assert sql(
                state,
                f"SELECT count(*) FROM deployments WHERE resource_id='{requests[0]['resourceId']}';",
            ) == str(expected_rows)
            page.screenshot(path=out / f"{label}-retry-recovered.png")
            (out / f"{label}-requests.json").write_text(
                json.dumps(
                    {
                        "requests": requests,
                        "replies": replies,
                        "deployment_rows": expected_rows,
                    },
                    indent=2,
                )
            )
            if changed == "port":
                page.unroute("**/loco.deployment.v1.DeploymentService/CreateDeployment")
                # A and B are real deployments from the preceding recovery case. C makes both
                # historical targets; changing rollback intent must retire A's lost-reply ID.
                third = dict(requests[1], requestId=str(uuid.uuid4()))
                third["spec"] = json.loads(json.dumps(third["spec"]))
                third["spec"]["service"]["port"] = 7070
                call(
                    state,
                    "loco.deployment.v1.DeploymentService/CreateDeployment",
                    third,
                )
                page.reload()
                rollback_requests, rollback_replies = [], []

                def rollback_reply(
                    route, _request, sent=rollback_requests, replies=rollback_replies
                ):
                    sent.append(route.request.post_data_json)
                    response = route.fetch()
                    assert response.status == 200, response.text()
                    replies.append(response.json())
                    if len(sent) == 1:
                        route.abort("connectionfailed")
                    else:
                        route.fulfill(response=response)

                page.route(
                    "**/loco.deployment.v1.DeploymentService/CreateDeployment",
                    rollback_reply,
                )
                for source in (
                    replies[0]["deploymentId"],
                    replies[1]["deploymentId"],
                    replies[0]["deploymentId"],
                ):
                    page.get_by_role(
                        "button", name="Actions for " + source[-8:], exact=True
                    ).click()
                    page.get_by_role(
                        "menuitem", name="Roll back to this", exact=True
                    ).click()
                    dialog = page.get_by_role(
                        "dialog", name="Roll back " + name, exact=True
                    )
                    dialog.get_by_role("button", name="Roll back", exact=True).click()
                    expect(dialog).to_have_count(0)
                assert len(rollback_requests) == 3, rollback_requests
                assert (
                    rollback_requests[0]["sourceDeploymentId"]
                    == rollback_requests[2]["sourceDeploymentId"]
                )
                assert len({r["requestId"] for r in rollback_requests}) == 3, (
                    rollback_requests
                )
                assert len({r["deploymentId"] for r in rollback_replies}) == 3, (
                    rollback_replies
                )
                assert (
                    sql(
                        state,
                        f"SELECT count(*) FROM deployments WHERE resource_id='{requests[0]['resourceId']}';",
                    )
                    == "6"
                )
                (out / "rollback-intents.json").write_text(
                    json.dumps(
                        {"requests": rollback_requests, "replies": rollback_replies},
                        indent=2,
                    )
                )
            assert not errors, errors
            context.close()
finally:
    registry.shutdown()
    thread.join()
    registry.server_close()
print(
    "PASS: real deployment reply dropped, same draft retried with identical request, one deployment retained in light/dark; synthetic registry, real API/database",
    flush=True,
)
