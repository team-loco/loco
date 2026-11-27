import argparse
import re
import time
from urllib.parse import parse_qs, urlsplit

from capacity_fixture import install_capacity, install_inventory
from harness import copied_text, expect_rpc, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check the System area: who sees it, the cluster pages and their actions"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "system", "tests/ui/fixtures/system/scenario.json")
ui = state["ui_url"]

SCOPES = "**/loco.token.v1.TokenService/GetScopes"
USER_ORGS = "**/loco.org.v1.OrgService/ListUserOrgs"
SHELL_WATCH_JS = """
window.__systemChrome = false;
new MutationObserver(() => {
  if (document.querySelector('[data-slot="system-tag"], a[href="/system/clusters"]') !== null) window.__systemChrome = true;
}).observe(document, { childList: true, subtree: true });
"""


def system_page(*args, **kwargs):
    context, page, errors = open_page(*args, **kwargs)
    install_capacity(context, state)
    return context, page, errors


def hold(context, pattern):
    """Keeps the requests that match `pattern` pending until the test releases them."""
    held = []
    context.route(pattern, lambda route: held.append(route))
    return held


def fleet_calls(requests):
    return [url for url in requests if "ClusterAdminService" in url]


def pick_dialog(page):
    return page.get_by_role("alertdialog")


def open_menu(page):
    page.locator('[data-slot="account-button"]').click()


def row(page, name):
    return page.get_by_role("row", name=f"Open {name}", exact=True)


def row_menu(page, name, item):
    page.get_by_role("button", name=f"Actions for {name}", exact=True).click()
    page.get_by_role("menuitem", name=item, exact=True).click()


with session(repo / "tests/ui/out/system") as browser:
    # Delayed fleet responses keep the sticky action column at its loaded width.
    for color in ("loco", "paper"):
        context, page, errors = system_page(
            browser,
            user="admin",
            narrow=True,
            state=state,
            storage={"loco:color:v1": color},
        )
        pattern = "**/loco.cluster.v1.ClusterAdminService/ListClusters"
        held = hold(context, pattern)
        page.goto(ui + "/system/clusters")
        table = page.get_by_role("table", name="Clusters", exact=True)
        actions = table.get_by_role("columnheader", name="Actions", exact=True)
        expect(actions).to_be_visible()
        expect(table.locator("[data-slot=skeleton]").first).to_be_visible()
        loading_box = actions.bounding_box()
        for request in held:
            request.continue_()
        context.unroute(pattern)
        expect(row(page, "us-ca-1")).to_be_visible()
        loaded_box = actions.bounding_box()
        assert abs(loaded_box["width"] - loading_box["width"]) <= 1, (
            loading_box,
            loaded_box,
        )
        assert actions.evaluate("el => getComputedStyle(el).boxShadow") == "none"
        button = page.get_by_role("button", name="Actions for us-ca-1", exact=True)
        assert button.evaluate(
            "el => { const b=el.getBoundingClientRect(); return b.left >= 0 && b.right <= innerWidth; }"
        )
        assert not errors, errors
        context.close()

    # A user without a system role never sees the switch.
    context, page, errors = system_page(browser, user="member", state=state)
    page.goto(ui + "/dashboard")
    expect(page.get_by_title("Switch organization", exact=True)).to_be_visible()
    open_menu(page)
    expect(
        page.get_by_role("menuitem", name="Organizations", exact=True)
    ).to_be_visible()
    expect(
        page.get_by_role("menuitemcheckbox", name="System", exact=True)
    ).to_have_count(0)
    context.close()

    # Without a system role, /system is not found however the account's requests are timed:
    # no System page mounts and no fleet call goes out while the scopes or the organization
    # list are pending.
    for delayed in (SCOPES, USER_ORGS):
        for path in (
            "/system",
            "/system/clusters",
            "/System/Clusters",
            "/system/clusters/new",
        ):
            context, page, errors = system_page(browser, user="member", state=state)
            context.add_init_script(SHELL_WATCH_JS)
            requests = []
            page.on("request", lambda request, seen=requests: seen.append(request.url))
            held = hold(context, delayed)
            page.goto(ui + path, wait_until="commit")
            page.wait_for_timeout(500)
            assert not page.evaluate("window.__systemChrome"), (delayed, path)
            not_found = page.get_by_role("heading", name="Page not found", exact=True)
            if delayed == USER_ORGS:
                expect(not_found).to_be_visible()
            for route in held:
                route.continue_()
            expect(not_found).to_be_visible()
            assert not page.evaluate("window.__systemChrome"), (delayed, path)
            assert not fleet_calls(requests), (delayed, path, requests)
            context.close()

    # A system admin switches in and out, and sees the clusters with their actions.
    context, page, errors = system_page(browser, user="admin", state=state)
    page.goto(ui + "/dashboard")
    # Capture the settled dashboard route, after its workspace redirect.
    expect(page).to_have_url(re.compile(r"/org/[^/]+/wks/[^/?]+(?:\?|$)"))
    open_menu(page)
    switch = page.get_by_role("menuitemcheckbox", name="System", exact=True)
    expect(switch).to_have_attribute("aria-checked", "false")
    origin = re.compile(re.escape(page.url.split("?")[0]) + r"(?:\?|$)")
    thumb = switch.locator("[data-slot=switch-thumb]")
    thumb.evaluate("""el => el.addEventListener('transitionrun', () => {
      el.getAnimations().forEach(animation => animation.pause());
      window.systemSwitchPaused = true;
    }, {once: true})""")
    switch.click()
    page.wait_for_function("() => window.systemSwitchPaused === true")
    expect(switch).to_have_attribute("aria-checked", "true")
    expect(switch).to_be_visible()
    expect(page).to_have_url(origin)
    thumb.evaluate("el => el.getAnimations().forEach(animation => animation.finish())")
    expect(page).to_have_url(ui + "/system")
    expect(page.get_by_role("heading", name="Overview", exact=True)).to_be_visible()
    expect(page.locator('[data-slot="system-tag"]')).to_have_text("System")
    sidebar = page.locator('[data-slot="sidebar"]')
    expect(sidebar.get_by_role("link", name="Clusters", exact=True)).to_be_visible()
    expect(sidebar.get_by_role("link", name="Resources", exact=True)).to_have_count(0)
    # Problems come first: the unhealthy and unreporting regions before the healthy one.
    regions = [
        text
        for text in page.locator("section h2").all_inner_texts()
        if re.fullmatch(r"[a-z]{2}-[a-z0-9]+-[0-9]+", text)
    ]
    assert regions[0] in ("de-he-1", "us-ca-1"), regions
    assert regions.index("gb-lnd-1") > regions.index("de-he-1"), regions
    assert "jp-13-1" not in regions, regions
    expect(page.get_by_role("row", name="Open de-he-1-b", exact=True)).to_contain_text(
        "Never reported"
    )
    expect(page.get_by_role("row", name="Open de-he-1", exact=True)).to_contain_text(
        "Not reporting"
    )
    expect(
        page.get_by_role("row", name="Open us-ca-1-builds", exact=True)
    ).to_contain_text("Degraded")

    # Switching System off returns to the page the user came from, in any organization the
    # scopes reach, listed or not.
    for org, workspace in (
        ("ops", "ops/main"),
        ("acme", "acme/shared"),
        ("labs", "labs/bench"),
    ):
        origin = f"/org/{state['orgs'][org]['id']}/wks/{state['workspaces'][workspace]}/resources"
        page.goto(ui + origin)
        open_menu(page)
        page.get_by_role("menuitemcheckbox", name="System", exact=True).click()
        expect(page).to_have_url(ui + "/system")
        open_menu(page)
        switch = page.get_by_role("menuitemcheckbox", name="System", exact=True)
        expect(switch).to_have_attribute("aria-checked", "true")
        switch.click()
        expect(page).to_have_url(re.compile("^" + re.escape(ui + origin)))
        expect(page.locator('[data-slot="system-tag"]')).to_have_count(0)

    page.goto(ui + "/system/clusters")
    expect(page.get_by_role("heading", name="Clusters", exact=True)).to_be_visible()
    table = page.get_by_role("table", name="Clusters", exact=True)
    expect(table.get_by_role("row", name="Open us-ca-1", exact=True)).to_contain_text(
        "Healthy"
    )
    expect(table.get_by_role("row", name="Open gb-lnd-1", exact=True)).to_contain_text(
        "Paused"
    )
    expect(table.get_by_role("row", name="Open jp-13-1", exact=True)).to_have_count(0)
    expect(table.get_by_text("de-he-1", exact=True).first).to_be_visible()
    page.get_by_role("textbox", name="Filter clusters by name", exact=True).fill(
        "builds"
    )
    expect(page).to_have_url(re.compile(r"[?&]q=builds"))
    expect(
        table.get_by_role("row", name="Open us-ca-1-builds", exact=True)
    ).to_be_visible()
    expect(table.get_by_role("row", name="Open us-ca-1", exact=True)).to_have_count(0)
    page.get_by_role("textbox", name="Filter clusters by name", exact=True).fill("")
    page.get_by_role("button", name="Region", exact=True).click()
    page.get_by_role("option", name=re.compile("^de-he-1")).click()
    expect(page).to_have_url(re.compile(r"[?&]region=de-he-1"))
    expect(table.get_by_role("row", name="Open de-he-1", exact=True)).to_be_visible()
    expect(table.get_by_role("row", name="Open de-he-1-b", exact=True)).to_be_visible()
    expect(table.get_by_role("row", name="Open us-ca-1", exact=True)).to_have_count(0)
    page.get_by_role("button", name="Clear region filter", exact=True).click()
    expect(page).not_to_have_url(re.compile(r"[?&]region="))
    expect(table.get_by_role("row", name="Open us-ca-1", exact=True)).to_be_visible()
    page.get_by_role("button", name="Removed", exact=True).click()
    expect(page).to_have_url(re.compile(r"[?&]removed=true"))
    expect(table.get_by_role("row", name="Open jp-13-1", exact=True)).to_be_visible()
    page.get_by_role("button", name="Actions for jp-13-1", exact=True).click()
    expect(page.get_by_role("menuitem")).to_have_text(["Restore"])
    page.keyboard.press("Escape")
    page.get_by_role("button", name="Removed", exact=True).click()

    # Pause asks first, then the cluster shows Paused and offers Resume.
    page.get_by_role("button", name="Actions for us-ca-1", exact=True).click()
    expect(page.get_by_role("menuitem")).to_have_text(["Pause", "Issue join token"])
    page.get_by_role("menuitem", name="Pause", exact=True).click()
    dialog = page.get_by_role("alertdialog")
    expect(dialog).to_contain_text("takes no new placements")
    dialog.get_by_role("button", name="Cancel", exact=True).click()
    expect(table.get_by_role("row", name="Open us-ca-1", exact=True)).to_contain_text(
        "Healthy"
    )
    row_menu(page, "us-ca-1", "Pause")
    with expect_rpc(
        page, "loco.cluster.v1.ClusterAdminService/PauseCluster", lambda body: True
    ):
        dialog.get_by_role("button", name="Pause cluster", exact=True).click()
    expect(table.get_by_role("row", name="Open us-ca-1", exact=True)).to_contain_text(
        "Paused"
    )
    page.get_by_role("button", name="Actions for us-ca-1", exact=True).click()
    expect(page.get_by_role("menuitem")).to_have_text(
        ["Resume", "Issue join token", "Remove"]
    )
    page.get_by_role("menuitem", name="Resume", exact=True).click()
    with expect_rpc(
        page, "loco.cluster.v1.ClusterAdminService/ResumeCluster", lambda body: True
    ):
        dialog.get_by_role("button", name="Resume cluster", exact=True).click()
    expect(table.get_by_role("row", name="Open us-ca-1", exact=True)).to_contain_text(
        "Healthy"
    )

    # A paused cluster is removed, then restored: it leaves the list, shows under Removed and
    # comes back paused.
    row_menu(page, "gb-lnd-1", "Remove")
    with expect_rpc(
        page, "loco.cluster.v1.ClusterAdminService/RemoveCluster", lambda body: True
    ):
        dialog.get_by_role("button", name="Remove cluster", exact=True).click()
    expect(table.get_by_role("row", name="Open gb-lnd-1", exact=True)).to_have_count(0)
    page.get_by_role("button", name="Removed", exact=True).click()
    expect(table.get_by_role("row", name="Open gb-lnd-1", exact=True)).to_contain_text(
        "Removed"
    )
    row_menu(page, "gb-lnd-1", "Restore")
    with expect_rpc(
        page, "loco.cluster.v1.ClusterAdminService/RestoreCluster", lambda body: True
    ):
        dialog.get_by_role("button", name="Restore cluster", exact=True).click()
    expect(table.get_by_role("row", name="Open gb-lnd-1", exact=True)).to_have_count(0)
    page.get_by_role("button", name="Removed", exact=True).click()
    expect(table.get_by_role("row", name="Open gb-lnd-1", exact=True)).to_contain_text(
        "Paused"
    )

    # A join token is masked until revealed.
    row_menu(page, "us-ca-1", "Issue join token")
    dialog.get_by_role("button", name="Issue token", exact=True).click()
    token_dialog = page.get_by_role("dialog", name="Join token for us-ca-1", exact=True)
    expect(token_dialog.get_by_role("img", name="Hidden join token")).to_be_visible()
    expect(
        token_dialog.get_by_role(
            "img", name="Install command with the join token hidden"
        )
    ).to_be_visible()
    token_dialog.get_by_role("button", name="Show join token", exact=True).click()
    expect(token_dialog.get_by_role("img", name="Hidden join token")).to_have_count(0)
    token_dialog.get_by_role("button", name="Done", exact=True).click()

    # The detail page shows the facts and the same actions.
    row(page, "us-ca-1-builds").click()
    expect(page).to_have_url(re.compile(r"/system/clusters/[\w-]+$"))
    expect(
        page.get_by_role("heading", name=re.compile("^us-ca-1-builds "))
    ).to_be_visible()
    for label, value in (
        ("Region", "us-ca-1"),
        ("Role", "Builds"),
        ("Agent version", "v0.0.217"),
        ("Last heartbeat", "UTC"),
    ):
        expect(
            page.locator("dt", has_text=label).locator("xpath=following-sibling::dd")
        ).to_contain_text(value)
    expect(
        page.locator("dt", has_text="Key fingerprint").locator(
            "xpath=following-sibling::dd"
        )
    ).to_contain_text(re.compile("[0-9a-f]{16}…"))
    expect(page.get_by_role("button", name="Pause", exact=True)).to_be_visible()
    expect(
        page.get_by_role("button", name="Issue join token", exact=True)
    ).to_be_visible()
    expect(page.locator('[data-slot="cluster-capacity"]')).to_contain_text(
        "1887 vCPU unreserved"
    )
    expect(page.locator('[data-slot="cluster-capacity"]')).to_contain_text(
        "6113 vCPU requested / 8000 vCPU allocatable"
    )
    expect(
        page.get_by_text("The build controller is not ready.", exact=True)
    ).to_be_visible()
    expect(page.get_by_role("heading", name="Issues", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", name="Events", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", name="CPU usage", exact=True)).to_be_visible()
    sections = page.locator("main section[aria-labelledby]").evaluate_all(
        "elements => elements.map(el => el.querySelector('h2')?.firstChild?.textContent).filter(Boolean)"
    )
    assert sections == ["Details", "Issues", "Capacity", "Nodes", "Events"], sections
    expect(page.get_by_role("region", name="Issues", exact=True)).to_contain_text(
        "The build controller is not ready."
    )
    expect(
        page.get_by_role("region", name="Issues", exact=True).get_by_text(
            "No issues", exact=True
        )
    ).to_have_count(0)
    # A full UUID and its copy icon stay inside their fact, including at larger text sizes.
    cluster_id = page.url.rsplit("/", 1)[-1]
    id_fact = page.locator("dt", has_text="Cluster ID").locator(
        "xpath=following-sibling::dd"
    )
    copy_id = id_fact.get_by_role("button")
    for font in (16, 20):
        page.evaluate(
            "size => document.documentElement.style.fontSize = size + 'px'", font
        )
        for width in (1280, 1440, 390):
            page.set_viewport_size({"width": width, "height": 844})
            fact_box = id_fact.bounding_box()
            for element in (copy_id, copy_id.locator("svg")):
                box = element.bounding_box()
                assert (
                    box["x"] + box["width"] <= fact_box["x"] + fact_box["width"] + 1
                ), (font, width, box, fact_box)
            assert page.evaluate(
                "document.documentElement.scrollWidth <= window.innerWidth"
            )
    copy_id.click()
    assert copied_text(page) == cluster_id
    page.evaluate("document.documentElement.style.removeProperty('font-size')")
    page.set_viewport_size({"width": 1280, "height": 800})
    page.goto(ui + "/system/clusters/00000000-0000-0000-0000-000000000000")
    expect(
        page.get_by_role("heading", name="Page not found", exact=True)
    ).to_be_visible()

    # Add cluster creates a cluster in a new region and shows its token masked.
    page.goto(ui + "/system/clusters")
    page.get_by_role("link", name="Add cluster", exact=True).click()
    expect(page).to_have_url(ui + "/system/clusters/new")
    page.get_by_role("button", name="New region", exact=True).click()
    create = page.get_by_role("button", name="Create cluster", exact=True)
    expect(create).to_be_disabled()
    page.get_by_role("textbox", name="Country", exact=True).fill("nz")
    page.get_by_role("textbox", name="State or province", exact=True).fill("akl")
    with expect_rpc(
        page,
        "loco.cluster.v1.ClusterAdminService/CreateCluster",
        lambda body: body.get("newRegion") == {"country": "nz", "subdivision": "akl"},
    ) as created:
        create.click()
    assert created.value.ok
    expect(
        page.get_by_role("heading", name="Join nz-akl-1", exact=True)
    ).to_be_visible()
    expect(page.get_by_role("img", name="Hidden join token")).to_be_visible()
    page.get_by_role("button", name="Show join token", exact=True).click()
    token = page.locator("[data-secret-value]").inner_text()
    assert token != "" and "•" not in token
    page.get_by_role("button", name="Copy install command", exact=True).click()
    command = page.evaluate("window.__copied")
    assert f"--from-literal=join-token={token}" in command, command
    assert "--set global.region=nz-akl-1" in command, command
    page.get_by_role("link", name="Back to clusters", exact=True).click()
    expect(page.get_by_role("row", name="Open nz-akl-1", exact=True)).to_be_visible()
    # An existing region and the local region are registered by their codes.
    page.goto(ui + "/system/clusters/new")
    page.get_by_role("button", name="Choose a region", exact=True).click()
    page.get_by_role("option", name=re.compile("^us-ca-1")).click()
    page.get_by_role("textbox", name="Name", exact=True).fill("us-ca-1-c")
    with expect_rpc(
        page,
        "loco.cluster.v1.ClusterAdminService/CreateCluster",
        lambda body: (
            body.get("region") == "us-ca-1" and body.get("name") == "us-ca-1-c"
        ),
    ):
        page.get_by_role("button", name="Create cluster", exact=True).click()
    expect(
        page.get_by_role("heading", name="Join us-ca-1-c", exact=True)
    ).to_be_visible()
    page.goto(ui + "/system/clusters/new")
    page.get_by_role("button", name="Local", exact=True).click()
    with expect_rpc(
        page,
        "loco.cluster.v1.ClusterAdminService/CreateCluster",
        lambda body: body.get("region") == "local-1",
    ):
        page.get_by_role("button", name="Create cluster", exact=True).click()
    expect(page.get_by_role("heading", name="Join local-1", exact=True)).to_be_visible()
    assert not [e for e in errors if e], errors
    context.close()

    # A system reader sees the same pages with no change controls.
    context, page, errors = system_page(browser, user="reader", state=state)
    page.goto(ui + "/dashboard")
    open_menu(page)
    page.get_by_role("menuitemcheckbox", name="System", exact=True).click()
    expect(page.get_by_role("heading", name="Overview", exact=True)).to_be_visible()
    page.goto(ui + "/system/clusters")
    table = page.get_by_role("table", name="Clusters", exact=True)
    expect(table.get_by_role("row", name="Open us-ca-1", exact=True)).to_be_visible()
    expect(page.get_by_role("button", name=re.compile("^Actions for"))).to_have_count(0)
    expect(page.get_by_role("link", name="Add cluster", exact=True)).to_have_count(0)
    page.get_by_role("row", name="Open us-ca-1", exact=True).click()
    expect(page.get_by_role("heading", name=re.compile("^us-ca-1 "))).to_be_visible()
    for name in ("Pause", "Resume", "Remove", "Restore", "Issue join token"):
        expect(page.get_by_role("button", name=name, exact=True)).to_have_count(0)
    # Synthetic responses isolate UI retry/empty and navigation behavior; backend coverage uses real reports.
    operations = "**/loco.cluster.v1.ClusterAdminService/GetClusterOperations"
    metrics_rpc = "**/loco.observability.v1.ObservabilityService/QueryMetrics"
    context.route(metrics_rpc, lambda route: route.fulfill(json={}))
    context.route(
        operations,
        lambda route: route.fulfill(
            status=503,
            json={"code": "unavailable", "message": "Activity temporarily unavailable"},
        ),
    )
    page.goto(ui + "/system/clusters")
    row(page, "us-ca-1").click()
    expect(page.get_by_role("heading", name="Issues", exact=True)).to_be_visible()
    expect(
        page.get_by_role("region", name="Issues", exact=True).get_by_role(
            "button", name="Retry", exact=True
        )
    ).to_be_visible()
    context.unroute(operations)
    build_id = "b0000000-0000-4000-8000-000000000001"
    failure_at = int(time.time()) - 6 * 60 * 60
    context.route(
        operations,
        lambda route: route.fulfill(
            json={
                "workloads": [
                    {
                        "id": build_id,
                        "kind": "build",
                        "resourceName": "synthetic-build",
                        "updatedAt": time.strftime(
                            "%Y-%m-%dT%H:%M:%SZ", time.gmtime(failure_at)
                        ),
                        "status": "failed",
                        "message": "The build failed while installing dependencies.",
                        "orgId": state["orgs"]["ops"]["id"],
                        "workspaceId": state["workspaces"]["ops/main"],
                        "resourceId": "b0000000-0000-4000-8000-000000000002",
                        "environmentSlug": "production",
                    }
                ]
            }
        ),
    )
    page.get_by_role("region", name="Issues", exact=True).get_by_role(
        "button", name="Retry", exact=True
    ).click()
    expect(page.get_by_role("heading", name="Issues", exact=True)).to_be_visible()
    expect(
        page.get_by_text("The build failed while installing dependencies.", exact=True)
    ).to_be_visible()
    build_url = urlsplit(
        page.get_by_role("link", name="synthetic-build", exact=True).get_attribute(
            "href"
        )
    )
    assert build_url.path == (
        f"/org/{state['orgs']['ops']['id']}"
        f"/wks/{state['workspaces']['ops/main']}/observability"
    )
    assert parse_qs(build_url.query).get("view") == ["logs"]
    assert parse_qs(build_url.query).get("q") == ["@build:" + build_id]
    assert parse_qs(build_url.query).get("at") == [str(failure_at * 1000)]
    expect(page.get_by_text("No events", exact=True)).to_be_visible()
    expect(
        page.get_by_text(
            "No data",
            exact=True,
        )
    ).to_have_count(2)
    context.unroute(operations)
    context.route(operations, lambda route: route.fulfill(json={}))
    page.goto(ui + "/system/clusters")
    row(page, "de-he-1").click()
    expect(page.locator('[data-slot="cluster-capacity"]')).to_contain_text("Stale")
    page.goto(ui + "/system/clusters")
    row(page, "de-he-1-b").click()
    expect(
        page.get_by_role("region", name="Capacity", exact=True).get_by_text(
            "Not reported", exact=True
        )
    ).to_have_count(2)
    page.goto(ui + "/system/clusters/new")
    expect(
        page.get_by_role("heading", name="Page not found", exact=True)
    ).to_be_visible()
    context.close()


# Synthetic metric responses verify request scope and prevent old samples leaking across cluster switches.
with session(repo / "tests/ui/out/system-metric-scope") as browser:
    context, page, errors = system_page(browser, user="reader", state=state)
    seen_metrics = []
    held_metrics = []
    hold_metrics = False

    def metric_response(route):
        seen_metrics.append(route.request.post_data_json)
        if hold_metrics:
            held_metrics.append(route)
            return
        now = int(time.time())
        route.fulfill(
            json={
                "series": [
                    {
                        "points": [
                            {
                                "timestamp": time.strftime(
                                    "%Y-%m-%dT%H:%M:%SZ", time.gmtime(now - 120)
                                ),
                                "value": 1.25,
                            },
                            {
                                "timestamp": time.strftime(
                                    "%Y-%m-%dT%H:%M:%SZ", time.gmtime(now - 60)
                                ),
                                "value": 1.5,
                            },
                        ]
                    }
                ]
            }
        )

    context.route(
        "**/loco.observability.v1.ObservabilityService/QueryMetrics", metric_response
    )
    context.route(
        "**/loco.cluster.v1.ClusterAdminService/GetClusterOperations",
        lambda route: route.fulfill(json={}),
    )
    with expect_rpc(
        page, "loco.cluster.v1.ClusterAdminService/ListClusters", lambda body: True
    ) as fleet:
        page.goto(ui + "/system/clusters")
    clusters = {
        cluster["name"]: cluster["id"] for cluster in fleet.value.json()["clusters"]
    }
    row(page, "us-ca-1").click()
    signals = page.locator('[data-slot="node-runtime-signal"]')
    expect(signals.get_by_label(re.compile(r"^(CPU|Memory) over time$"))).to_have_count(
        2
    )

    def assert_metric_scope(cluster_id, node_name="node-0000"):
        assert {body["metricName"] for body in seen_metrics} == {
            "k8s.node.cpu.usage",
            "k8s.node.memory.working_set",
        }, seen_metrics
        for body in seen_metrics:
            assert body["installation"] is True, body
            assert body.get("workspaceId", "") == "", body
            assert body["attributes"] == {"cluster": cluster_id, "node": node_name}, (
                body
            )
            assert body["aggregation"] == "avg", body

    assert_metric_scope(clusters["us-ca-1"])
    # Changing nodes also replaces the observer and must not paint the old node's samples.
    seen_metrics.clear()
    hold_metrics = True

    def selected_node_metric(request, metric):
        body = request.post_data_json or {}
        return (
            request.url.endswith("/QueryMetrics")
            and body.get("attributes", {}).get("node") == "node-0001"
            and body.get("metricName") == metric
        )

    with page.expect_request(
        lambda request: selected_node_metric(request, "k8s.node.cpu.usage")
    ):
        with page.expect_request(
            lambda request: selected_node_metric(request, "k8s.node.memory.working_set")
        ):
            page.get_by_role("row", name="Inspect node-0001", exact=True).click()
    expect(signals.locator('[data-slot="skeleton"]')).to_have_count(2)
    expect(signals.get_by_label(re.compile(r"^(CPU|Memory) over time$"))).to_have_count(
        0
    )
    assert_metric_scope(clusters["us-ca-1"], "node-0001")
    for route in held_metrics:
        route.fulfill(json={})
    held_metrics.clear()
    seen_metrics.clear()
    hold_metrics = True
    next_path = "/system/clusters/" + clusters["de-he-1"]

    # This is a same-component client route transition, so the old query observer could otherwise retain data.
    def next_metric(request, name):
        return (
            request.url.endswith(
                "/loco.observability.v1.ObservabilityService/QueryMetrics"
            )
            and request.post_data_json.get("metricName") == name
            and request.post_data_json.get("attributes", {}).get("cluster")
            == clusters["de-he-1"]
        )

    with page.expect_request(
        lambda request: next_metric(request, "k8s.node.cpu.usage")
    ) as cpu_request:
        with page.expect_request(
            lambda request: next_metric(request, "k8s.node.memory.working_set")
        ) as memory_request:
            page.evaluate(
                "path => { history.pushState({}, '', path); dispatchEvent(new PopStateEvent('popstate')); }",
                next_path,
            )
    expect(page).to_have_url(ui + next_path)
    expect(signals.locator('[data-slot="skeleton"]')).to_have_count(2)
    expect(signals.get_by_label(re.compile(r"^(CPU|Memory) over time$"))).to_have_count(
        0
    )
    seen_metrics = [
        cpu_request.value.post_data_json,
        memory_request.value.post_data_json,
    ]
    assert_metric_scope(clusters["de-he-1"])
    stale_node = page.get_by_role("row", name="Inspect node-0000", exact=True)
    expect(stale_node).not_to_contain_text("%")
    for route in held_metrics:
        route.fulfill(json={})
    expect(
        page.get_by_text(
            "No data",
            exact=True,
        )
    ).to_have_count(2)
    hold_metrics = False
    nodes_region = page.get_by_role("region", name="Nodes", exact=True)
    with expect_rpc(
        page,
        "loco.cluster.v1.ClusterAdminService/ListClusterNodes",
        lambda body: body.get("page") == 1,
    ):
        nodes_region.get_by_role("button", name="Next page", exact=True).first.click()
    expect(
        page.get_by_role("row", name="Inspect node-0010", exact=True)
    ).to_be_visible()
    page.get_by_role("textbox", name="Find a node", exact=True).fill("node-0999")
    expect(
        page.get_by_role("row", name="Inspect node-0999", exact=True)
    ).to_be_visible()
    page.get_by_role("row", name="Inspect node-0999", exact=True).click()
    expect(page.locator('[data-slot="allocation-treemap"]')).to_have_count(1)
    # Stale requests retain all observed groups but cannot establish available capacity.
    expect(page.locator('[data-slot="allocation-treemap"] > *')).to_have_count(14)
    with expect_rpc(
        page,
        "loco.cluster.v1.ClusterAdminService/GetNodeAllocations",
        lambda body: body.get("remainderOnly") is True,
    ):
        page.get_by_role("button", name=re.compile("^Inspect Other:")).click()
    page.get_by_role("textbox", name="Search node consumers", exact=True).fill("0999")
    consumers = page.get_by_role("table", name="Node consumers", exact=True)
    expect(
        consumers.get_by_role("button", name="Workspace 0999", exact=True)
    ).to_be_visible()
    expect(consumers.get_by_role("row")).to_have_count(2)
    page.get_by_role("button", name=re.compile("^Inspect Workspace 0000:")).click()
    expect(
        consumers.get_by_role("columnheader", name="Resource", exact=True)
    ).to_be_visible()
    resource_link = consumers.get_by_role("link", name="capacity-api", exact=True)
    expect(resource_link).to_have_attribute(
        "href",
        re.compile(
            r"^/org/.+/wks/.+/resource/"
            + re.escape(state["resources"]["acme/shared/capacity-api"])
        ),
    )
    resource_link.click()
    expect(page.get_by_role("heading", name="capacity-api", exact=True)).to_be_visible()

    # Native node usage remains available while pod reservations have never reported.
    context.route(
        "**/loco.cluster.v1.ClusterAdminService/ListClusterNodes",
        lambda route: route.fulfill(
            json={
                "nodes": [{"name": "node-without-sample", "ready": True}],
                "totalCount": 1,
            }
        ),
    )
    seen_metrics.clear()
    page.goto(ui + "/system/clusters/" + clusters["us-ca-1-builds"])
    expect(page.get_by_text("Reservations not reported", exact=True)).to_be_visible()
    expect(signals.get_by_label(re.compile(r"^(CPU|Memory) over time$"))).to_have_count(
        2
    )
    assert_metric_scope(clusters["us-ca-1-builds"], "node-without-sample")
    assert not errors, errors
    context.close()

# Synthetic reservation replies exercise bounded tables and real workspace/resource routes.
with session(repo / "tests/ui/out/system-allocations") as browser:
    context, page, errors = system_page(browser, user="admin", state=state)
    inventory_calls = install_inventory(context, state)
    org = state["orgs"]["acme"]["id"]
    page.goto(f"{ui}/system/orgs/{org}")
    workspaces = page.get_by_role("region", name="Workspaces", exact=True)
    table = workspaces.get_by_role("table", name="Workspaces", exact=True)
    expect(table.get_by_role("row")).to_have_count(26)
    expect(table.get_by_role("row").nth(1)).to_contain_text("shared")
    expect(workspaces).to_contain_text("Partial reservations")
    with expect_rpc(
        page,
        "loco.cluster.v1.ClusterAdminService/ListWorkspaceAllocations",
        lambda body: body.get("page") == 1,
    ):
        workspaces.get_by_role("button", name="Next page", exact=True).click()
    expect(table.get_by_role("row")).to_have_count(7)
    # Sorting applies before paging and resets the page, rather than sorting its six rows.
    with expect_rpc(
        page,
        "loco.cluster.v1.ClusterAdminService/ListWorkspaceAllocations",
        lambda body: body.get("memory") is True and body.get("page", 0) == 0,
    ):
        table.get_by_role(
            "columnheader", name="Reserved memory", exact=True
        ).get_by_role("button").click()
    expect(table.get_by_role("row")).to_have_count(26)
    first_name = "Workspace 0030"
    if inventory_calls[-1][1].get("ascending"):
        first_name = "shared"
    expect(table.get_by_role("row").nth(1)).to_contain_text(first_name)
    search = workspaces.get_by_role("textbox", name="Search workspaces", exact=True)
    search.fill("Workspace 0030")
    expect(table.get_by_role("row")).to_have_count(2)
    expect(table.get_by_role("row").nth(1)).to_contain_text("Workspace 0030")
    search.fill("shared")
    expect(table.get_by_role("row")).to_have_count(2)
    table.get_by_role("link", name="shared", exact=True).click()
    resources = page.get_by_role("table", name="Resources", exact=True)
    expect(
        resources.get_by_role("columnheader", name="Configured size", exact=True)
    ).to_be_visible()
    row = resources.get_by_role("row").filter(
        has=page.get_by_role("link", name="capacity-api", exact=True)
    )
    expect(row).to_contain_text("0.125 vCPU")
    expect(row).to_contain_text("64 MiB")
    expect(row).to_contain_text("Workspace builds 0.3 vCPU")
    expect(row).to_contain_text("Workspace builds 128 MiB")
    assert any(kind == "resources" for kind, _ in inventory_calls), inventory_calls
    # Telemetry failure must leave cluster management and navigation usable.
    context.route(
        "**/loco.cluster.v1.ClusterAdminService/ListClusterCapacities",
        lambda route: route.fulfill(
            status=503,
            content_type="application/json",
            body='{"code":"unavailable","message":"Capacity source unavailable"}',
        ),
    )
    page.goto(ui + "/system/clusters")
    expect(page.get_by_role("table", name="Clusters", exact=True)).to_be_visible()
    expect(page.get_by_role("alert")).to_contain_text("Capacity source unavailable")
    expect(page.get_by_role("button", name="Add cluster", exact=True)).to_be_visible()
    assert not errors, errors
    context.close()
