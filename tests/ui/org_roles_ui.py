import argparse
import re
import uuid
from datetime import datetime

from harness import evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check organization role changes in the production UI"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "org-roles", "tests/ui/fixtures/org-roles/scenario.json")
org = evidence.rpc(
    state["api_url"],
    state["users"]["owner"]["token"],
    "loco.org.v1.OrgService/ListUserOrgs",
    {"userId": state["users"]["owner"]["id"]},
)["orgs"][0]
path = state["ui_url"] + f"/org/{org['id']}/team"

with session(repo / "tests/ui/out/org_roles") as browser:
    context, page, errors = open_page(browser, state=state)
    requests = []
    page.on("request", lambda request: requests.append(request.url))
    page.goto(state["ui_url"] + "/organizations")
    organizations = page.get_by_role("table", name="Organizations", exact=True)
    expect(
        organizations.get_by_role("columnheader", name="Workspaces", exact=True)
    ).to_be_visible()
    expect(organizations.get_by_text("Owner", exact=True)).to_be_visible()
    row = organizations.get_by_role("link", name="Open acme", exact=True)
    expect(row.get_by_role("cell").nth(2)).to_have_text("2")
    expect(row.get_by_role("cell").nth(3)).to_have_text("3")
    page.get_by_role("button", name="Actions for acme", exact=True).click()
    expect(page.get_by_role("menuitem", name="Settings", exact=True)).to_be_visible()
    page.get_by_role("menuitem", name="Delete organization", exact=True).click()
    expect(page).to_have_url(re.compile(r"#organization-danger$"))
    expect(
        page.get_by_role("button", name="Delete organization", exact=True)
    ).to_be_in_viewport()
    page.goto(state["ui_url"] + "/organizations?new=1")
    dialog = page.get_by_role("dialog", name="New organization", exact=True)
    name = dialog.get_by_role("textbox", name="Name", exact=True)
    create = dialog.get_by_role("button", name="Create", exact=True)
    name.fill("  acme  ")
    expect(dialog.get_by_text("acme is taken.", exact=True)).to_be_visible()
    expect(create).to_be_disabled()
    name.fill("   ")
    expect(create).to_be_disabled()
    name.fill("a" * 101)
    expect(
        dialog.get_by_text("Use at most 100 characters.", exact=True)
    ).to_be_visible()
    expect(create).to_be_disabled()
    for accepted in ("a", "ACME", "Acme & Partners", "a" * 100, "🚀" * 100):
        name.fill(accepted)
        expect(name).to_have_value(accepted)
        expect(create).to_be_enabled()
    name.fill("🚀" * 101)
    expect(create).to_be_disabled()
    dialog.get_by_role("button", name="Cancel", exact=True).click()
    expect(page).not_to_have_url(re.compile(r"[?&]new="))
    page.goto(state["ui_url"] + "/organizations?new=1")
    name.fill("  Acme & Partners 🚀  ")
    with page.expect_response(
        lambda response: response.url.endswith("/CreateOrg")
    ) as creation:
        create.click()
    assert creation.value.ok
    assert creation.value.request.post_data_json == {"name": "Acme & Partners 🚀"}
    expect(dialog).to_be_hidden()
    created_org = creation.value.json()["orgId"]
    workspaces = evidence.rpc(
        state["api_url"],
        state["users"]["owner"]["token"],
        "loco.workspace.v1.WorkspaceService/ListOrgWorkspaces",
        {"orgId": created_org},
    ).get("workspaces", [])
    assert [workspace["name"] for workspace in workspaces] == ["default"], workspaces
    expect(page).to_have_url(
        re.compile(rf"/org/{created_org}/wks/{workspaces[0]['id']}(\?env=[\w-]+)?$")
    )
    evidence.rpc(
        state["api_url"],
        state["users"]["owner"]["token"],
        "loco.org.v1.OrgService/DeleteOrg",
        {"orgId": created_org},
    )
    shared = state["workspaces"]["acme/shared"]
    page.goto(state["ui_url"] + f"/org/{org['id']}/wks/{shared}/resources")
    page.get_by_title("Switch workspace", exact=True).click()
    page.get_by_role("menuitem", name="New organization", exact=True).click()
    name.fill("sidebar-" + uuid.uuid4().hex[:8])
    workspace_rpc = "**/loco.workspace.v1.WorkspaceService/CreateWorkspace"
    context.route(
        workspace_rpc,
        lambda route: route.fulfill(
            status=503,
            json={"code": "unavailable", "message": "Workspace creation unavailable"},
        ),
    )
    before_orgs = sum(url.endswith("/CreateOrg") for url in requests)
    with page.expect_response(
        lambda response: response.url.endswith("/CreateOrg")
    ) as sidebar_creation:
        create.click()
    expect(
        page.get_by_text("Workspace creation unavailable.", exact=True)
    ).to_be_visible()
    expect(name).to_be_disabled()
    expect(create).to_be_enabled()
    sidebar_org = sidebar_creation.value.json()["orgId"]
    context.unroute(workspace_rpc)
    with page.expect_response(
        lambda response: response.url.endswith("/CreateWorkspace")
    ) as workspace_creation:
        create.click()
    assert workspace_creation.value.ok
    workspace_id = workspace_creation.value.json()["workspaceId"]
    expect(dialog).to_be_hidden()
    expect(page).to_have_url(
        re.compile(rf"/org/{sidebar_org}/wks/{workspace_id}(\?env=[\w-]+)?$")
    )
    assert sum(url.endswith("/CreateOrg") for url in requests) == before_orgs + 1
    workspaces = evidence.rpc(
        state["api_url"],
        state["users"]["owner"]["token"],
        "loco.workspace.v1.WorkspaceService/ListOrgWorkspaces",
        {"orgId": sidebar_org},
    ).get("workspaces", [])
    assert [workspace["name"] for workspace in workspaces] == ["default"], workspaces
    evidence.rpc(
        state["api_url"],
        state["users"]["owner"]["token"],
        "loco.org.v1.OrgService/DeleteOrg",
        {"orgId": sidebar_org},
    )
    page.goto(path)
    page.wait_for_load_state("networkidle")
    members = evidence.rpc(
        state["api_url"],
        state["users"]["owner"]["token"],
        "loco.org.v1.OrgService/ListOrgMembers",
        {"orgId": org["id"]},
    )["members"]
    expect(page.get_by_role("columnheader", name="Joined", exact=True)).to_be_visible()
    expect(
        page.get_by_role("columnheader", name="Last active", exact=True)
    ).to_be_visible()
    for member in members:
        joined = (
            datetime.fromisoformat(member["joinedAt"].replace("Z", "+00:00"))
            .astimezone()
            .strftime("%b %-d, %Y")
        )
        row = page.get_by_role("row").filter(has_text=member["user"]["name"])
        expect(row.get_by_role("cell").nth(5)).to_have_text(joined)
    invitations_before = sum(url.endswith("/ListInvitations") for url in requests)
    scopes_before = sum(url.endswith("/GetScopes") for url in requests)
    member_role = page.get_by_role(
        "button", name="Change role for Max Member", exact=True
    )
    owner_role = page.get_by_role(
        "button", name="Change role for Olivia Owner", exact=True
    )
    expect(member_role).to_have_text("Member")
    owner_role.click()
    page.get_by_role("menuitem", name=re.compile(r"^Member")).click()
    page.get_by_role("button", name="Change role", exact=True).click()
    page.get_by_text(
        "Organization must keep at least one admin.", exact=True
    ).wait_for()
    expect(page.get_by_role("alertdialog")).to_be_visible()
    page.get_by_role("button", name="Cancel", exact=True).click()
    member_role.focus()
    page.keyboard.press("Enter")
    page.get_by_role("menuitem", name=re.compile(r"^Admin")).click()
    page.get_by_role("button", name="Cancel", exact=True).click()
    expect(member_role).to_have_text("Member")
    member_role.click()
    page.get_by_role("menuitem", name=re.compile(r"^Admin")).click()
    page.get_by_role("button", name="Change role", exact=True).click()
    expect(member_role).to_have_text("Admin")
    member_row = page.get_by_role("row").filter(has_text="Max Member")
    expect(member_row.get_by_text("All workspaces", exact=True)).to_be_visible()
    member_role.click()
    page.get_by_role("menuitem", name=re.compile(r"^Member")).click()
    page.get_by_role("button", name="Change role", exact=True).click()
    expect(member_role).to_have_text("Member")
    expect(member_row.get_by_text("shared", exact=True)).to_be_visible()
    page.get_by_role("button", name="Workspace", exact=True).click()
    page.get_by_role("option", name=re.compile(r"^shared")).click()
    expect(page).to_have_url(re.compile(r"[?&]ws="))
    expect(page.get_by_role("row").filter(has_text="Lee Leaver")).to_have_count(0)
    expect(member_row).to_be_visible()
    page.get_by_role("button", name="Clear workspace filter", exact=True).click()
    expect(page.get_by_role("row").filter(has_text="Lee Leaver")).to_have_count(1)
    page.get_by_role("button", name="Actions for Max Member", exact=True).click()
    page.get_by_role("menuitem", name="Workspace access", exact=True).click()
    drawer = page.get_by_role("dialog", name="Max Member")
    expect(page).to_have_url(re.compile(r"[?&]access="))
    expect(
        drawer.get_by_role("group", name="Access to shared", exact=True)
    ).to_be_visible()
    expect(
        drawer.get_by_role("group", name="Access to sibling", exact=True)
    ).to_have_count(0)
    drawer.get_by_role("button", name=re.compile(r"^All workspaces")).click()
    expect(
        drawer.get_by_role("group", name="Access to sibling", exact=True)
    ).to_be_visible()
    page.keyboard.press("Escape")
    expect(page).not_to_have_url(re.compile(r"[?&]access="))
    member_role.click()
    page.get_by_role("menuitem", name=re.compile(r"^Admin")).click()
    page.get_by_role("button", name="Change role", exact=True).click()
    expect(member_role).to_have_text("Admin")
    assert sum(url.endswith("/GetScopes") for url in requests) == scopes_before
    page.get_by_role("button", name="Actions for Lee Leaver", exact=True).click()
    page.get_by_role("menuitem", name="Remove from acme", exact=True).click()
    expect(page.get_by_role("alertdialog")).to_contain_text(
        "Remove Lee Leaver from acme?"
    )
    page.get_by_role("button", name="Remove", exact=True).click()
    expect(page.get_by_role("row").filter(has_text="Lee Leaver")).to_have_count(0)
    owner_role.click()
    page.get_by_role("menuitem", name=re.compile(r"^Member")).click()
    page.get_by_role("button", name="Change role", exact=True).click()
    expect(owner_role).to_have_text("Member")
    expect(owner_role).to_be_disabled()
    expect(member_role).to_be_disabled()
    assert sum(url.endswith("/GetScopes") for url in requests) > scopes_before
    assert (
        sum(url.endswith("/ListInvitations") for url in requests) == invitations_before
    )
    member_token = state["users"]["member"]["token"]
    for user, role in (("owner", "ORG_ROLE_ADMIN"), ("member", "ORG_ROLE_MEMBER")):
        evidence.rpc(
            state["api_url"],
            member_token,
            "loco.org.v1.OrgService/SetOrgMemberRole",
            {
                "orgId": org["id"],
                "userId": state["users"][user]["id"],
                "role": role,
            },
        )
    assert not errors, errors
    context.close()
    context, page, errors = open_page(browser, "member", narrow=True, state=state)
    page.goto(state["ui_url"] + "/organizations")
    organizations = page.get_by_role("table", name="Organizations", exact=True)
    expect(organizations.get_by_text("Member", exact=True)).to_be_visible()
    expect(
        organizations.get_by_role("columnheader", name="Workspaces", exact=True)
    ).to_be_hidden()
    page.get_by_role("button", name="Actions for acme", exact=True).click()
    expect(page.get_by_role("menuitem", name="Settings", exact=True)).to_have_count(0)
    expect(
        page.get_by_role("menuitem", name="Delete organization", exact=True)
    ).to_have_count(0)
    page.keyboard.press("Escape")
    page.get_by_role("link", name="Open acme", exact=True).focus()
    page.keyboard.press("Enter")
    expect(page).not_to_have_url(re.compile(r"/organizations"))
    page.goto(path)
    expect(
        page.get_by_role("button", name="Change role for Max Member", exact=True)
    ).to_be_disabled()
    assert not errors, errors
    context.close()
    browser.close()
print(
    "Organization role changes and member removal passed; member controls disabled at narrow width"
)
