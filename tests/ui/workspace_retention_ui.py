import argparse
from pathlib import Path

from harness import call, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check that workspace settings show and edit the workspace's telemetry retention"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/workspace-retention"))
args = parser.parse_args()
state = load_state(
    args, "workspace-retention", "tests/ui/fixtures/settings-labels/scenario.json"
)
out = Path(args.out)
workspace_id = state["workspaces"]["acme/shared"]
orgs = call(
    state,
    "loco.org.v1.OrgService/ListUserOrgs",
    {"userId": state["users"]["owner"]["id"]},
)
org_id = next(org["id"] for org in orgs["orgs"] if org["name"] == "acme")
path = f"{state['ui_url']}/org/{org_id}/wks/{workspace_id}/settings?scope=workspace"
install = "365 days"


def stored():
    return call(
        state,
        "loco.workspace.v1.WorkspaceService/GetWorkspace",
        {"workspaceId": workspace_id},
    )["workspace"].get("retention", {})


def row(section, signal):
    return section.get_by_role("row").filter(
        has=section.page.get_by_role("cell", name=signal, exact=True)
    )


def expect_row(section, signal, kept, source):
    expect(row(section, signal).get_by_role("cell")).to_have_text(
        [signal, kept + source, install]
    )


call(
    state,
    "loco.workspace.v1.WorkspaceService/UpdateWorkspace",
    {
        "workspaceId": workspace_id,
        "logRetentionDays": 0,
        "traceRetentionDays": 0,
        "metricRetentionDays": 0,
    },
)

with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    page.goto(path)
    section = page.get_by_role("region", name="Data retention", exact=True)
    for signal in ("Logs", "Traces", "Metrics"):
        expect_row(section, signal, install, "organization limit")

    edit = section.get_by_role("button", name="Edit retention")
    edit.click()
    expect(page.get_by_role("dialog")).to_have_count(0)
    expect(edit).to_have_count(0)
    save = section.get_by_role("button", name="Save", exact=True)
    expect(save).to_be_disabled()
    traces = section.get_by_role("textbox", name="Traces retention in days")
    traces.fill("400")
    expect(section.get_by_text("Whole days, 1 to 365")).to_be_visible()
    expect(save).to_be_disabled()
    row(section, "Traces").get_by_role("button", name="Use limit").click()
    expect(traces).to_have_value("")
    section.get_by_role("textbox", name="Logs retention in days").fill("7")
    save.click()
    expect(save).to_have_count(0)
    expect_row(section, "Logs", "7 days", "set for this workspace")
    expect_row(section, "Traces", install, "organization limit")
    assert stored() == {"logDays": 7}, stored()

    section.get_by_role("button", name="Edit retention").click()
    logs = section.get_by_role("textbox", name="Logs retention in days")
    expect(logs).to_have_value("7")
    section.get_by_role("button", name="Cancel", exact=True).click()
    expect(logs).to_have_count(0)
    expect_row(section, "Logs", "7 days", "set for this workspace")

    section.get_by_role("button", name="Edit retention").click()
    row(section, "Logs").get_by_role("button", name="Use limit").click()
    section.get_by_role("button", name="Save", exact=True).click()
    expect(section.get_by_role("button", name="Edit retention")).to_be_visible()
    expect_row(section, "Logs", install, "organization limit")
    assert stored() == {}, stored()
    assert not errors, errors
    context.close()

    context, page, errors = open_page(browser, "member", state=state)
    page.goto(path)
    section = page.get_by_role("region", name="Data retention", exact=True)
    expect_row(section, "Logs", install, "organization limit")
    expect(section.get_by_role("button", name="Edit retention")).to_be_disabled()
    assert not errors, errors
    context.close()
print("Workspace retention dashboard checks passed")
