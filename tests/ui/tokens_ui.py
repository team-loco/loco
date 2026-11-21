import argparse
import re

from harness import call, load_state, open_page, repo, session, sql
from playwright.sync_api import expect

parser = argparse.ArgumentParser(description="Check the new token drawer")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "tokens", "tests/ui/fixtures/audit/scenario.json")
org = state["orgs"]["acme"]["id"]
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
    page.goto(state["ui_url"] + "/tokens?owner=organization")
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
    "owner=user opens personal tokens, which name a workspace in another organization"
)
