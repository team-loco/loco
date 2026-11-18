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


def expect_row(section, signal, workspace, applied):
    expect(row(section, signal).get_by_role("cell")).to_have_text(
        [signal, workspace, install, applied]
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
    section = page.get_by_role("region", name="Retention", exact=True)
    for signal in ("Logs", "Traces", "Metrics"):
        expect_row(section, signal, "Not set", install)

    section.get_by_role("button", name="Edit retention").click()
    dialog = page.get_by_role("dialog")
    save = dialog.get_by_role("button", name="Save", exact=True)
    expect(save).to_be_disabled()
    dialog.get_by_role("textbox", name="Traces (days)").fill("400")
    expect(dialog.get_by_text("Enter whole days from 1 to 365")).to_be_visible()
    expect(save).to_be_disabled()
    dialog.get_by_role("textbox", name="Traces (days)").fill("")
    dialog.get_by_role("textbox", name="Logs (days)").fill("7")
    save.click()
    expect(dialog).to_be_hidden()
    expect_row(section, "Logs", "7 days", "7 days")
    expect_row(section, "Traces", "Not set", install)
    assert stored() == {"logDays": 7}, stored()

    section.get_by_role("button", name="Edit retention").click()
    dialog.get_by_role("textbox", name="Logs (days)").fill("")
    dialog.get_by_role("button", name="Save", exact=True).click()
    expect(dialog).to_be_hidden()
    expect_row(section, "Logs", "Not set", install)
    assert stored() == {}, stored()
    assert not errors, errors
    context.close()

    context, page, errors = open_page(browser, "member", state=state)
    page.goto(path)
    section = page.get_by_role("region", name="Retention", exact=True)
    expect_row(section, "Logs", "Not set", install)
    expect(section.get_by_role("button", name="Edit retention")).to_be_disabled()
    assert not errors, errors
    context.close()
print("Workspace retention dashboard checks passed")
