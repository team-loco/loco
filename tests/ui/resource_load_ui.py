import argparse
import collections
import os
import re
import uuid
from pathlib import Path

from harness import evidence, expect_rpc, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check the requests of a cold resource page load"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/resource-load"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "resource-load", "tests/ui/fixtures/deployment-history/scenario.json"
)
workspace = state["workspaces"]["acme/shared"]
org = state["orgs"]["acme"]["id"]
name = "load-" + uuid.uuid4().hex[:8]
resource = evidence.seed_resources(
    state,
    [
        {
            "workspace": "acme/shared",
            "name": name,
            "deployments": [
                {
                    "environment": "production",
                    "age": "1 hour",
                    "placements": {"us-ca-1": {}},
                    "spec": {
                        "platform": "linux/amd64",
                        "observability": {"injectOtel": True},
                        "build": {
                            "type": "image",
                            "image": "registry.example.com/shop:latest",
                        },
                        "port": 8080,
                        "regions": {
                            "us-ca-1": {
                                "cpu": "500m",
                                "memory": "512Mi",
                                "minReplicas": 1,
                                "maxReplicas": 1,
                            }
                        },
                    },
                }
            ],
        }
    ],
)[f"acme/shared/{name}"]

RPC = re.compile(r"/loco\.\w+\.v1\.\w+/(\w+)(?:\?|$)")
ONCE = (
    "GetConfig",
    "GetScopes",
    "ListUserOrgs",
    "ListOrgWorkspaces",
    "ListEnvironments",
    "GetOrg",
    "GetResource",
    "ListWorkspaceResources",
    "GetDeployment",
)
HELD = (
    "observability.v1.ObservabilityService/QueryMetrics",
    "event.v1.EventService/ListEvents",
)
url = state["ui_url"] + f"/org/{org}/wks/{workspace}/resource/{resource}?env=production"
with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    calls = []
    methods = {}

    def record(request):
        rpc = RPC.search(request.url)
        if rpc:
            calls.append((rpc.group(1), request.post_data or ""))
            methods[rpc.group(1)] = request.method

    held = []
    page.on("request", record)
    for method in HELD:
        context.route(f"**/loco.{method}", lambda route: held.append(route))
    # The shell lists the workspace's resources before the page's code arrives,
    # and environments load after the page has mounted.
    environments = []
    context.route(
        "**/loco.environment.v1.EnvironmentService/ListEnvironments",
        lambda route: environments.append(route),
        times=1,
    )
    page_code = []
    context.route(
        re.compile(r"/assets/Resource-[^/]+\.js$"),
        lambda route: page_code.append(route),
        times=1,
    )
    with expect_rpc(
        page,
        "loco.resource.v1.ResourceService/ListWorkspaceResources",
        lambda body: True,
    ):
        page.goto(url)
    assert page_code, "the resource page's code loaded before the resource list"
    page_code[0].continue_()
    expect(
        page.get_by_role("status").filter(has_text="Loading resource")
    ).to_be_attached()
    assert environments, "the workspace's environments loaded before the page did"
    environments[0].continue_()
    replicas = page.get_by_role("region", name="Replicas", exact=True)
    expect(replicas).to_be_visible()
    expect(replicas.locator("[data-slot=skeleton]").first).to_be_visible()
    expect(replicas.get_by_text("—", exact=True)).to_have_count(0)
    for route in held:
        route.continue_()
    context.unroute_all(behavior="wait")
    page.wait_for_timeout(3000)
    counts = collections.Counter(calls)
    repeated = {call[0]: n for call, n in counts.items() if n > 1}
    assert not repeated, repeated
    assert collections.Counter(c[0] for c in calls)["WatchDeployment"] == 1, calls
    for method in ONCE:
        assert any(c[0] == method for c in calls), (method, calls)
    assert methods["GetConfig"] == "GET", methods
    assert {m for name, m in methods.items() if name != "GetConfig"} == {"POST"}, (
        methods
    )
    assert not errors, errors
    context.close()

    # The workspace URL names its environment while the resource list still loads.
    context, page, _ = open_page(browser, state=state)
    listing = []
    context.route(
        "**/loco.resource.v1.ResourceService/ListWorkspaceResources",
        lambda route: listing.append(route),
    )
    page.goto(state["ui_url"] + f"/org/{org}/wks/{workspace}")
    expect(page).to_have_url(re.compile(r"[?&]env=production(&|$)"))
    assert listing, "the resource list loaded before the check"
    context.close()
    # A resource that fails to load keeps its header from the workspace's resource list.
    context, page, _ = open_page(browser, state=state)
    context.route(
        "**/loco.resource.v1.ResourceService/GetResource",
        lambda route: route.fulfill(
            status=503, json={"code": "unavailable", "message": "fixture outage"}
        ),
    )
    page.goto(url)
    expect(
        page.get_by_role("heading", name="Couldn’t load this resource", exact=True)
    ).to_be_visible()
    expect(page.get_by_role("heading", level=1)).to_have_text(name)
    context.close()
    shots = Path(os.environ.get("LOAD_SHOTS", out))
    shots.mkdir(parents=True, exist_ok=True)
    for mode in ("light", "dark"):
        context, page, _ = open_page(
            browser, state=state, storage={evidence.MODE_KEY: mode}
        )
        for method in HELD:
            context.route(f"**/loco.{method}", lambda route: None)
        page.goto(url)
        replicas = page.get_by_role("region", name="Replicas", exact=True)
        expect(replicas.locator("[data-slot=skeleton]").first).to_be_visible()
        page.wait_for_timeout(1000)
        page.screenshot(path=shots / f"loading-{mode}.png")
        context.close()
print("PASS: cold resource page load", flush=True)
