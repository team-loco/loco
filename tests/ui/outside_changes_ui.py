import argparse
from pathlib import Path

from harness import call, evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check that the dashboard shows resources changed outside the tab"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/outside-changes"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "outside-changes", "tests/ui/fixtures/deployment-history/scenario.json"
)
org = state["orgs"]["acme"]["id"]
workspace = state["workspaces"]["acme/shared"]
service = {
    "routing": {"port": 8080},
    "regions": {"us-ca-1": {"enabled": True, "cpu": "100m", "memory": "128Mi"}},
}
resources = evidence.seed_resources(
    state,
    [
        {"workspace": "acme/shared", "name": name, "service": service}
        for name in ("outside-focus", "outside-navigate", "own-delete")
    ],
)


def delete(name):
    call(
        state,
        "loco.resource.v1.ResourceService/DeleteResource",
        {"resourceId": resources[f"acme/shared/{name}"]},
    )


with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    page.goto(state["ui_url"] + f"/org/{org}/wks/{workspace}/resources?env=production")

    def row(name):
        return page.get_by_role("link", name=name, exact=True)

    expect(row("outside-focus")).to_be_visible()
    expect(row("outside-navigate")).to_be_visible()

    delete("outside-focus")
    page.evaluate(
        "document.dispatchEvent(new Event('visibilitychange', { bubbles: true }))"
    )
    expect(row("outside-focus")).to_have_count(0)

    page.get_by_role("link", name="Dashboard", exact=True).click()
    expect(page.get_by_role("heading", level=1)).to_have_text("Dashboard")
    delete("outside-navigate")
    page.get_by_role("link", name="Resources", exact=True).click()
    expect(page.get_by_role("heading", level=1)).to_have_text("Resources")
    expect(row("outside-navigate")).to_have_count(0)
    row("own-delete").click()
    page.get_by_role("button", name="Resource settings", exact=True).click()
    page.get_by_role("button", name="Delete resource", exact=True).click()
    dialog = page.get_by_role("alertdialog")
    dialog.get_by_role("textbox").fill("own-delete")
    with page.expect_response(
        lambda response: (
            response.url.endswith("/ListWorkspaceResources")
            and response.request.post_data_json.get("pageSize") == 200
            and response.status == 200
        )
    ) as refreshed_resources:
        dialog.get_by_role("button", name="Delete own-delete", exact=True).click()
    deleted_id = resources["acme/shared/own-delete"]
    assert deleted_id not in {
        item["id"] for item in refreshed_resources.value.json().get("resources", [])
    }
    expect(page.get_by_role("heading", level=1)).to_have_text("Resources")
    expect(row("own-delete")).to_have_count(0)
    assert not errors, errors
    context.close()
print("Outside changes passed")
