import argparse
import re
from pathlib import Path

import evidence
from harness import call, expect_rpc, load_state, open_page, repo, session, sql
from playwright.sync_api import expect

parser = argparse.ArgumentParser(description="Check the new token drawer")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "tokens", "tests/ui/fixtures/audit/scenario.json")
org = state["orgs"]["acme"]["id"]
shared = state["workspaces"]["acme/shared"]
sibling = state["workspaces"]["acme/sibling"]
private = state["workspaces"]["acme/private"]
if "tokens_resources" not in state:
    service = {
        "routing": {"port": 8080},
        "regions": {"us-ca-1": {"enabled": True, "cpu": "100m", "memory": "128Mi"}},
    }
    state["tokens_resources"] = evidence.seed_resources(
        state,
        [
            {"workspace": "acme/shared", "name": "storefront-web", "service": service},
            {"workspace": "acme/sibling", "name": "inventory-api", "service": service},
            {"workspace": "acme/private", "name": "ledger", "service": service},
        ],
    )
    evidence.write_json(Path(args.state), state)
call(
    state,
    "loco.token.v1.TokenService/CreateToken",
    {
        "name": "ci-source",
        "entityType": "ENTITY_TYPE_ORGANIZATION",
        "entityId": org,
        "expiresInSec": "3600",
        "scopes": [
            {
                "entityType": "ENTITY_TYPE_ORGANIZATION",
                "entityId": org,
                "scope": "SCOPE_READ",
            }
        ],
    },
)

with session(repo / "tests/ui/out/tokens") as browser:
    # An expired token is listed but leaves room under a limit of one.
    sql(
        state,
        f"INSERT INTO org_limit_overrides (org_id, tokens_per_entity, reason) VALUES ('{org}', 1, 'test');"
        " UPDATE api_tokens SET expires_at = NOW() - INTERVAL '1 day' WHERE name = 'ci-source';",
    )
    context, page, errors = open_page(browser, state=state)
    page.goto(state["ui_url"] + "/tokens?owner=org")
    expect(page.get_by_text("expired 1d ago")).to_be_visible()
    expect(page.get_by_role("button", name="New token", exact=True)).to_be_enabled()
    assert not errors, errors
    context.close()
    sql(
        state,
        f"DELETE FROM org_limit_overrides WHERE org_id = '{org}';"
        " UPDATE api_tokens SET expires_at = NOW() + INTERVAL '1 hour' WHERE name = 'ci-source';",
    )

    context, page, errors = open_page(browser, narrow=True, state=state)
    page.goto(state["ui_url"] + "/tokens?owner=org")
    organization = page.get_by_role("group", name="Owner").get_by_role(
        "button", name=re.compile(r"^Organization")
    )
    expect(organization.first).to_have_attribute("aria-pressed", "true")
    page.get_by_role("button", name="Actions for ci-source").click()
    page.get_by_role("menuitem", name="Duplicate").click()
    drawer = page.get_by_role("dialog", name="New token")
    name = drawer.get_by_label("Name", exact=True)
    expect(name).to_have_value("ci-source-copy")

    name.fill("")
    name.press("Enter")
    expect(drawer.get_by_text("Enter a name.", exact=True)).to_be_visible()

    name.fill("QA Token <script>")
    expect(name).to_have_value("QA Token <script>")
    expect(drawer.get_by_text("Saved as qa-token--script-")).to_be_visible()

    subtitle = drawer.get_by_text("Organization · everything in it", exact=True)
    assert subtitle.evaluate("node => node.scrollWidth <= node.clientWidth")

    drawer.get_by_role("button", name="Create token", exact=True).click()
    created = page.get_by_role("dialog", name="Token created")
    expect(created.get_by_text("qa-token--script-", exact=True)).to_be_visible()
    for block in created.locator('[data-slot="code-block"] pre').all():
        assert block.evaluate("node => node.scrollWidth <= node.clientWidth")
    page.keyboard.press("Escape")
    expect(created).to_be_hidden()
    assert not errors, errors
    context.close()

    context, page, errors = open_page(browser, state=state)
    page.goto(state["ui_url"] + "/tokens?owner=org")
    menu = page.get_by_role("button", name="Actions for ci-source")
    expect(menu).to_be_visible()
    container = page.locator('[data-slot="data-table"]').first
    assert container.get_attribute("data-scroll") is None
    assert container.evaluate("node => getComputedStyle(node).maskImage") == "none"
    gap = menu.evaluate(
        "node => node.closest('[data-slot=\"data-table\"]').getBoundingClientRect().right"
        " - node.getBoundingClientRect().right"
    )
    padding = menu.evaluate(
        "node => parseFloat(getComputedStyle(node.closest('td')).paddingRight)"
    )
    assert gap >= padding >= 20, (gap, padding)
    assert not errors, errors
    context.close()

    # The picker lists one page of workspaces and one page of the organization's resources, both searched on the server.
    context, page, errors = open_page(
        browser,
        state=state,
        storage={"loco:active-org:v1": org, "loco:active-workspace:v1": shared},
    )
    listed = []
    page.on(
        "request",
        lambda request: (
            listed.append(request.post_data_json["workspaceId"])
            if request.url.endswith("/ListWorkspaceResources")
            else None
        ),
    )
    page.goto(state["ui_url"] + "/tokens?owner=org")
    with expect_rpc(
        page,
        "loco.resource.v1.ResourceService/ListOrgResources",
        lambda body: body.get("orgId") == org and body.get("pageSize") == 40,
    ):
        page.get_by_role("button", name="New token", exact=True).first.click()
    drawer = page.get_by_role("dialog", name="New token")
    search = drawer.locator('input[aria-label="Add a workspace or resource"]')
    search.focus()
    options = page.get_by_role("listbox")
    expect(
        options.get_by_role("option", name=re.compile(r"^storefront-web"))
    ).to_be_visible()
    expect(options.get_by_role("option", name=re.compile(r"^private"))).to_be_visible()
    expect(
        options.get_by_role("option", name=re.compile(r"^inventory-api"))
    ).to_contain_text("sibling")
    assert sibling not in listed and private not in listed, listed

    # A search without a slash finds resources in workspaces other than the active one.
    with expect_rpc(
        page,
        "loco.resource.v1.ResourceService/ListOrgResources",
        lambda body: body.get("query") == "ledg",
    ):
        search.fill("ledg")
    expect(options.get_by_role("option", name=re.compile(r"^ledger"))).to_contain_text(
        "private"
    )

    with (
        expect_rpc(
            page,
            "loco.workspace.v1.WorkspaceService/ListOrgWorkspaces",
            lambda body: body.get("query") == "sib" and body.get("pageSize") == 40,
        ),
        expect_rpc(
            page,
            "loco.resource.v1.ResourceService/ListOrgResources",
            lambda body: body.get("query") == "sib",
        ),
    ):
        search.fill("sib")
    workspace = options.get_by_role("option", name=re.compile(r"^sibling"))
    expect(workspace).to_contain_text("Workspace · 1 resources")
    expect(options.get_by_role("option", name=re.compile(r"^private"))).to_have_count(0)
    with expect_rpc(
        page,
        "loco.resource.v1.ResourceService/ListWorkspaceResources",
        lambda body: body.get("workspaceId") == sibling and "query" not in body,
    ):
        workspace.get_by_role("button", name="Show resources in sibling").click()
    expect(search).to_have_value("sibling/")
    expect(drawer.get_by_role("group", name="Level for sibling")).to_have_count(0)
    options.get_by_role("option", name=re.compile(r"^inventory-api")).click()
    expect(drawer.get_by_role("group", name="Level for inventory-api")).to_be_visible()

    search.focus()
    with expect_rpc(
        page,
        "loco.resource.v1.ResourceService/ListWorkspaceResources",
        lambda body: body.get("workspaceId") == shared and body.get("query") == "front",
    ):
        search.fill("shared/front")
    expect(
        options.get_by_role("option", name=re.compile(r"^storefront-web"))
    ).to_be_visible()
    assert private not in listed, listed

    # ArrowRight on a highlighted workspace lists its resources; the pointer leaves the list, where hovering moves the highlight.
    page.mouse.move(0, 0)
    search.fill("sibling")
    expect(
        options.get_by_role("option", name=re.compile(r"^sibling"))
    ).to_have_attribute("data-highlighted", "")
    search.press("ArrowRight")
    expect(search).to_have_value("sibling/")

    # A resource listed under a workspace shows the access its organization already grants.
    search.fill("")
    options.get_by_role("option", name=re.compile(r"^acme")).click()
    expect(drawer.get_by_role("group", name="Level for acme")).to_be_visible()
    search.focus()
    search.fill("shared/front")
    expect(
        options.get_by_role("option", name=re.compile(r"^storefront-web"))
    ).to_contain_text("Included in acme")
    assert not errors, errors
    context.close()

    # Duplicating waits for a resource's workspace, through which the organization grants its access.
    call(
        state,
        "loco.token.v1.TokenService/CreateToken",
        {
            "name": "inherits",
            "entityType": "ENTITY_TYPE_ORGANIZATION",
            "entityId": org,
            "expiresInSec": "3600",
            "scopes": [
                {
                    "entityType": "ENTITY_TYPE_RESOURCE",
                    "entityId": state["tokens_resources"]["acme/sibling/inventory-api"],
                    "scope": "SCOPE_READ",
                }
            ],
        },
    )
    context, page, errors = open_page(
        browser,
        state=state,
        storage={"loco:active-org:v1": org, "loco:active-workspace:v1": shared},
    )
    held = []

    def hold_sibling(route):
        if (route.request.post_data_json or {}).get("workspaceId") == sibling:
            held.append(route)
        else:
            route.fallback()

    page.route("**/loco.workspace.v1.WorkspaceService/GetWorkspace", hold_sibling)
    page.goto(state["ui_url"] + "/tokens?owner=org")
    page.get_by_role("button", name="Actions for inherits").click()
    page.get_by_role("menuitem", name="Duplicate").click()
    drawer = page.get_by_role("dialog", name="New token")
    expect(drawer).to_have_count(0)
    assert held, "GetWorkspace for sibling was not requested"
    for route in held:
        route.fallback()
    page.unroute("**/loco.workspace.v1.WorkspaceService/GetWorkspace")
    expect(drawer.get_by_role("group", name="Level for inventory-api")).to_be_visible()
    expect(drawer.get_by_text("sibling", exact=True)).to_be_visible()
    assert not errors, errors
    context.close()

    # A member is offered only the workspaces it can read.
    context, page, errors = open_page(browser, "member", state=state)
    page.goto(state["ui_url"] + "/tokens?owner=user")
    page.get_by_role("button", name="New token", exact=True).first.click()
    drawer = page.get_by_role("dialog", name="New token")
    search = drawer.locator('input[aria-label="Add a workspace or resource"]')
    search.focus()
    options = page.get_by_role("listbox")
    expect(options.get_by_role("option", name=re.compile(r"^shared"))).to_be_visible()
    expect(options.get_by_role("option", name=re.compile(r"^private"))).to_have_count(0)
    assert not errors, errors
    context.close()

    beta = call(state, "loco.org.v1.OrgService/CreateOrg", {"name": "beta"})["orgId"]
    outside = call(
        state,
        "loco.workspace.v1.WorkspaceService/CreateWorkspace",
        {"orgId": beta, "name": "outside"},
    )["workspaceId"]
    call(
        state,
        "loco.token.v1.TokenService/CreateToken",
        {
            "name": "laptop",
            "entityType": "ENTITY_TYPE_USER",
            "entityId": state["users"]["owner"]["id"],
            "expiresInSec": "3600",
            "scopes": [
                {
                    "entityType": "ENTITY_TYPE_WORKSPACE",
                    "entityId": outside,
                    "scope": "SCOPE_READ",
                }
            ],
        },
    )
    context, page, errors = open_page(
        browser, state=state, storage={"loco:active-org:v1": org}
    )
    page.goto(state["ui_url"] + "/tokens")
    owners = page.get_by_role("group", name="Owner")
    personal = owners.get_by_role("button", name=re.compile(r"^Personal"))
    personal.click()
    expect(page).to_have_url(re.compile(r"[?&]owner=user(&|$)"))
    page.reload()
    expect(personal).to_have_attribute("aria-pressed", "true")
    row = page.get_by_role("row").filter(has_text="laptop")
    expect(row.get_by_text("outside workspace", exact=True)).to_be_visible()
    assert not errors, errors
    context.close()
print(
    "PASS: the token drawer shows the saved name, wraps text and closes on Escape; the row menu keeps the cell padding; "
    "the access picker searches workspaces on the server and lists only the resources of the workspaces it shows; "
    "owner=user opens personal tokens, which name a workspace in another organization"
)
