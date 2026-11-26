import argparse
import re

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
    # A user without a system role never sees the switch.
    context, page, errors = open_page(browser, user="member", state=state)
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
            context, page, errors = open_page(browser, user="member", state=state)
            context.add_init_script(SHELL_WATCH_JS)
            requests = []
            page.on("request", lambda request, seen=requests: seen.append(request.url))
            held = hold(context, delayed)
            page.goto(ui + path, wait_until="commit")
            page.wait_for_timeout(500)
            assert not page.evaluate("window.__systemChrome"), (delayed, path)
            not_found = page.get_by_role(
                "heading", name="Nothing lives at this address", exact=True
            )
            if delayed == USER_ORGS:
                expect(not_found).to_be_visible()
            for route in held:
                route.continue_()
            expect(not_found).to_be_visible()
            assert not page.evaluate("window.__systemChrome"), (delayed, path)
            assert not fleet_calls(requests), (delayed, path, requests)
            context.close()

    # A system admin switches in and out, and sees the clusters with their actions.
    context, page, errors = open_page(browser, user="admin", state=state)
    page.goto(ui + "/dashboard")
    open_menu(page)
    switch = page.get_by_role("menuitemcheckbox", name="System", exact=True)
    expect(switch).to_have_attribute("aria-checked", "false")
    origin = page.url
    thumb = switch.locator("[data-slot=switch-thumb]")
    thumb.evaluate("""el => el.addEventListener('transitionrun', () => {
      el.getAnimations().forEach(animation => animation.pause());
      window.systemSwitchPaused = true;
    }, {once: true})""")
    switch.click()
    page.wait_for_function("window.systemSwitchPaused === true")
    expect(switch).to_have_attribute("aria-checked", "true")
    expect(switch).to_be_visible()
    expect(page).to_have_url(origin)
    thumb.evaluate("el => el.getAnimations().forEach(animation => animation.finish())")
    expect(page).to_have_url(ui + "/system")
    expect(
        page.get_by_role("heading", name="System overview", exact=True)
    ).to_be_visible()
    expect(page.locator('[data-slot="system-tag"]')).to_have_text("System")
    sidebar = page.locator('[data-slot="sidebar"]')
    expect(sidebar.get_by_role("link", name="Clusters", exact=True)).to_be_visible()
    expect(sidebar.get_by_role("link", name="Resources", exact=True)).to_have_count(0)
    # Problems come first: the unhealthy and unreporting regions before the healthy one.
    regions = page.locator("section h2").all_inner_texts()
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
        ("State", "Degraded"),
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
        page.get_by_role("heading", name="Nothing lives at this address", exact=True)
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
    context, page, errors = open_page(browser, user="reader", state=state)
    page.goto(ui + "/dashboard")
    open_menu(page)
    page.get_by_role("menuitemcheckbox", name="System", exact=True).click()
    expect(
        page.get_by_role("heading", name="System overview", exact=True)
    ).to_be_visible()
    page.goto(ui + "/system/clusters")
    table = page.get_by_role("table", name="Clusters", exact=True)
    expect(table.get_by_role("row", name="Open us-ca-1", exact=True)).to_be_visible()
    expect(page.get_by_role("button", name=re.compile("^Actions for"))).to_have_count(0)
    expect(page.get_by_role("link", name="Add cluster", exact=True)).to_have_count(0)
    page.get_by_role("row", name="Open us-ca-1", exact=True).click()
    expect(page.get_by_role("heading", name=re.compile("^us-ca-1 "))).to_be_visible()
    for name in ("Pause", "Resume", "Remove", "Restore", "Issue join token"):
        expect(page.get_by_role("button", name=name, exact=True)).to_have_count(0)
    page.goto(ui + "/system/clusters/new")
    expect(
        page.get_by_role("heading", name="Nothing lives at this address", exact=True)
    ).to_be_visible()
    context.close()
