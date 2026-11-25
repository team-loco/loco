import argparse
import re
from functools import partial

from harness import call, expect_rpc, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check the System area: who sees it, the cluster pages and their actions"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "system", "tests/ui/fixtures/system/scenario.json")
ui = state["ui_url"]

LIST_CLUSTERS = "loco.cluster.v1.ClusterAdminService/ListClusters"
LIST_REGIONS = "loco.cluster.v1.RegionAdminService/ListRegions"
SHELL_WATCH_JS = """
window.__systemChrome = false;
new MutationObserver(() => {
  if (document.querySelector('[data-slot="system-tag"], a[href="/system/clusters"]') !== null) window.__systemChrome = true;
}).observe(document, { childList: true, subtree: true });
"""


def serve(body, route):
    route.fulfill(json=body)


def open_menu(page):
    page.locator('[data-slot="account-button"]').click()


def row(page, name):
    return page.get_by_role("row", name=f"Open {name}", exact=True)


def row_menu(page, name, item):
    page.get_by_role("button", name=f"Actions for {name}", exact=True).click()
    page.get_by_role("menuitem", name=item, exact=True).click()


with session(repo / "tests/ui/out/system") as browser:
    # A user without a system role never sees the switch, and /system is not found.
    context, page, errors = open_page(browser, user="member", state=state)
    context.add_init_script(SHELL_WATCH_JS)
    page.goto(ui + "/dashboard")
    expect(page.get_by_title("Switch organization", exact=True)).to_be_visible()
    open_menu(page)
    expect(
        page.get_by_role("menuitem", name="Organizations", exact=True)
    ).to_be_visible()
    expect(
        page.get_by_role("menuitemcheckbox", name="System", exact=True)
    ).to_have_count(0)
    page.keyboard.press("Escape")
    for path in ("/system", "/system/clusters", "/system/clusters/new"):
        page.goto(ui + path)
        expect(
            page.get_by_role(
                "heading", name="Nothing lives at this address", exact=True
            )
        ).to_be_visible()
        assert not page.evaluate("window.__systemChrome"), path
    context.close()

    # A system admin switches in and out, and sees the clusters with their actions.
    context, page, errors = open_page(browser, user="admin", state=state)
    page.goto(ui + "/dashboard")
    open_menu(page)
    switch = page.get_by_role("menuitemcheckbox", name="System", exact=True)
    expect(switch).to_have_attribute("aria-checked", "false")
    switch.click()
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
    open_menu(page)
    switch = page.get_by_role("menuitemcheckbox", name="System", exact=True)
    expect(switch).to_have_attribute("aria-checked", "true")
    switch.click()
    expect(page).to_have_url(re.compile(r"/org/[\w-]+/wks/[\w-]+"))

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
    expect(page.get_by_text("nz-akl-1", exact=True)).to_be_visible()
    assert not [e for e in errors if e], errors
    context.close()

    # A system reader sees the same pages with no change controls. The API grants read access
    # to the list calls separately, so the page receives the admin's lists.
    listed = {
        method: call(state, method, {}, user="admin")
        for method in (LIST_CLUSTERS, LIST_REGIONS)
    }
    context, page, errors = open_page(browser, user="reader", state=state)
    for method, body in listed.items():
        context.route(f"**/{method}", partial(serve, body))
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
