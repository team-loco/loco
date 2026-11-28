import argparse
import hashlib
import json
import re
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from harness import evidence, expect_rpc, load_state, open_page, repo, session, sql
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
        for theme, changed in (("light", False), ("dark", False), ("light", True)):
            context, page, errors = open_page(
                browser,
                state=state,
                color_scheme=theme,
                storage={evidence.MODE_KEY: theme},
            )
            name = "retry-" + theme + ("-edited" if changed else "")
            label = theme + ("-edited" if changed else "")
            page.goto(
                state["ui_url"]
                + f"/org/{org}/wks/{workspace}/resources?env=production&new=1"
            )
            ask = page.get_by_role("dialog", name="New resource", exact=True)
            ask.get_by_role("textbox", name="Container image", exact=True).fill(
                f"127.0.0.1:{registry.server_port}/retry:latest"
            )
            ask.get_by_role("textbox", name="Name", exact=True).fill(name)
            ask.get_by_role("button", name="Continue", exact=True).click()
            drawer = page.get_by_role("dialog", name=name, exact=True)
            drawer.get_by_role("textbox", name="Port", exact=True).fill("8080")
            expect(
                drawer.get_by_text(f"{name}.apps.e2e.test is available", exact=True)
            ).to_be_visible()
            expect(drawer.get_by_text("Checking the image…", exact=True)).to_have_count(
                0
            )
            requests, replies = [], []

            def lost_reply(route, requests=requests, replies=replies):
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
            expect(
                drawer.get_by_text(f"{name}.apps.e2e.test is available", exact=True)
            ).to_be_visible()
            page.screenshot(path=out / f"{label}-reply-lost.png")
            if changed:
                drawer.get_by_role("textbox", name="Port", exact=True).fill("9090")
            deploy.click()
            expect(page).to_have_url(
                re.compile(r"/resource/[0-9a-f-]+\?env=production$")
            )
            assert len(requests) == 2, requests
            expected_rows = 2 if changed else 1
            if changed:
                assert requests[0]["resourceId"] == requests[1]["resourceId"], requests
                assert requests[0]["requestId"] != requests[1]["requestId"], requests
                assert requests[1]["spec"]["service"]["port"] == 9090, requests
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
